package api

import (
	"errors"
	"log/slog"
	"net/http"
)

// The stable error codes of the v1 error envelope. Clients branch on these,
// never on the message.
const (
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeValidation       = "validation"
	CodeConflict         = "conflict"
	CodeRateLimited      = "rate_limited"
	CodeInternal         = "internal"
	CodeUnavailable      = "unavailable"
)

// Error is an API error: an HTTP status, a stable code, a message safe to
// show a caller, and, for validation errors, the fields that were wrong. An
// operation returns one (Validation, NotFound, Conflict, Forbidden) for every
// failure the caller can act on; anything else becomes a 500 in AsError.
type Error struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Body returns e as the wire envelope.
func (e *Error) Body() ErrorBody {
	return ErrorBody{Error: ErrorDetail{Code: e.Code, Message: e.Message, Fields: e.Fields}}
}

// Validation is a 400 naming each invalid field and what is wrong with it.
func Validation(fields map[string]string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: CodeValidation, Message: "the request has invalid fields", Fields: fields}
}

// NotFound is a 404: "<what> not found".
func NotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: what + " not found"}
}

// Conflict is a 409 with msg.
func Conflict(msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: CodeConflict, Message: msg}
}

// Forbidden is the 403 for a key lacking scope.
func Forbidden(scope string) *Error {
	return &Error{Status: http.StatusForbidden, Code: CodeForbidden, Message: "this API key lacks the " + scope + " scope"}
}

// Unavailable is a 503: a service the operation depends on (ShortLinks, for
// one) is down. Set Operation.MayBeUnavailable on an operation that returns
// it, so the OpenAPI document lists the 503.
func Unavailable(msg string) *Error {
	return &Error{Status: http.StatusServiceUnavailable, Code: CodeUnavailable, Message: msg}
}

// internalError is the one 500 every unexpected failure gets.
func internalError() *Error {
	return &Error{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "internal error"}
}

// ErrorBody is the JSON body of every v1 error:
// {"error":{"code":"...","message":"...","fields":{...}}}. middleware's
// writeAPIError writes the same shape for the 401, 403, 429 and 500 it
// answers itself (envelope_test.go keeps the two in step).
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is ErrorBody's one member.
type ErrorDetail struct {
	Code    string            `json:"code" jsonschema:"stable error code: unauthorized, forbidden, not_found, method_not_allowed, validation, conflict, rate_limited, unavailable or internal"`
	Message string            `json:"message" jsonschema:"human-readable; do not parse"`
	Fields  map[string]string `json:"fields,omitempty" jsonschema:"for validation errors: each invalid field and what is wrong with it"`
}

// AsError returns the *Error in err's chain, if any. Anything else is logged,
// with op naming the operation, and becomes a 500 "internal error". This is
// the only place an underlying error's text goes, and it goes to the journal,
// never to a response.
func AsError(op string, err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	slog.Error("api: operation failed", "op", op, "err", err)
	return internalError()
}
