package snapshot

import "errors"

// ErrorCode returns a stable machine-readable code for err.
// It returns the empty string when err is nil or has no code.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ""
}

type codedError struct {
	code string
	msg  string
}

func (e *codedError) Error() string {
	return e.msg
}

func newCodedError(code, msg string) error {
	return &codedError{code: code, msg: msg}
}
