// Package wire holds the HTTP capture helpers shared by the A2A and MCP
// clients: an SSE tee that reports every frame of a streaming response while
// the protocol client reads it, and body buffering for requests.
package wire

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
)

// SSETee wraps a text/event-stream body. OnFrame gets the data of every
// complete frame (multi-line data joined), in order; OnEnd is called once,
// with the frame count, when the body is exhausted, fails or is closed.
type SSETee struct {
	rc      io.ReadCloser
	onFrame func(data []byte)
	onEnd   func(frames int, err error)

	mu     sync.Mutex
	buf    bytes.Buffer
	frames int
	done   bool
}

// NewSSETee returns the wrapped body.
func NewSSETee(rc io.ReadCloser, onFrame func(data []byte), onEnd func(frames int, err error)) *SSETee {
	return &SSETee{rc: rc, onFrame: onFrame, onEnd: onEnd}
}

// Read implements io.Reader.
func (s *SSETee) Read(p []byte) (int, error) {
	n, err := s.rc.Read(p)
	if n > 0 {
		s.mu.Lock()
		s.buf.Write(p[:n])
		s.drain()
		s.mu.Unlock()
	}
	if err == io.EOF {
		s.finish(nil)
	} else if err != nil {
		s.finish(err)
	}
	return n, err
}

// Close implements io.Closer.
func (s *SSETee) Close() error {
	s.finish(nil)
	return s.rc.Close()
}

// drain hands complete frames (terminated by a blank line) to onFrame. Caller holds mu.
func (s *SSETee) drain() {
	for {
		data := s.buf.Bytes()
		idx := bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
			return
		}
		frame := string(data[:idx])
		s.buf.Next(idx + 2)
		var payload strings.Builder
		sc := bufio.NewScanner(strings.NewReader(frame))
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data:") {
				if payload.Len() > 0 {
					payload.WriteByte('\n')
				}
				payload.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if payload.Len() == 0 {
			continue
		}
		s.frames++
		if s.onFrame != nil {
			s.onFrame([]byte(payload.String()))
		}
	}
}

func (s *SSETee) finish(err error) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	if s.buf.Len() > 0 {
		s.buf.WriteString("\n\n")
		s.drain()
	}
	frames := s.frames
	s.mu.Unlock()
	if s.onEnd != nil {
		s.onEnd(frames, err)
	}
}

// IsSSE reports whether a response is an event stream.
func IsSSE(resp *http.Response) bool {
	return strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
}

// BufferRequest reads the request body and makes it re-readable (also for retries).
func BufferRequest(req *http.Request) []byte {
	if req.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return body
}

// BufferResponse reads a (non-streaming) response body, capped at limit bytes, and restores it.
func BufferResponse(resp *http.Response, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, err
}
