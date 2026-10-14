package eventlog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tranmh/agent-atlas/internal/events"
)

func TestAppendAssignsIDsPersistsAndPublishes(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	s, err := Open(dir, func() time.Time { return clock })
	require.NoError(t, err)
	require.Equal(t, "s-20261014-100000", s.Session())
	ch, cancel := s.Broadcaster().Subscribe()
	defer cancel()

	out, err := s.Append([]events.Event{{Kind: events.KindRunStarted, Source: "orchestrator"}, {Kind: events.KindAgentWork}})
	require.NoError(t, err)
	require.Equal(t, int64(1), out[0].ID)
	require.Equal(t, int64(2), out[1].ID)
	require.Equal(t, "unknown", out[1].Source)
	require.Equal(t, int64(1), (<-ch).ID)

	require.Len(t, s.After(0), 2)
	require.Len(t, s.After(1), 1)
	require.Empty(t, s.After(2))

	// A second store in the same second gets a distinct session.
	s2, err := Open(dir, func() time.Time { return clock })
	require.NoError(t, err)
	require.NotEqual(t, s.Session(), s2.Session())
	require.NoError(t, s.Close())
	require.NoError(t, s2.Close())

	sessions, err := s2.Sessions()
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	got, err := s2.ReadSession(s.Session())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, events.KindRunStarted, got[0].Kind)
	_, err = s2.ReadSession("../etc/passwd")
	require.Error(t, err)
}
