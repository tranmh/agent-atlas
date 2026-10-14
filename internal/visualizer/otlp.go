package visualizer

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/tranmh/agent-atlas/internal/events"
)

// timeKey carries the log record timestamp (unix nanoseconds) in the attribute map.
const timeKey = "_time_unix_nano"

// DecodeOTLPLogs parses an OTLP/HTTP logs export (protobuf or JSON) and returns
// the attributes of every log record as a flat map.
func DecodeOTLPLogs(body []byte, contentType string) ([]map[string]any, error) {
	var req collogs.ExportLogsServiceRequest
	var err error
	if strings.Contains(contentType, "json") {
		err = protojson.Unmarshal(body, &req)
	} else {
		err = proto.Unmarshal(body, &req)
	}
	if err != nil {
		return nil, fmt.Errorf("otlp: %w", err)
	}
	var out []map[string]any
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				m := map[string]any{}
				for _, kv := range lr.GetAttributes() {
					m[kv.GetKey()] = anyValue(kv.GetValue())
				}
				if b := lr.GetBody(); b != nil {
					if _, ok := m["message"]; !ok {
						m["message"] = anyValue(b)
					}
				}
				m["scope"] = sl.GetScope().GetName()
				if ts := lr.GetTimeUnixNano(); ts > 0 {
					m[timeKey] = int64(ts)
				} else if ts := lr.GetObservedTimeUnixNano(); ts > 0 {
					m[timeKey] = int64(ts)
				}
				out = append(out, m)
			}
		}
	}
	return out, nil
}

func anyValue(v *common.AnyValue) any {
	switch x := v.GetValue().(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue
	case *common.AnyValue_IntValue:
		return x.IntValue
	case *common.AnyValue_DoubleValue:
		return x.DoubleValue
	case *common.AnyValue_BoolValue:
		return x.BoolValue
	case *common.AnyValue_ArrayValue:
		var arr []any
		for _, e := range x.ArrayValue.GetValues() {
			arr = append(arr, anyValue(e))
		}
		return arr
	case *common.AnyValue_KvlistValue:
		m := map[string]any{}
		for _, kv := range x.KvlistValue.GetValues() {
			m[kv.GetKey()] = anyValue(kv.GetValue())
		}
		return m
	case *common.AnyValue_BytesValue:
		return string(x.BytesValue)
	}
	return nil
}

// AccessEvent turns one gateway access-log record into a gateway_access event.
// It returns false for records that are not request logs.
func AccessEvent(attrs map[string]any, limit int) (events.Event, bool) {
	str := func(k string) string {
		switch v := attrs[k].(type) {
		case string:
			return v
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		return ""
	}
	if str("http.method") == "" && str("http.request.method") == "" {
		return events.Event{}, false
	}
	method := firstNonEmpty(str("http.method"), str("http.request.method"))
	path := firstNonEmpty(str("http.path"), str("url.path"))
	status, _ := strconv.Atoi(firstNonEmpty(str("http.status"), str("http.response.status_code")))
	route := str("route")
	if i := strings.LastIndex(route, "/"); i >= 0 {
		route = route[i+1:]
	}
	caller := str("caller")
	e := events.Event{
		Source: events.SourceGateway, Kind: events.KindGatewayAccess, Via: events.ViaGateway,
		RunID: str("run_id"), From: caller, To: route, Route: route, HTTPStatus: status,
		Method:     firstNonEmpty(str("a2a.method"), method+" "+path),
		ContextID:  str("a2a.context.id"),
		StateTo:    events.ShortState(str("a2a.task.state")),
		DurationMS: parseDurationMS(str("duration")),
	}
	if m := str("mcp.method.name"); m != "" || strings.HasSuffix(route, "-mcp") {
		// MCP route of a scenario: name the server (multiplex target) and the tool.
		e.Method = "MCP " + firstNonEmpty(m, method)
		if tool := str("mcp_tool"); tool != "" {
			e.Method += " " + tool
		} else if uri := str("mcp.resource.uri"); uri != "" {
			e.Method += " " + uri
		}
		e.To = "agentgateway"
		if t := str("mcp.target"); t != "" && len(route) > 3 {
			e.To = route[:3] + t // "sc-mcp" + target "erp" -> "sc-erp"
		}
		if status == 400 && str("reason") == "MCP" {
			e.Note = fmt.Sprintf("agentgateway: MCP policy refused %s for %s (400 Unknown tool)", str("mcp_tool"), caller)
		}
	} else if model := str("gen_ai.request.model"); model != "" || strings.HasPrefix(path, "/v1/") {
		e.To = "llm"
		e.Method = "POST " + path
		if model != "" {
			e.Method += " (" + model + ")"
		}
	}
	if len(caller) > 3 && caller[2] == '-' {
		e.Scenario = caller[:2]
	} else if len(route) > 3 && route[2] == '-' {
		e.Scenario = route[:2]
	}
	if ns, ok := attrs[timeKey].(int64); ok {
		e.ProducedTS = time.Unix(0, ns).UTC()
		delete(attrs, timeKey)
	}
	if e.Note != "" {
		e.WithValue(attrs, limit)
		return e, true
	}
	e.Note = fmt.Sprintf("agentgateway: %s %s → %s %d in %d ms", firstNonEmpty(caller, "unknown caller"), e.Method, firstNonEmpty(e.To, path), status, e.DurationMS)
	if n, _ := strconv.Atoi(str("retry.attempt")); n > 0 {
		e.Note += fmt.Sprintf(" (retry policy: succeeded on attempt %d)", n+1)
	}
	if o := str("a2a.response.outcome"); o == "error" {
		e.Note += fmt.Sprintf(" (JSON-RPC error %s)", str("a2a.response.error_code"))
	}
	if s := e.StateTo; s != "" {
		e.Note += ", task " + s
	}
	if status == 403 {
		e.Note += " (denied by authorization policy)"
	}
	e.WithValue(attrs, limit)
	return e, true
}

func parseDurationMS(s string) int64 {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasSuffix(s, "ms"):
		v, _ := strconv.ParseFloat(strings.TrimSuffix(s, "ms"), 64)
		return int64(v)
	case strings.HasSuffix(s, "s"):
		v, _ := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
		return int64(v * 1000)
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
