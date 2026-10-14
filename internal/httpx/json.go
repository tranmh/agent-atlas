package httpx

import (
	"encoding/json"
	"net/http"
)

// APIError is the single error shape shared by every service:
// {"error": "<code>", "message": "<human text>", "field": "<name>"|null}.
type APIError struct {
	Error   string  `json:"error"`
	Message string  `json:"message"`
	Field   *string `json:"field"`
}

// Machine-readable error codes.
const (
	CodeValidation   = "validation_error"
	CodeNotFound     = "not_found"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeInternal     = "internal"
	CodeBadGateway   = "bad_gateway"
)

// WriteJSON writes v as JSON with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the shared error shape.
func WriteError(w http.ResponseWriter, status int, code, message string, field *string) {
	WriteJSON(w, status, APIError{Error: code, Message: message, Field: field})
}

// Field returns a pointer to s, for the optional `field` of WriteError.
func Field(s string) *string { return &s }
