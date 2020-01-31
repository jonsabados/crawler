package crawl

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func Test_parseLinks(t *testing.T) {
	testCases := []struct {
		desc           string
		input          string
		expectedResult []Link
	}{
		{
			"valid html",
			"testresources/valid.html",
			[]Link{
				{
					LinkTypeA,
					"http://foo.bar.com/icky_whitespace",
				},
				{
					LinkTypeA,
					"http://foo.bar.com/nice_link",
				},
				{
					LinkTypeA,
					"http://foo.bar.com/CasingFun",
				},
				{
					LinkTypeA,
					"http://foo.bar.com/noproto",
				},
				{
					LinkTypeA,
					"mailto:someemail@foo.bar.com",
				},
				{
					LinkTypeA,
					"http://foo.bar.com/blah.html",
				},
				{
					LinkTypeA,
					"http://foo.bar.com/img",
				},
				{
					LinkTypeImg,
					"http://foo.bar.com/somimage",
				},
			},
		},
		{
			"invalid html",
			"testresources/invalid.html",
			[]Link{
				Link{
					LinkTypeA,
					"http://foo.bar.com/icky_whitespace",
				},
				{
					LinkTypeA,
					"http://foo.bar.com/nice_link",
				},
				{
					LinkTypeA,
					"http://foo.bar.com/CasingFun",
				},
				{
					LinkTypeA,
					"mailto:someemail@foo.bar.com",
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			asserter := assert.New(t)
			origin, err := url.Parse("http://foo.bar.com/blah/wee.html")
			asserter.NoError(err)
			in, err := os.Open(tc.input)
			if asserter.NoError(err) {
				defer in.Close()
				res := parseLinks(context.Background(), origin, in)
				asserter.Equal(tc.expectedResult, res)
			}
		})
	}
}

func Test_ReadDocument_RespectsContextDeadlinesAndTimeouts(t *testing.T) {
	asserter := assert.New(t)

	timeout := time.Millisecond * 10
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(timeout + time.Millisecond)
		body, err := ioutil.ReadFile("testresources/valid.html")
		asserter.NoError(err)
		w.Write(body)
	}))
	defer ts.Close()

	ctx, _ := context.WithTimeout(context.Background(), timeout)
	_, err := ReadDocument(ctx, ts.URL)
	if asserter.Error(err) {
		asserter.True(strings.Contains(err.Error(), "context deadline exceeded"))
	}
}

func Test_ReadDocument_HappyPath(t *testing.T) {
	asserter := assert.New(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadFile("testresources/valid.html")
		asserter.NoError(err)
		w.Write(body)
	}))
	defer ts.Close()

	ctx := context.Background()
	res, err := ReadDocument(ctx, fmt.Sprintf("%s/foo/bar.html", ts.URL))
	asserter.Equal([]Link{
		{
			LinkTypeA,
			"http://foo.bar.com/icky_whitespace",
		},
		{
			LinkTypeA,
			"http://foo.bar.com/nice_link",
		},
		{
			LinkTypeA,
			"http://foo.bar.com/CasingFun",
		},
		{
			LinkTypeA,
			"http://foo.bar.com/noproto",
		},
		{
			LinkTypeA,
			"mailto:someemail@foo.bar.com",
		},
		{
			LinkTypeA,
			fmt.Sprintf("%s/blah.html", ts.URL),
		},
		{
			LinkTypeA,
			"http://foo.bar.com/img",
		},
		{
			LinkTypeImg,
			fmt.Sprintf("%s/somimage", ts.URL),
		},
	}, res)
	asserter.NoError(err)
}

func Test_ReadDocument_GarbageInput(t *testing.T) {
	asserter := assert.New(t)

	ctx := context.Background()
	_, err := ReadDocument(ctx, " http://urlfail.com/")
	asserter.Error(err)
}

func Test_SameDomainEligibilityChecker(t *testing.T) {
	testCases := []struct {
		desc           string
		baseURL        string
		input          string
		expectedResult bool
	}{
		{
			"base match",
			"https://foo.bar.com",
			"https://foo.bar.com/blah.html",
			true,
		},
		{
			"case insensitive match",
			"https://Foo.bar.com",
			"https://foo.Bar.com/blah.html",
			true,
		},
		{
			"no match",
			"https://notfoo.bar.com",
			"https://foo.bar.com/blah.html",
			false,
		},
		{
			"different protocol OK",
			"https://foo.bar.com",
			"http://foo.bar.com/blah.html",
			true,
		},
		{
			"mailto shot down",
			"https://foo.bar.com",
			"mailto:bob@foo.bar.com",
			false,
		},
		{
			"match on only hostname shot down",
			"https://foo.bar.com",
			"https://foo.bob.com",
			false,
		},
		{
			"garbage cleanly rejected",
			"https://foo.bar.com",
			"this isn't a URL but doesn't trigger an error on url.parse",
			false,
		},
		{
			"more garbage cleanly rejected",
			"https://foo.bar.com",
			" https://foo.bar.com/blah",
			false,
		},
		{
			"non http protocol rejected",
			"https://foo.bar.com",
			"madeup://foo.bar.com/blah",
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			asserter := assert.New(t)

			toTest := SameDomainEligibilityChecker(tc.baseURL)

			asserter.Equal(tc.expectedResult, toTest(tc.input))
		})
	}
}

func Test_NewCrawler_HappyPath(t *testing.T) {
	asserter := assert.New(t)

	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	// note - need more links than workers to make sure we don't end up blocking when publishing to the work queue
	linkStructure := map[string][]Link{
		"start": {
			{LinkTypeA, "A"},
			{LinkTypeA, "B"},
			{LinkTypeA, "D"},
			{LinkTypeA, "E"},
			{LinkTypeA, "F"},
			{LinkTypeA, "G"},
			{LinkTypeA, "H"},
			{LinkTypeA, "I"},
			{LinkTypeA, "J"},
			{LinkTypeA, "K"},
			{LinkTypeA, "L"},
			{LinkTypeImg, "IMG"},
			{LinkTypeA, "start"},
		},
		"A": {
			{LinkTypeA, "B"},
			{LinkTypeA, "C"},
			{LinkTypeA, "Z"},
			{LinkTypeA, "start"},
		},
		"B": {
			{LinkTypeA, "C"},
			{LinkTypeA, "W"},
		},
		"C": {
			{LinkTypeA, "A"},
		},
	}

	dupeRead := make(map[string]bool)
	dupeReadLock := sync.Mutex{}
	reader := func(ctx context.Context, url string) ([]Link, error) {
		dupeReadLock.Lock()
		_, alreadyDone := dupeRead[url]
		if alreadyDone {
			asserter.Fail(fmt.Sprintf("duplicate read on %s", url))
		}
		dupeRead[url] = true
		dupeReadLock.Unlock()
		if url == "B" {
			time.Sleep(time.Millisecond * 100)
		}
		ret, present := linkStructure[url]
		if !present {
			return nil, errors.New("this shouldn't wreck the world")
		}
		return ret, nil
	}

	shouldIncludeMock := func(s string) bool {
		return s != "W"
	}

	crawl, _ := NewCrawler(logger, 3, time.Second, reader, shouldIncludeMock)

	timedOut := make(chan bool)
	go func() {
		time.Sleep(time.Second * 1)
		timedOut <- true
	}()
	done := make(chan bool)

	go func() {
		res, err := crawl("start")
		if asserter.NoError(err) {
			asserter.Equal(linkStructure, res)
			done <- true
		}
	}()

	select {
	case <-timedOut:
		asserter.Fail(fmt.Sprintf("time out"))
	case <-done:
		return
	}
}

func Test_NewCrawler_Shutdown(t *testing.T) {
	asserter := assert.New(t)

	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	reader := func(ctx context.Context, url string) ([]Link, error) {
		time.Sleep(time.Second * 2)
		return []Link{}, nil
	}

	shouldIncludeMock := func(s string) bool {
		return s != "W"
	}

	crawl, stop := NewCrawler(logger, 10, time.Second*3, reader, shouldIncludeMock)

	timedOut := make(chan bool)
	go func() {
		time.Sleep(time.Second * 1)
		timedOut <- true
	}()
	done := make(chan bool)

	go func() {
		_, err := crawl("start")
		asserter.EqualError(err, "execution terminated")
		done <- true
	}()

	stop <- true

	select {
	case <-timedOut:
		asserter.Fail(fmt.Sprintf("time out"))
	case <-done:
		return
	}

}
