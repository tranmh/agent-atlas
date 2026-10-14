package wire

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// chunked returns the input in small pieces to exercise frame reassembly.
type chunked struct {
	s string
	n int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	k := min(c.n, len(c.s), len(p))
	copy(p, c.s[:k])
	c.s = c.s[k:]
	return k, nil
}

func (c *chunked) Close() error { return nil }

func TestSSETeeReportsFramesAndEndOnce(t *testing.T) {
	in := "id: 1\ndata: {\"a\":1}\n\n: comment\n\nevent: x\ndata: line1\ndata: line2\n\ndata: tail"
	var frames []string
	ends := 0
	tee := NewSSETee(&chunked{s: in, n: 3}, func(b []byte) { frames = append(frames, string(b)) }, func(n int, err error) {
		ends++
		require.Equal(t, 3, n)
		require.NoError(t, err)
	})
	out, err := io.ReadAll(tee)
	require.NoError(t, err)
	require.Equal(t, in, string(out), "the reader sees the stream unchanged")
	require.NoError(t, tee.Close())
	require.Equal(t, []string{`{"a":1}`, "line1\nline2", "tail"}, frames)
	require.Equal(t, 1, ends)
	_ = strings.NewReader
}
