package http

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

var retryableStatus = map[int]struct{}{
	http.StatusRequestTimeout:      {},
	http.StatusTooManyRequests:     {},
	http.StatusInternalServerError: {},
	http.StatusBadGateway:          {},
	http.StatusServiceUnavailable:  {},
	http.StatusGatewayTimeout:      {},
}

type httpRetryError struct {
	err           error
	status        int
	retryAfter    time.Duration
	hasRetryAfter bool
	transport     bool
}

func (e *httpRetryError) Error() string {
	if e.err == nil {
		return "upload failed"
	}
	return e.err.Error()
}

func (e *httpRetryError) Unwrap() error {
	return e.err
}

func (e *httpRetryError) retryable() bool {
	if e.transport {
		return true
	}
	_, ok := retryableStatus[e.status]
	return ok
}

func retryAfterFrom(e error) time.Duration {
	var he *httpRetryError
	if errors.As(e, &he) && he.hasRetryAfter && (he.status == http.StatusTooManyRequests || he.status == http.StatusServiceUnavailable) {
		return he.retryAfter
	}
	return 0
}

func parseRetryAfter(h http.Header) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return 0, false
	}
	d := time.Until(t)
	if d < 0 {
		d = 0
	}
	return d, true
}

func wrapHTTPError(resp *http.Response, err error, transport bool) error {
	if err == nil {
		return nil
	}
	he := &httpRetryError{err: err, transport: transport}
	if resp != nil {
		he.status = resp.StatusCode
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			if d, ok := parseRetryAfter(resp.Header); ok {
				he.retryAfter = d
				he.hasRetryAfter = true
			}
		}
	}
	return he
}
