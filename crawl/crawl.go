package crawl

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"golang.org/x/net/html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// parseLinks looks for html links in an io stream. It tries to continue on any errors as if nothing was wrong until
// it encounters the end of the io stream (assuming a logger is setup on the context passed in errors will be logged
// at a warn level though).
func parseLinks(ctx context.Context, r io.Reader) []string {
	t := html.NewTokenizer(r)
	ret := make([]string, 0)
	processing := true
	for processing {
		tt := t.Next()
		if tt == html.ErrorToken {
			err := t.Err()
			switch t.Err() {
			case io.EOF:
				processing = false
			default:
				zerolog.Ctx(ctx).Warn().Err(err).Msg("error encountered parsing document")
			}
		}

		if tt == html.StartTagToken {
			tagName, hasMoreAttrs := t.TagName()
			if string(tagName) == "a" {
				for hasMoreAttrs {
					var attr []byte
					var val []byte
					attr, val, hasMoreAttrs = t.TagAttr()
					if string(attr) == "href" {
						ret = append(ret, string(val))
					}
				}
			}
		}
	}
	return ret
}

// DocumentReader reads all links from a URL. Create one with NewDocumentFetcher
type DocumentReader func(ctx context.Context, url string) ([]string, error)

// ReadDocument is the non-test implementation of DocumentReader
func ReadDocument(ctx context.Context, url string) (strings []string, err error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	req = req.WithContext(ctx)
	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer res.Body.Close()
	zerolog.Ctx(ctx).Debug().Str("url", url).Msg("extracting urls")
	ret := parseLinks(ctx, res.Body)
	zerolog.Ctx(ctx).Debug().Str("url", url).Msg("done extracting urls")
	return ret, nil
}

type URLEligibilityChecker func(url string) bool

func SameDomainEligibilityChecker(sourceURL string) URLEligibilityChecker {
	u, err := url.Parse(sourceURL)
	if err != nil {
		panic(err)
	}
	return func(targetURL string) bool {
		test, err := url.Parse(targetURL)
		if err != nil {
			return false
		}
		if test.Scheme != "http" && test.Scheme != "https" {
			return false
		}
		return strings.ToLower(test.Host) == strings.ToLower(u.Host)
	}
}

type Crawler func(url string) ([]string, error)

// NewCrawler returns a crawler and channel that can be used to shut it down
func NewCrawler(baseLogger zerolog.Logger, workerCount int, readTimeout time.Duration, extractLinks DocumentReader, shouldIncludeURL URLEligibilityChecker) (Crawler, chan bool) {
	stopSignal := make(chan bool)
	return func(url string) ([]string, error) {
		baseLogger.Debug().Str("url", url).Msg("starting crawl")
		urlsSeen := map[string]bool{
			url: true,
		}
		urlsLock := sync.Mutex{}

		urlsToProcess := make(chan string, workerCount)

		// we cant just use a wait group since workers are also feeding work. There may be a better way wait for them
		// to all be done, but just waiting for them to be idle for .5 seconds seems like a good-enough solution for
		// the current use case. It could also be feed with a chanel removing the need for another lock, but the
		// additional lock seems more simple than a separate go routine limiting to the channel and so on.
		idleThreshold := time.Millisecond * 500
		idleMap := make(map[int]time.Time)
		// there will be one guy only doing read operations on this, but since there is only one reader and all other
		// things accessing it will write doing a RWLock wouldn't do much more than add complexity.
		idleMapLock := sync.Mutex{}

		startWorker := func(workerNo int) {
			baseLogger.Info().Int("worker", workerNo).Msg("starting worker")

			go func() {
				localLogger := baseLogger.With().Int("worker", workerNo).Logger()
				ctx := localLogger.WithContext(context.Background())

				for u := range urlsToProcess {
					idleMapLock.Lock()
					delete(idleMap, workerNo)
					idleMapLock.Unlock()

					localLogger.Info().Str("url", u).Msg("processing url")
					// theoretically we could try to tie into stopSignal here and call the cancel function if its sent
					// but it would add a chunk of complexity since it would need to fan out and we already
					// set a timeout so -meh-
					readCtx, _ := context.WithTimeout(ctx, readTimeout)
					docLinks, err := extractLinks(readCtx, u)
					localLogger.Debug().Str("url", u).Msg("extracted links from url")
					if err != nil {
						// if we decorated the error with a stack it needs to go through fmt with a +v to spit the stack
						// out, so no using .Err(err) here
						localLogger.Warn().Str("error", fmt.Sprintf("%+v", err)).Str("url", u).Msg("error reading url")
					} else {
						for _, l := range docLinks {
							localLogger.Debug().Str("url", l).Msg("checking url for inclusion")
							if shouldIncludeURL(l) {
								urlsLock.Lock()
								_, seen := urlsSeen[l]
								if !seen {
									// if workers aren't keeping up with the number of links we have seen things will deadlock
									// so just do that in a separate goroutine. l can also be changed so it needs to be
									// an argument to the func were invoking rather than just doing urlsToProcess <- l
									go func(toAdd string) {
										urlsToProcess <- toAdd
									}(l)
									urlsSeen[l] = true
								}
								urlsLock.Unlock()
							}
						}
					}
					localLogger.Debug().Msg("marking idle")
					idleMapLock.Lock()
					idleMap[workerNo] = time.Now()
					idleMapLock.Unlock()
				}
			}()
		}

		// fire up our workers - not just doing an arg-less go func() inline since i is gonna change immediately
		for i := 0; i < workerCount; i++ {
			idleMap[i] = time.Now()
			startWorker(i)
		}

		urlsToProcess <- url

		workersIdle := make(chan bool)
		stopWorkerWatcherSignal := make(chan bool)
		// this guy is going to monitor the idle map and see if all workers have been idle for > our idle threshold
		// then if so send the signal to bail
		go func() {
			ticker := time.NewTicker(time.Millisecond * 10)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					allDone := true
					for i := 0; i < workerCount; i++ {
						idleMapLock.Lock()
						idleTime, isIdle := idleMap[i]
						idleMapLock.Unlock()
						if !isIdle {
							// worker is active
							allDone = false
						} else if time.Now().Sub(idleTime) < idleThreshold {
							// worker is idle, but not long enough
							allDone = false
						}
					}
					if allDone {
						workersIdle <- true
						return
					}
				case <-stopWorkerWatcherSignal:
					return
				}
			}
		}()

		select {
		case <-workersIdle:
			// happy path, all workers are idle
			close(urlsToProcess)
		case <-stopSignal:
			close(urlsToProcess)
			stopWorkerWatcherSignal <- true
			return nil, errors.New("execution terminated")
		}

		ret := make([]string, 0)
		for u := range urlsSeen {
			ret = append(ret, u)
		}
		return ret, nil
	}, stopSignal
}
