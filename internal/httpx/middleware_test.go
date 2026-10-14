package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORS_Preflight(t *testing.T) {
	called := false
	h := CORS("http://localhost:3000")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodOptions, "/api/plants", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if called {
		t.Fatal("preflight must not reach the handler")
	}
	hd := rec.Header()
	if got := hd.Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("origin = %q", got)
	}
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"} {
		if !strings.Contains(hd.Get("Access-Control-Allow-Methods"), m) {
			t.Errorf("methods missing %s: %q", m, hd.Get("Access-Control-Allow-Methods"))
		}
	}
	for _, x := range []string{"Content-Type", "Authorization", "Last-Event-ID"} {
		if !strings.Contains(hd.Get("Access-Control-Allow-Headers"), x) {
			t.Errorf("headers missing %s: %q", x, hd.Get("Access-Control-Allow-Headers"))
		}
	}
	if hd.Get("Access-Control-Expose-Headers") != "" {
		t.Errorf("must not expose headers, got %q", hd.Get("Access-Control-Expose-Headers"))
	}
}

func TestCORS_DefaultOriginAndPassThrough(t *testing.T) {
	h := CORS("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestRequestLogger_LogsAndKeepsFlusher(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("wrapped writer lost http.Flusher (SSE would break)")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("hello"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log not JSON: %q", buf.String())
	}
	if line["msg"] != "request" || line["method"] != "POST" || line["path"] != "/x" {
		t.Fatalf("log = %v", line)
	}
	if line["status"].(float64) != 202 || line["bytes"].(float64) != 5 {
		t.Fatalf("status/bytes = %v/%v", line["status"], line["bytes"])
	}
}
