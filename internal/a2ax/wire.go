// Package a2ax makes A2A traffic visible. Transport is an http.RoundTripper
// for the orchestrator's A2A client: it adds the run headers, and reports every
// card fetch, JSON-RPC request, response and SSE frame exactly as it went over
// the wire, plus task state changes derived from that wire data.
package a2ax

import (
	"encoding/json"
	"sync"

	"github.com/tranmh/agent-atlas/internal/events"
)

// TaskInfo is what we can learn about a task from one JSON-RPC result,
// streaming frame or push notification body.
type TaskInfo struct {
	Kind      string // task | statusUpdate | artifactUpdate | message
	TaskID    string
	ContextID string
	State     string // short form, e.g. "working"; empty if the payload has none
	Text      string // status message text, if any
	Artifact  string // artifact name, for artifactUpdate
}

// ExtractTaskInfo reads an A2A v1.0 result object (the "result" of a JSON-RPC
// response, a streaming frame's result, or a push notification body).
func ExtractTaskInfo(result json.RawMessage) TaskInfo {
	var r struct {
		Task           *taskJSON   `json:"task"`
		StatusUpdate   *statusJSON `json:"statusUpdate"`
		ArtifactUpdate *struct {
			TaskID    string `json:"taskId"`
			ContextID string `json:"contextId"`
			Artifact  struct {
				Name string `json:"name"`
			} `json:"artifact"`
		} `json:"artifactUpdate"`
		Message *struct {
			TaskID    string `json:"taskId"`
			ContextID string `json:"contextId"`
		} `json:"message"`
		// GetTask returns the task itself as the result.
		taskJSON
	}
	if json.Unmarshal(result, &r) != nil {
		return TaskInfo{}
	}
	switch {
	case r.Task != nil:
		return TaskInfo{Kind: "task", TaskID: r.Task.ID, ContextID: r.Task.ContextID, State: events.ShortState(r.Task.Status.State), Text: r.Task.Status.text()}
	case r.StatusUpdate != nil:
		return TaskInfo{Kind: "statusUpdate", TaskID: r.StatusUpdate.TaskID, ContextID: r.StatusUpdate.ContextID, State: events.ShortState(r.StatusUpdate.Status.State), Text: r.StatusUpdate.Status.text()}
	case r.ArtifactUpdate != nil:
		return TaskInfo{Kind: "artifactUpdate", TaskID: r.ArtifactUpdate.TaskID, ContextID: r.ArtifactUpdate.ContextID, Artifact: r.ArtifactUpdate.Artifact.Name}
	case r.Message != nil:
		return TaskInfo{Kind: "message", TaskID: r.Message.TaskID, ContextID: r.Message.ContextID}
	case r.ID != "" && r.Status.State != "":
		return TaskInfo{Kind: "task", TaskID: r.ID, ContextID: r.ContextID, State: events.ShortState(r.Status.State), Text: r.Status.text()}
	}
	return TaskInfo{}
}

type taskJSON struct {
	ID        string     `json:"id"`
	ContextID string     `json:"contextId"`
	Status    statusBody `json:"status"`
}

type statusJSON struct {
	TaskID    string     `json:"taskId"`
	ContextID string     `json:"contextId"`
	Status    statusBody `json:"status"`
}

type statusBody struct {
	State   string `json:"state"`
	Message *struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"message"`
}

func (s statusBody) text() string {
	if s.Message == nil {
		return ""
	}
	for _, p := range s.Message.Parts {
		if p.Text != "" {
			return p.Text
		}
	}
	return ""
}

// rpcEnvelope is a JSON-RPC request or response.
type rpcEnvelope struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Tracker remembers the last known state of each task so state changes can
// be reported once, whichever path (response, stream, push) revealed them.
type Tracker struct {
	mu     sync.Mutex
	states map[string]string
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker { return &Tracker{states: map[string]string{}} }

// Update records state for task and returns the previous state and whether it
// changed. States never move backwards: responses, stream frames and push
// notifications can arrive out of order, and a late "submitted" or anything
// after a terminal state is stale.
func (t *Tracker) Update(task, state string) (string, bool) {
	if task == "" || state == "" {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	prev := t.states[task]
	if prev == state || terminal(prev) || (state == "submitted" && prev != "") {
		return prev, false
	}
	t.states[task] = state
	return prev, true
}

func terminal(state string) bool {
	switch state {
	case "completed", "failed", "canceled", "rejected":
		return true
	}
	return false
}
