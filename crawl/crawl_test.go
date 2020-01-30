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
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

func Test_parseLinks(t *testing.T) {
	testCases := []struct {
		desc           string
		input          string
		expectedResult []string
	}{
		{
			"valid html",
			"testresources/valid.html",
			[]string{
				"http://foo.bar.com/icky_whitespace",
				"http://foo.bar.com/nice_link",
				"http://foo.bar.com/CasingFun",
				"mailto:someemail@foo.bar.com",
				"http://foo.bar.com/img",
			},
		},
		{
			"invalid html",
			"testresources/invalid.html",
			[]string{
				"http://foo.bar.com/icky_whitespace",
				"http://foo.bar.com/nice_link",
				"http://foo.bar.com/CasingFun",
				"mailto:someemail@foo.bar.com",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			asserter := assert.New(t)
			in, err := os.Open(tc.input)
			if asserter.NoError(err) {
				defer in.Close()
				res := parseLinks(context.Background(), in)
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
	res, err := ReadDocument(ctx, ts.URL)
	asserter.Equal([]string{
		"http://foo.bar.com/icky_whitespace",
		"http://foo.bar.com/nice_link",
		"http://foo.bar.com/CasingFun",
		"mailto:someemail@foo.bar.com",
		"http://foo.bar.com/img",
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

	linkStructure := map[string][]string{
		"start": {
			"A",
			"B",
			"D",
		},
		"A": {
			"B",
			"C",
			"Z",
		},
		"B": {
			"C",
			"W",
		},
		"C": {
			"A",
		},
	}

	reader := func(ctx context.Context, url string) ([]string, error) {
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

	crawl, _ := NewCrawler(logger, 10, time.Second, reader, shouldIncludeMock)

	timedOut := make(chan bool)
	go func() {
		time.Sleep(time.Second * 1)
		timedOut <- true
	}()
	done := make(chan bool)

	go func() {
		res, err := crawl("start")
		if asserter.NoError(err) {
			sort.Strings(res)
			// note - W shouldn't appear due to not matching our inclusion predicate
			asserter.Equal([]string{
				"A",
				"B",
				"C",
				"D",
				"Z",
			}, res)
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

	reader := func(ctx context.Context, url string) ([]string, error) {
		time.Sleep(time.Second * 2)
		return []string{}, nil
	}

	shouldIncludeMock := func(s string) bool {
		return s != "W"
	}

	crawl, stop := NewCrawler(logger, 10, time.Second * 3, reader, shouldIncludeMock)

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
