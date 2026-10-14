package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()

	if RunHealthcheck(ok.URL) != 0 {
		t.Error("200 should be 0")
	}
	if RunHealthcheck(bad.URL) != 1 {
		t.Error("503 should be 1")
	}
	if RunHealthcheck("http://127.0.0.1:1/nope") != 1 {
		t.Error("connection refused should be 1")
	}
}
