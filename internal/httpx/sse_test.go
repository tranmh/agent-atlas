package httpx

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type noFlush struct{ http.ResponseWriter }

func TestNewSSEWriter_RequiresFlusher(t *testing.T) {
	if _, err := NewSSEWriter(noFlush{httptest.NewRecorder()}); err != ErrNoFlusher {
		t.Fatalf("err = %v", err)
	}
}

func TestSSEWriter_HeadersAndFrames(t *testing.T) {
	rec := httptest.NewRecorder()
	s, err := NewSSEWriter(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Event("42", "plant", map[string]string{"plantId": "p1", "status": "red"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Event("", "", 7); err != nil {
		t.Fatal(err)
	}
	_ = s.Heartbeat()
	_ = s.Retry(3000)

	h := rec.Header()
	if h.Get("Content-Type") != "text/event-stream" || h.Get("Cache-Control") != "no-cache" || h.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers = %v", h)
	}
	if !rec.Flushed {
		t.Fatal("not flushed")
	}
	want := "id: 42\nevent: plant\ndata: {\"plantId\":\"p1\",\"status\":\"red\"}\n\n" +
		"data: 7\n\n" +
		": heartbeat\n\n" +
		"retry: 3000\n\n"
	if rec.Body.String() != want {
		t.Fatalf("body =\n%q\nwant\n%q", rec.Body.String(), want)
	}
}

func TestSSEWriter_SanitisesNewlinesInIDAndName(t *testing.T) {
	rec := httptest.NewRecorder()
	s, _ := NewSSEWriter(rec)
	_ = s.Event("1\n2", "a\r\nb", "x\ny")
	want := "id: 1 2\nevent: a  b\ndata: \"x\\ny\"\n\n"
	if rec.Body.String() != want {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestSSEWriter_UnencodableData(t *testing.T) {
	s, _ := NewSSEWriter(httptest.NewRecorder())
	if err := s.Event("", "x", make(chan int)); err == nil {
		t.Fatal("want encode error")
	}
}

// End-to-end over a real HTTP connection: the client must receive each event
// before the handler returns (proves flushing works through RequestLogger).
func TestSSEWriter_StreamsOverHTTP(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(RequestLogger(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := NewSSEWriter(w)
		if err != nil {
			t.Error(err)
			return
		}
		_ = s.Event("1", "snapshot", map[string]int{"n": 1})
		<-release
	})))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	var got []string
	for len(got) < 3 {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (got %v)", err, got)
		}
		got = append(got, strings.TrimRight(line, "\n"))
	}
	if strings.Join(got, "|") != `id: 1|event: snapshot|data: {"n":1}` {
		t.Fatalf("got %v", got)
	}
}

func TestBroadcaster_DeliversToAll(t *testing.T) {
	b := NewBroadcaster[int](4)
	c1, cancel1 := b.Subscribe()
	c2, cancel2 := b.Subscribe()
	defer cancel1()
	defer cancel2()
	if n := b.Publish(5); n != 2 {
		t.Fatalf("delivered = %d", n)
	}
	if <-c1 != 5 || <-c2 != 5 {
		t.Fatal("value mismatch")
	}
	if b.Subscribers() != 2 {
		t.Fatal("subscribers")
	}
}

func TestBroadcaster_DisconnectsSlowSubscriberWithoutBlocking(t *testing.T) {
	b := NewBroadcaster[int](2)
	var evictions atomic.Int32
	b.OnEvict(func() { evictions.Add(1) })
	slow, cancelSlow := b.Subscribe()
	defer cancelSlow()
	fast, cancelFast := b.Subscribe()
	defer cancelFast()

	done := make(chan struct{})
	go func() {
		for i := range 5 {
			b.Publish(i)
			<-fast // fast subscriber keeps up
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
	if b.Evicted() != 1 || evictions.Load() != 1 {
		t.Fatalf("evicted = %d (callback %d), want 1", b.Evicted(), evictions.Load())
	}
	if b.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want 1 (slow one removed)", b.Subscribers())
	}
	// The slow subscriber keeps what it had buffered, then sees the close —
	// it never gets a value after a gap.
	var got []int
	for v := range slow {
		got = append(got, v)
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("slow subscriber got %v, want [0 1] then close", got)
	}
	cancelSlow() // after eviction: must not panic (double close)
	if n := b.Publish(9); n != 1 {
		t.Fatalf("delivered after eviction = %d, want 1", n)
	}
	if <-fast != 9 {
		t.Fatal("fast subscriber lost a value")
	}
}

// A consumer that resubscribes after being disconnected receives new values.
func TestBroadcaster_ResubscribeAfterEviction(t *testing.T) {
	b := NewBroadcaster[int](1)
	ch, cancel := b.Subscribe()
	b.Publish(1)
	b.Publish(2) // full → evicted
	if v, ok := <-ch; !ok || v != 1 {
		t.Fatalf("buffered value: %d %v", v, ok)
	}
	if _, ok := <-ch; ok {
		t.Fatal("evicted channel should be closed")
	}
	cancel()
	ch2, cancel2 := b.Subscribe()
	defer cancel2()
	b.Publish(3)
	if v := <-ch2; v != 3 {
		t.Fatalf("resubscribed got %d", v)
	}
}

func TestBroadcaster_CancelClosesAndIsIdempotent(t *testing.T) {
	b := NewBroadcaster[string](1)
	ch, cancel := b.Subscribe()
	cancel()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed")
	}
	if b.Subscribers() != 0 {
		t.Fatal("not removed")
	}
	if b.Publish("x") != 0 {
		t.Fatal("publish after cancel delivered")
	}
}

func TestBroadcaster_Close(t *testing.T) {
	b := NewBroadcaster[int](0)
	ch, cancel := b.Subscribe()
	b.Close()
	if _, ok := <-ch; ok {
		t.Fatal("not closed")
	}
	cancel() // must not panic (double close)
	b.Close()
	late, _ := b.Subscribe()
	if _, ok := <-late; ok {
		t.Fatal("late subscriber should get a closed channel")
	}
	if b.Publish(1) != 0 {
		t.Fatal("publish after close")
	}
}

// Run with -race: concurrent publishers, subscribers joining/leaving and Close.
func TestBroadcaster_Race(t *testing.T) {
	b := NewBroadcaster[int](8)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					b.Publish(i)
				}
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				ch, cancel := b.Subscribe()
				for range 3 {
					select {
					case <-ch:
					case <-time.After(time.Millisecond):
					}
				}
				cancel()
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	b.Close()
	close(stop)
	wg.Wait()
	_ = b.Evicted()
}
