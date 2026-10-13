// Package apierr is the API's one error envelope. Every error answer has the
// same shape, so a client can branch on a stable code instead of matching
// message text, and knows whether trying again can help:
//
//	{"error": "server is busy", "code": "server_busy", "retryable": true,
//	 "field": "port", "details": {...}}
//
// error is the human-readable message (kept under that name so existing
// clients reading .error keep working); field and details are optional.
package apierr

import (
	"encoding/json"
	"net/http"
)

// Body is the envelope.
type Body struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	Field     string `json:"field,omitempty"`
	Details   any    `json:"details,omitempty"`
}

// Stable codes. Endpoints may add more specific ones (server_busy,
// node_unreachable, token_expired, …); these are the defaults by status.
const (
	CodeInvalidRequest   = "invalid_request"
	CodeValidation       = "validation"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeConflict         = "conflict"
	CodeTooLarge         = "too_large"
	CodeUnsupportedMedia = "unsupported_media_type"
	CodeRateLimited      = "rate_limited"
	CodeInternal         = "internal"
	CodeBadGateway       = "bad_gateway"
	CodeUnavailable      = "unavailable"
	CodeTimeout          = "timeout"
	CodeStorageFull      = "insufficient_storage"
)

// CodeFor is the default code for a status.
func CodeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeInvalidRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodeTooLarge
	case http.StatusUnsupportedMediaType:
		return CodeUnsupportedMedia
	case http.StatusUnprocessableEntity:
		return CodeValidation
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusBadGateway:
		return CodeBadGateway
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	case http.StatusGatewayTimeout:
		return CodeTimeout
	case http.StatusInsufficientStorage:
		return CodeStorageFull
	}
	if status >= 500 {
		return CodeInternal
	}
	return CodeInvalidRequest
}

// RetryableStatus reports whether the same request may succeed later without
// any change: rate limits and transient upstream or server failures.
func RetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout, http.StatusInternalServerError:
		return true
	}
	return false
}

// Write answers with the envelope. An empty code takes the status default.
func Write(w http.ResponseWriter, status int, code, msg string) {
	WriteBody(w, status, Body{Error: msg, Code: code})
}

// WriteBody answers with a fully specified envelope, filling a missing code
// and retryable flag from the status.
func WriteBody(w http.ResponseWriter, status int, b Body) {
	if b.Code == "" {
		b.Code = CodeFor(status)
	}
	if !b.Retryable {
		b.Retryable = RetryableStatus(status)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(b)
}

// NotFound and MethodNotAllowed replace the router's plain-text defaults.
func NotFound(w http.ResponseWriter, _ *http.Request) {
	Write(w, http.StatusNotFound, "", "no such endpoint")
}

func MethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	Write(w, http.StatusMethodNotAllowed, "", "method not allowed")
}
