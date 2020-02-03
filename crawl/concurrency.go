package crawl

import (
	"context"
	"github.com/rs/zerolog"
	"sync"
	"time"
)

type workRequest struct {
	url      string
	complete chan<- []Link
	error    chan<- error
}

type workerPool struct {
	workQueue chan workRequest
}

func (w *workerPool) startWorkerPool(ctx context.Context, readDocument DocumentReader, readTimeout time.Duration, workerCount int) func() {
	stopSignals := make([]chan bool, 0)
	w.workQueue = make(chan workRequest)

	startWorker := func(workerNo int) {
		stop := make(chan bool)
		stopSignals = append(stopSignals, stop)

		go func() {
			for {
				select {
				case w := <-w.workQueue:
					logger := zerolog.Ctx(ctx).With().Int("worker", workerNo).Logger()
					localCtx := logger.WithContext(ctx)
					localCtx, _ = context.WithTimeout(localCtx, readTimeout)

					res, err := readDocument(localCtx, w.url)
					if err != nil {
						w.error <- err
					} else {
						w.complete <- res
					}
				case <-stop:
					return
				}
			}
		}()
	}

	for i := 0; i <= workerCount; i++ {
		startWorker(i)
	}

	stop := func() {
		for _, stop := range stopSignals {
			stop <- true
		}
	}
	return stop
}

func (w *workerPool) queueRead(url string) (<-chan []Link, <-chan error) {
	complete := make(chan []Link)
	onErr := make(chan error)
	w.workQueue <- workRequest{url, complete, onErr}
	return complete, onErr
}

type siteMap struct {
	mutex   sync.Mutex
	siteMap map[string][]Link
}

func (s *siteMap) addURL(url string, links []Link) {
	s.mutex.Lock()
	if s.siteMap == nil {
		s.siteMap = make(map[string][]Link)
	}
	defer s.mutex.Unlock()
	s.siteMap[url] = links
}

type visitedURLTracker struct {
	mutex    sync.Mutex
	urlsSeen map[string]bool
}

func (v *visitedURLTracker) hasURLBeenSeenPreviously(url string) bool {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	_, seen := v.urlsSeen[url]
	if !seen {
		v.urlsSeen[url] = true
	}
	return seen
}

func newURLTracker(startingURL string) visitedURLTracker {
	return visitedURLTracker{
		urlsSeen: map[string]bool{
			startingURL: true,
		},
	}
}
