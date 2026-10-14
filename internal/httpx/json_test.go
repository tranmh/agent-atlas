package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteError_Shape(t *testing.T) {
	cases := []struct {
		name  string
		field *string
		want  string
	}{
		{"null field", nil, `{"error":"not_found","message":"no such plant","field":null}`},
		{"with field", Field("plantId"), `{"error":"not_found","message":"no such plant","field":"plantId"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, http.StatusNotFound, CodeNotFound, "no such plant", tc.field)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
				t.Fatalf("content-type = %q", ct)
			}
			var got, want map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal([]byte(tc.want), &want)
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			if string(gb) != string(wb) {
				t.Fatalf("body = %s, want %s", gb, wb)
			}
		})
	}
}

func TestErrorCodes(t *testing.T) {
	codes := map[string]string{
		CodeValidation:   "validation_error",
		CodeNotFound:     "not_found",
		CodeUnauthorized: "unauthorized",
		CodeForbidden:    "forbidden",
		CodeInternal:     "internal",
		CodeBadGateway:   "bad_gateway",
	}
	for got, want := range codes {
		if got != want {
			t.Errorf("code %q != %q", got, want)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]int{"n": 1})
	if rec.Code != http.StatusCreated || rec.Body.String() != "{\"n\":1}\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}
