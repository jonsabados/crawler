package main

import (
	"flag"
	"fmt"
	"github.com/jonsabados/crawler/crawl"
	"github.com/rs/zerolog"
	"net/url"
	"os"
	"time"
)

func main() {
	startingURL := flag.String("url", "", "required - starting point for crawl and must be an http or https url. Only links on the same domain will be searched")
	workerCount := flag.Int("workers", 10, "how many workers to execute with")
	readTimeout := flag.Int("readTimeout", 500, "http read timeout in milliseconds (per URL seen)")
	executionTimeout := flag.Int("executionTimeout", 120, "total execution timout, in seconds")

	flag.Parse()

	if *startingURL == "" {
		flag.Usage()
		os.Exit(1)
	}

	u, err := url.Parse(*startingURL)
	if err != nil {
		flag.Usage()
		os.Exit(2)
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		flag.Usage()
		os.Exit(3)
	}

	logger := zerolog.New(os.Stdout).Level(zerolog.InfoLevel)
	crawler, stop := crawl.NewCrawler(logger, *workerCount, time.Duration(*readTimeout) * time.Millisecond, crawl.ReadDocument, crawl.SameDomainEligibilityChecker(*startingURL))

	go func() {
		time.Sleep(time.Duration(*executionTimeout) * time.Second)
		stop()
	}()

	res, err := crawler(*startingURL)
	if err != nil {
		panic(err)
	}
	for u, links := range res {
		fmt.Fprintf(os.Stdout, "Links for %s\n", u)
		for _, l := range links {
			fmt.Fprintf(os.Stdout, "\t%s: %s\n", l.LinkType, l.LinkTarget)
		}
	}
	os.Exit(0)
}
