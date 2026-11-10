package identify

import (
	"errors"
	"net/http"
)

// transientError is a failure of an outside service that is expected to go
// away by itself: a network problem, rate limiting or a server error. It says
// nothing about the item, so it does not count as a failed attempt.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

func transient(err error) error { return &transientError{err} }

// transientStatus reports whether an HTTP status is worth retrying later.
func transientStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// badCaptureError means the item's photos cannot be used at all; retrying
// will not help, so the item goes straight to review.
type badCaptureError struct{ err error }

func (e *badCaptureError) Error() string { return e.err.Error() }
func (e *badCaptureError) Unwrap() error { return e.err }

func isTransient(err error) bool {
	var t *transientError
	return errors.As(err, &t)
}

func isBadCapture(err error) bool {
	var b *badCaptureError
	return errors.As(err, &b)
}
