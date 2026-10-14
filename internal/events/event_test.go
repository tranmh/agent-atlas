package events

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTruncate(t *testing.T) {
	p, tr := Truncate([]byte(`{"a":1}`), 100)
	require.False(t, tr)
	require.JSONEq(t, `{"a":1}`, string(p))

	p, tr = Truncate([]byte("plain text"), 100)
	require.False(t, tr)
	require.JSONEq(t, `{"text":"plain text"}`, string(p))

	p, tr = Truncate([]byte(`{"a":"0123456789"}`), 5)
	require.True(t, tr)
	var m map[string]any
	require.NoError(t, json.Unmarshal(p, &m))
	require.Equal(t, `{"a":`, m["head"])
	require.EqualValues(t, 18, m["bytes"])
}

func TestReporterBatchesAndFlushesOnClose(t *testing.T) {
	var mu sync.Mutex
	var got []Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/events", r.URL.Path)
		var batch []Event
		require.NoError(t, json.NewDecoder(r.Body).Decode(&batch))
		mu.Lock()
		got = append(got, batch...)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	r := NewReporter(srv.URL, nil)
	for i := 0; i < 120; i++ {
		r.Emit(Event{Kind: KindAgentWork, Source: SourceAgent, Note: "x"})
	}
	r.Close()
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 120)
	require.False(t, got[0].ProducedTS.IsZero())
}

func TestReporterWithoutURLDiscards(t *testing.T) {
	r := NewReporter("", nil)
	r.Emit(Event{Kind: KindAgentWork})
	r.Close()
	require.Zero(t, r.Dropped())
}
