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
				if err != nil {
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
	if strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
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
	zerolog.Ctx(ctx).Info().Str("url", url).Msg("reading url")
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

func crawl(ctx context.Context, url string, workers *workerPool, shouldCrawlURL URLEligibilityChecker, urlsSeen *visitedURLTracker, siteMap *siteMap) error {
	stop := ctx.Done()
	s, e := workers.queueRead(url)
	select {
	case <-stop:
		return errors.New("context cancelled")
	case res := <-s:
		siteMap.addURL(url, res)
		linksToCrawl := make([]string, 0)
		for _, l := range res {
			if l.LinkType == LinkTypeA && shouldCrawlURL(l.LinkTarget) && !urlsSeen.hasURLBeenSeenPreviously(l.LinkTarget) {
				linksToCrawl = append(linksToCrawl, l.LinkTarget)
			}
		}
		if len(linksToCrawl)  > 0 {
			wg := sync.WaitGroup{}
			for _, l := range linksToCrawl {
				wg.Add(1)
				go func(url string) {
					_ = crawl(ctx, url, workers, shouldCrawlURL, urlsSeen, siteMap)
					wg.Done()
				}(l)
			}
			wg.Wait()
		}
	case err := <-e:
		zerolog.Ctx(ctx).Err(err).Str("url", url).Msg("error reading URL")
		return err
	}
	return nil
}

type Crawler func(url string) (map[string][]Link, error)

// NewCrawler returns a crawler and function that can be used to shut it down
func NewCrawler(baseLogger zerolog.Logger, workerCount int, readTimeout time.Duration, extractLinks DocumentReader, shouldCrawlURL URLEligibilityChecker) (Crawler, func()) {
	ctx := context.Background()
	ctx = baseLogger.WithContext(ctx)
	ctx, stop := context.WithCancel(ctx)

	return func(startingURL string) (map[string][]Link, error) {
		baseLogger.Debug().Str("startingURL", startingURL).Msg("starting crawl")

		siteMap := siteMap{}
		urlsSeen := newURLTracker(startingURL)

		workers := workerPool{}
		stopWorkers := workers.startWorkerPool(ctx, extractLinks, readTimeout, workerCount)
		defer stopWorkers()

		err := crawl(ctx, startingURL, &workers, shouldCrawlURL, &urlsSeen, &siteMap)

		return siteMap.siteMap, err
	}, stop
}
