package action

import (
	"errors"
	"fmt"
)

// HTTPError is returned by the file API and notification HTTP clients.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("http status %d", e.Status)
	}
	return fmt.Sprintf("http status %d: %s", e.Status, e.Body)
}

func (e *HTTPError) Retryable() bool { return e.Status == 429 || e.Status >= 500 }

// RetryableError wraps transient network/timeout errors.
type RetryableError struct{ Err error }

func (e *RetryableError) Error() string   { return e.Err.Error() }
func (e *RetryableError) Unwrap() error   { return e.Err }
func (e *RetryableError) Retryable() bool { return true }

func retryable(err error) error {
	if err == nil {
		return nil
	}
	return &RetryableError{Err: err}
}

func IsRetryable(err error) bool {
	var r interface{ Retryable() bool }
	return errors.As(err, &r) && r.Retryable()
}

func IsConflict(err error) bool {
	var e *HTTPError
	return errors.As(err, &e) && e.Status == 409
}

func IsNotFound(err error) bool {
	var e *HTTPError
	return errors.As(err, &e) && e.Status == 404
}
