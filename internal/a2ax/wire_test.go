package a2ax

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractTaskInfo(t *testing.T) {
	info := ExtractTaskInfo([]byte(`{"statusUpdate":{"taskId":"t1","contextId":"c1","status":{"state":"TASK_STATE_WORKING","message":{"parts":[{"text":"busy"}]}}}}`))
	require.Equal(t, TaskInfo{Kind: "statusUpdate", TaskID: "t1", ContextID: "c1", State: "working", Text: "busy"}, info)
	info = ExtractTaskInfo([]byte(`{"task":{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_INPUT_REQUIRED"}}}`))
	require.Equal(t, "input-required", info.State)
	info = ExtractTaskInfo([]byte(`{"id":"t1","contextId":"c1","status":{"state":"TASK_STATE_COMPLETED"}}`))
	require.Equal(t, "task", info.Kind)
	require.Equal(t, "completed", info.State)
	info = ExtractTaskInfo([]byte(`{"artifactUpdate":{"taskId":"t1","artifact":{"name":"a"}}}`))
	require.Equal(t, "a", info.Artifact)
	require.Equal(t, TaskInfo{}, ExtractTaskInfo([]byte(`nope`)))
}

func TestTrackerNeverMovesBackwards(t *testing.T) {
	tr := NewTracker()
	_, ch := tr.Update("t", "submitted")
	require.True(t, ch)
	_, ch = tr.Update("t", "working")
	require.True(t, ch)
	_, ch = tr.Update("t", "submitted") // a late response
	require.False(t, ch)
	_, ch = tr.Update("t", "input-required")
	require.True(t, ch)
	_, ch = tr.Update("t", "working")
	require.True(t, ch)
	_, ch = tr.Update("t", "completed")
	require.True(t, ch)
	_, ch = tr.Update("t", "working")
	require.False(t, ch)
}
