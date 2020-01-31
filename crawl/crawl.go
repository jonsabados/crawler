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

type LinkType int

const (
	LinkTypeA   LinkType = iota
	LinkTypeImg LinkType = iota
)

func (l LinkType) String() string {
	if l == LinkTypeA {
		return "hyperlink"
	} else {
		return "resource"
	}
}

func (l LinkType) linkAttr() string {
	if l == LinkTypeA {
		return "href"
	} else {
		return "src"
	}
}

func parseLinkType(tag string) LinkType {
	if tag == "a" {
		return LinkTypeA
	} else if tag == "img" {
		return LinkTypeImg
	} else {
		panic(fmt.Sprintf("unknown link type %s", tag))
	}
}

type Link struct {
	LinkType   LinkType
	LinkTarget string
}

// parseLinks looks for html links in an io stream. It tries to continue on any errors as if nothing was wrong until
// it encounters the end of the io stream (assuming a logger is setup on the context passed in errors will be logged
// at a warn level though).
func parseLinks(ctx context.Context, source *url.URL, r io.Reader) []Link {
	t := html.NewTokenizer(r)
	ret := make([]Link, 0)
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

		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			ret = processElement(ctx, source, t, ret)
		}
	}
	return ret
}

func processElement(ctx context.Context, source *url.URL, t *html.Tokenizer, existingLinks []Link) []Link {
	tagNameBytes, hasMoreAttrs := t.TagName()
	tagName := string(tagNameBytes)

	if tagName == "a" || tagName == "img" {
		linkType := parseLinkType(tagName)
		for hasMoreAttrs {
			var attr []byte
			var val []byte
			attr, val, hasMoreAttrs = t.TagAttr()
			if string(attr) == linkType.linkAttr() {
				target, err := linkTarget(source, string(val))
				if err  != nil {
					zerolog.Ctx(ctx).Warn().Err(err).Msg("error parsing link target")
				} else {
					existingLinks = append(existingLinks, Link{
						LinkType:   linkType,
						LinkTarget: target,
					})
				}
			}
		}
	}
	return existingLinks
}

func linkTarget(source *url.URL, href string) (string, error) {
	if strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "http://") || strings.HasPrefix(href,"https://") {
		return href, nil
	}
	relative, err := url.Parse(href)
	if err != nil {
		return "", nil
	}
	return source.ResolveReference(relative).String(), nil
}

// DocumentReader reads all links from a URL. Create one with NewDocumentFetcher
type DocumentReader func(ctx context.Context, url string) (links []Link, err error)

// ReadDocument is the non-test implementation of DocumentReader
func ReadDocument(ctx context.Context, url string) (links []Link, err error) {
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
	ret := parseLinks(ctx, req.URL, res.Body)
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

type Crawler func(url string) (map[string][]Link, error)

// NewCrawler returns a crawler and channel that can be used to shut it down
func NewCrawler(baseLogger zerolog.Logger, workerCount int, readTimeout time.Duration, extractLinks DocumentReader, shouldIncludeURL URLEligibilityChecker) (Crawler, chan bool) {
	stopSignal := make(chan bool)
	return func(startingURL string) (map[string][]Link, error) {
		baseLogger.Debug().Str("startingURL", startingURL).Msg("starting crawl")

		ret := make(map[string][]Link)
		retLock := sync.Mutex{}

		urlsToProcess := make(chan string, workerCount)
		urlsSeen := newURLTracker(startingURL, urlsToProcess)

		// we cant just use a wait group since workers are also feeding work. There may be a better way wait for them
		// to all be done, but just waiting for them to be idle for .5 seconds seems like a good-enough solution for
		// the current use case. It could also be feed with a chanel removing the need for another lock, but the
		// additional lock seems more simple than a separate go routine limiting to the channel and so on.
		idleThreshold := time.Millisecond * 500
		idleMap := newIdleWorkerTracker()

		startWorker := func(workerNo int) {
			baseLogger.Info().Int("worker", workerNo).Msg("starting worker")

			go func() {
				localLogger := baseLogger.With().Int("worker", workerNo).Logger()
				ctx := localLogger.WithContext(context.Background())

				for u := range urlsToProcess {
					idleMap.MarkBusy(workerNo)

					localLogger.Info().Str("startingURL", u).Msg("processing url")
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
						retLock.Lock()
						ret[u] = docLinks
						retLock.Unlock()

						for _, link := range docLinks {
							if link.LinkType != LinkTypeA {
								continue
							}
							localLogger.Debug().Str("url", link.LinkTarget).Msg("checking url for inclusion")
							if shouldIncludeURL(link.LinkTarget) {
								urlsSeen.appendURL(link.LinkTarget)
							}
						}
					}
					localLogger.Debug().Msg("marking idle")
					idleMap.MarkIdle(workerNo)
				}
			}()
		}

		// fire up our workers
		for i := 0; i < workerCount; i++ {
			idleMap.MarkIdle(i)
			startWorker(i)
		}

		urlsToProcess <- startingURL
		complete, cancel := idleMap.Await(workerCount, idleThreshold)

		select {
		case <-complete:
			close(urlsToProcess)
		case <-stopSignal:
			close(urlsToProcess)
			cancel <- true
			return nil, errors.New("execution terminated")
		}

		return ret, nil
	}, stopSignal
}
