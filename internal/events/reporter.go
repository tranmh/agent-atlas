package events

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Emitter accepts events. The Reporter implements it; tests use a Recorder.
type Emitter interface {
	Emit(e Event)
}

// Reporter batches events and POSTs them to the visualizer's /api/events.
// Emit never blocks the protocol path: when the buffer is full the event is
// dropped and counted.
type Reporter struct {
	url    string
	client *http.Client
	log    *slog.Logger
	ch     chan Event    // never closed: Emit may run concurrently with or after Close
	stop   chan struct{} // closed by Close
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool

	dropped atomic.Uint64
	failed  atomic.Uint64

	BatchSize int
	Interval  time.Duration
}

// NewReporter starts a reporter posting to baseURL + "/api/events". An empty
// baseURL yields a reporter that discards everything.
func NewReporter(baseURL string, log *slog.Logger) *Reporter {
	if log == nil {
		log = slog.Default()
	}
	r := &Reporter{
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       log,
		ch:        make(chan Event, 4096),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		BatchSize: 50,
		Interval:  100 * time.Millisecond,
	}
	if baseURL != "" {
		r.url = baseURL + "/api/events"
	}
	go r.loop()
	return r
}

// Emit queues an event. ProducedTS is stamped here.
func (r *Reporter) Emit(e Event) {
	if r.url == "" || r.closed.Load() {
		return
	}
	if e.ProducedTS.IsZero() {
		e.ProducedTS = time.Now().UTC()
	}
	select {
	case r.ch <- e:
	default:
		if r.dropped.Add(1)%100 == 1 {
			r.log.Warn("event buffer full, dropping", "dropped", r.dropped.Load())
		}
	}
}

// Dropped is the number of events lost to a full buffer.
func (r *Reporter) Dropped() uint64 { return r.dropped.Load() }

// Close flushes queued events (up to 5 s) and stops the reporter. Events
// emitted afterwards are discarded.
func (r *Reporter) Close() {
	r.once.Do(func() {
		r.closed.Store(true)
		close(r.stop)
	})
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
	}
}

func (r *Reporter) loop() {
	defer close(r.done)
	batch := make([]Event, 0, r.BatchSize)
	tick := time.NewTicker(r.Interval)
	defer tick.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		r.post(batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-r.stop:
			// Drain what is queued, then send the rest and stop.
			for {
				select {
				case e := <-r.ch:
					batch = append(batch, e)
					if len(batch) >= r.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case e := <-r.ch:
			batch = append(batch, e)
			if len(batch) >= r.BatchSize {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

func (r *Reporter) post(batch []Event) {
	body, err := json.Marshal(batch)
	if err != nil {
		r.log.Error("encode events", "err", err)
		return
	}
	// Retry briefly: the visualizer may still be starting.
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := r.client.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			err = &statusError{resp.StatusCode}
		}
		if attempt == 2 {
			if r.failed.Add(1)%20 == 1 {
				r.log.Warn("post events failed", "err", err, "events", len(batch))
			}
			return
		}
		time.Sleep(time.Duration(200*(attempt+1)) * time.Millisecond)
	}
}

type statusError struct{ code int }

func (e *statusError) Error() string { return http.StatusText(e.code) }

// Recorder is an in-memory Emitter for tests.
type Recorder struct {
	mu     sync.Mutex
	events []Event
}

// Emit records the event.
func (r *Recorder) Emit(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// Events returns a copy of the recorded events.
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// Kinds returns the kinds of the recorded events in order.
func (r *Recorder) Kinds() []Kind {
	var out []Kind
	for _, e := range r.Events() {
		out = append(out, e.Kind)
	}
	return out
}
