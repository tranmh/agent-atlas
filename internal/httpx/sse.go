package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrNoFlusher is returned by NewSSEWriter when the ResponseWriter cannot flush.
var ErrNoFlusher = errors.New("sse: response writer does not support flushing")

// SSEWriter writes Server-Sent Events and flushes after every frame. It is safe
// for concurrent use (e.g. an event loop plus a heartbeat ticker).
type SSEWriter struct {
	mu sync.Mutex
	w  http.ResponseWriter
	f  http.Flusher
}

// NewSSEWriter sets the SSE response headers, writes the 200 status line and
// flushes so the client sees the stream open immediately.
func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, ErrNoFlusher
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &SSEWriter{w: w, f: f}, nil
}

// Event writes one event. id and name are omitted when empty; data is
// JSON-encoded onto a single `data:` line.
func (s *SSEWriter) Event(id, name string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("sse: encode %q: %w", name, err)
	}
	var b strings.Builder
	if id != "" {
		b.WriteString("id: ")
		b.WriteString(sanitizeLine(id))
		b.WriteByte('\n')
	}
	if name != "" {
		b.WriteString("event: ")
		b.WriteString(sanitizeLine(name))
		b.WriteByte('\n')
	}
	b.WriteString("data: ")
	b.Write(payload) // json.Marshal output never contains raw newlines
	b.WriteString("\n\n")
	return s.write(b.String())
}

// Comment writes an SSE comment line (ignored by clients); use it as a
// keep-alive so proxies do not close idle streams.
func (s *SSEWriter) Comment(text string) error {
	return s.write(": " + sanitizeLine(text) + "\n\n")
}

// Heartbeat writes a keep-alive comment.
func (s *SSEWriter) Heartbeat() error { return s.Comment("heartbeat") }

// Retry tells the client how many milliseconds to wait before reconnecting.
func (s *SSEWriter) Retry(ms int) error {
	return s.write(fmt.Sprintf("retry: %d\n\n", ms))
}

func (s *SSEWriter) write(frame string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write([]byte(frame)); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

func sanitizeLine(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}

// Broadcaster fans values out to any number of subscribers. Publish never
// blocks: a subscriber whose buffer is full is disconnected instead — removed
// and its channel closed after the values it already holds — so it never
// silently misses a value. Its consumer sees the channel close (like on
// Close) and must resync; an SSE handler simply ends the stream and the
// client reconnects (Last-Event-ID replay or a fresh snapshot). Evictions are
// counted (Evicted).
type Broadcaster[T any] struct {
	mu      sync.Mutex
	subs    map[uint64]chan T
	nextID  uint64
	buffer  int
	closed  bool
	evicted atomic.Uint64
	onEvict func()
}

// NewBroadcaster creates a broadcaster whose subscriber channels have the given
// buffer size (minimum 1). The buffer is how far a subscriber may fall behind
// before it is disconnected.
func NewBroadcaster[T any](buffer int) *Broadcaster[T] {
	if buffer < 1 {
		buffer = 1
	}
	return &Broadcaster[T]{subs: make(map[uint64]chan T), buffer: buffer}
}

// OnEvict registers a callback run (under the broadcaster's lock, so it must
// be fast and must not call back into the broadcaster) whenever a slow
// subscriber is disconnected. Call it before publishing starts.
func (b *Broadcaster[T]) OnEvict(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onEvict = fn
}

// Subscribe registers a subscriber. The returned cancel func unregisters it and
// closes the channel; it is idempotent and safe after an eviction. After
// Close, Subscribe returns an already-closed channel.
func (b *Broadcaster[T]) Subscribe() (<-chan T, func()) {
	ch := make(chan T, b.buffer)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if c, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(c)
			}
		})
	}
}

// Publish delivers v to every subscriber without blocking and returns how many
// subscribers received it. Subscribers with a full buffer are disconnected.
func (b *Broadcaster[T]) Publish(v T) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	delivered := 0
	for id, ch := range b.subs {
		select {
		case ch <- v:
			delivered++
		default:
			delete(b.subs, id)
			close(ch)
			b.evicted.Add(1)
			if b.onEvict != nil {
				b.onEvict()
			}
		}
	}
	return delivered
}

// Evicted is the total number of subscribers disconnected for falling behind.
func (b *Broadcaster[T]) Evicted() uint64 { return b.evicted.Load() }

// Subscribers returns the current subscriber count.
func (b *Broadcaster[T]) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Close unregisters and closes every subscriber channel. Further Publish calls
// are no-ops; further Subscribe calls get a closed channel.
func (b *Broadcaster[T]) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
}
