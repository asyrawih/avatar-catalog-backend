// Package apierr menyediakan satu bentuk error untuk seluruh API:
//
//	{"error": {"code": "...", "message": "...", "details": [...]}}
//
// Service mengembalikan *apierr.Error, lapisan HTTP tinggal menulisnya.
package apierr

import "net/http"

// Error adalah kegagalan yang sudah tahu status HTTP-nya sendiri.
type Error struct {
	Status  int
	Code    string
	Message string
	Details []any
	// RetryAfter, jika > 0, ditulis sebagai header Retry-After (detik).
	RetryAfter int
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New membuat error dengan status dan kode tertentu.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// WithDetails melampirkan rincian per-field atau per-asset.
func (e *Error) WithDetails(details ...any) *Error {
	e.Details = append(e.Details, details...)
	return e
}

// WithRetryAfter menandai berapa detik klien sebaiknya menunggu.
func (e *Error) WithRetryAfter(seconds int) *Error {
	e.RetryAfter = seconds
	return e
}

// Pembungkus untuk status yang sering dipakai.
func BadRequest(code, msg string) *Error   { return New(http.StatusBadRequest, code, msg) }
func Unauthorized(code, msg string) *Error { return New(http.StatusUnauthorized, code, msg) }
func Forbidden(code, msg string) *Error    { return New(http.StatusForbidden, code, msg) }
func NotFound(code, msg string) *Error     { return New(http.StatusNotFound, code, msg) }
func Conflict(code, msg string) *Error     { return New(http.StatusConflict, code, msg) }
func Gone(code, msg string) *Error         { return New(http.StatusGone, code, msg) }
func TooLarge(code, msg string) *Error     { return New(http.StatusRequestEntityTooLarge, code, msg) }
func Unprocessable(code, msg string) *Error {
	return New(http.StatusUnprocessableEntity, code, msg)
}
func RateLimited(code, msg string) *Error { return New(http.StatusTooManyRequests, code, msg) }
