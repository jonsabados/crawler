package crawl

import (
	"sync"
	"time"
)

type idleWorkerTracker struct {
	sync.Mutex
	idleMap map[int]time.Time
}

func (p *idleWorkerTracker) MarkBusy(workerNo int) {
	p.Lock()
	defer p.Unlock()
	delete(p.idleMap, workerNo)
}

func (p *idleWorkerTracker) MarkIdle(workerNo int) {
	p.Lock()
	defer p.Unlock()
	p.idleMap[workerNo] = time.Now()
}

func (p *idleWorkerTracker) IdleStats(workerNo int) (time.Time, bool) {
	p.Lock()
	defer p.Unlock()
	time, isIdle := p.idleMap[workerNo]
	return time, isIdle
}

// Await returns a channel that can be listened to to for worker completion, and another channel that can be used to stop workers
func (p *idleWorkerTracker) Await(workerCount int, idleThreshold time.Duration) (<-chan bool,  chan<- bool) {
	complete := make(chan bool)
	cancel := make(chan bool)
	go func() {
		ticker := time.NewTicker(time.Millisecond * 10)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				allDone := true
				for i := 0; i < workerCount; i++ {
					idleTime, isIdle := p.IdleStats(i)
					if !isIdle {
						// worker is active
						allDone = false
					} else if time.Now().Sub(idleTime) < idleThreshold {
						// worker is idle, but not long enough
						allDone = false
					}
				}
				if allDone {
					complete <- true
					return
				}
			case <-cancel:
				return
			}
		}
	}()
	return complete, cancel
}

func newIdleWorkerTracker() idleWorkerTracker {
	return idleWorkerTracker{
		idleMap: make(map[int]time.Time),
	}
}

type urlTracker struct {
	sync.Mutex
	urlsSeen   map[string]bool
	urlsToWork chan string
}

func (v *urlTracker) appendURL(url string) {
	v.Lock()
	_, seen := v.urlsSeen[url]
	if !seen {
		// if workers aren't keeping up with the number of links we have seen things will deadlock
		// so just do that in a separate goroutine. l can also be changed so it needs to be
		// an argument to the func were invoking rather than just doing urlsToProcess <- l
		go func() {
			v.urlsToWork <- url
		}()
		v.urlsSeen[url] = true
	}
	v.Unlock()
}

func newURLTracker(startingURL string, urlsToWork chan string) urlTracker {
	return urlTracker{
		urlsSeen: map[string]bool{
			startingURL: true,
		},
		urlsToWork: urlsToWork,
	}
}
