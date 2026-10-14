// Package eventlog is the visualizer's event store: one JSONL file per
// session (one visualizer process lifetime), an in-memory copy for replay to
// new SSE subscribers, and a broadcaster for live delivery.
package eventlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tranmh/agent-atlas/internal/events"
	"github.com/tranmh/agent-atlas/internal/httpx"
)

// Store appends events to the current session.
type Store struct {
	dir     string
	session string

	mu     sync.Mutex
	file   *os.File
	w      *bufio.Writer
	events []events.Event
	nextID int64
	bc     *httpx.Broadcaster[events.Event]
	now    func() time.Time
}

// SessionInfo describes a recorded session file.
type SessionInfo struct {
	ID       string    `json:"id"`
	Bytes    int64     `json:"bytes"`
	Modified time.Time `json:"modified"`
	Current  bool      `json:"current"`
}

var sessionRe = regexp.MustCompile(`^s-[0-9]{8}-[0-9]{6}(-[0-9]+)?$`)

// Open starts a new session in dir. An empty dir keeps events in memory only.
func Open(dir string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	s := &Store{dir: dir, nextID: 1, bc: httpx.NewBroadcaster[events.Event](4096), now: now}
	s.session = "s-" + now().UTC().Format("20060102-150405")
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for i := 1; ; i++ {
		f, err := os.OpenFile(filepath.Join(dir, s.session+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			s.file, s.w = f, bufio.NewWriterSize(f, 64<<10)
			return s, nil
		}
		if !errors.Is(err, os.ErrExist) || i > 100 {
			return nil, err
		}
		s.session = fmt.Sprintf("s-%s-%d", now().UTC().Format("20060102-150405"), i)
	}
}

// Session is the current session id.
func (s *Store) Session() string { return s.session }

// Broadcaster delivers appended events live.
func (s *Store) Broadcaster() *httpx.Broadcaster[events.Event] { return s.bc }

// Append assigns ids and timestamps, persists and publishes the events.
func (s *Store) Append(batch []events.Event) ([]events.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]events.Event, 0, len(batch))
	var werr error
	for _, e := range batch {
		e.ID = s.nextID
		s.nextID++
		e.TS = s.now().UTC()
		e.Session = s.session
		if e.Source == "" {
			e.Source = "unknown"
		}
		if s.w != nil {
			b, err := json.Marshal(e)
			if err == nil {
				_, err = s.w.Write(append(b, '\n'))
			}
			if err != nil && werr == nil {
				werr = err
			}
		}
		s.events = append(s.events, e)
		out = append(out, e)
	}
	if s.w != nil {
		if err := s.w.Flush(); err != nil && werr == nil {
			werr = err
		}
	}
	for _, e := range out {
		s.bc.Publish(e)
	}
	return out, werr
}

// After returns the events with id > after (in order).
func (s *Store) After(after int64) []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	// ids are dense from 1, so the index is id-1.
	if after < 0 {
		after = 0
	}
	if after >= int64(len(s.events)) {
		return nil
	}
	return append([]events.Event(nil), s.events[after:]...)
}

// Len is the number of events in the current session.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// Sessions lists the recorded sessions, newest first.
func (s *Store) Sessions() ([]SessionInfo, error) {
	if s.dir == "" {
		return []SessionInfo{{ID: s.session, Current: true}}, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	for _, de := range entries {
		id := strings.TrimSuffix(de.Name(), ".jsonl")
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") || !sessionRe.MatchString(id) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, SessionInfo{ID: id, Bytes: info.Size(), Modified: info.ModTime().UTC(), Current: id == s.session})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// SessionPath returns the JSONL path of a recorded session.
func (s *Store) SessionPath(id string) (string, error) {
	if s.dir == "" || !sessionRe.MatchString(id) {
		return "", os.ErrNotExist
	}
	p := filepath.Join(s.dir, id+".jsonl")
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// ReadSession loads a recorded session.
func (s *Store) ReadSession(id string) ([]events.Event, error) {
	if id == s.session {
		return s.After(0), nil
	}
	p, err := s.SessionPath(id)
	if err != nil {
		return nil, err
	}
	return ReadFile(p)
}

// ReadFile parses a JSONL event file, skipping malformed lines.
func ReadFile(path string) ([]events.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []events.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var e events.Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Close flushes and closes the session file and the broadcaster.
func (s *Store) Close() error {
	s.bc.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	_ = s.w.Flush()
	return s.file.Close()
}
