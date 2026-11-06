package server

import (
	"sync"
	"time"

	"github.com/JanVrana/gitreview/internal/git"
)

const (
	pollInterval      = 1500 * time.Millisecond
	keepaliveInterval = 20 * time.Second
)

// watcher polls the repository fingerprint once for all SSE clients and only
// while at least one client is connected. Transient git errors (e.g. a lock
// held by a concurrent commit) are skipped rather than reported as changes.
type watcher struct {
	repo    *git.Repo
	mu      sync.Mutex
	subs    map[chan string]struct{}
	stop    chan struct{}
	last    string
	running bool
}

func newWatcher(repo *git.Repo) *watcher {
	return &watcher{repo: repo, subs: map[chan string]struct{}{}}
}

// subscribe registers a client. The channel has a buffer of one so a slow
// client keeps at most one pending notification (which is all it needs).
func (w *watcher) subscribe() (<-chan string, func()) {
	ch := make(chan string, 1)
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	if !w.running {
		w.running = true
		w.stop = make(chan struct{})
		w.last, _ = w.repo.Fingerprint()
		go w.loop(w.stop)
	}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subs, ch)
		if len(w.subs) == 0 && w.running {
			close(w.stop)
			w.running = false
		}
		w.mu.Unlock()
	}
}

func (w *watcher) loop(stop chan struct{}) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		fp, err := w.repo.Fingerprint()
		if err != nil {
			continue
		}
		w.mu.Lock()
		if fp != w.last && w.last != "" {
			for ch := range w.subs {
				select {
				case ch <- fp:
				default: // a notification is already pending
				}
			}
		}
		w.last = fp
		w.mu.Unlock()
	}
}
