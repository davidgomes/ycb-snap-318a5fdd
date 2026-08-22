package runtime

import (
	"fmt"
	"strings"
)

// Error kinds returned by errtype().
const (
	ErrorIndex      = "index"
	ErrorConversion = "conversion"
	ErrorType       = "type"
	ErrorNil        = "nil"
	ErrorRetry      = "retry"
	ErrorCustom     = "custom"
	ErrorNone       = "none"
)

// Error is a recoverable runtime error raised by throw() or classified from a panic.
type Error struct {
	Kind    string
	Message string
}

func (e Error) Error() string {
	return e.Message
}

func (e Error) String() string {
	return e.Message
}

func NewError(kind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

func NewCustomError(message string) *Error {
	return NewError(ErrorCustom, message)
}

func RetryLimitError() *Error {
	return NewError(ErrorRetry, "retry limit exceeded")
}

func ToErrorMessage(v any) string {
	if v == nil {
		return "nil"
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return fmt.Sprintf("%v", v)
}

// Wrap converts a recovered panic value into a classified Error.
func Wrap(r any) *Error {
	if r == nil {
		return nil
	}
	if e, ok := r.(*Error); ok {
		return e
	}
	msg := ToErrorMessage(r)
	return NewError(classifyMessage(msg), msg)
}

// Classify returns the errtype() label for a caught error or other value.
func Classify(v any) string {
	if v == nil {
		return ErrorNone
	}
	switch e := v.(type) {
	case *Error:
		if e == nil {
			return ErrorNone
		}
		return e.Kind
	case Error:
		return e.Kind
	case error:
		if e == nil {
			return ErrorNone
		}
		if re, ok := e.(*Error); ok {
			if re == nil {
				return ErrorNone
			}
			return re.Kind
		}
		return classifyMessage(e.Error())
	case string:
		return classifyMessage(e)
	default:
		return classifyMessage(fmt.Sprintf("%v", v))
	}
}

func classifyMessage(msg string) string {
	s := strings.ToLower(msg)
	switch {
	case strings.Contains(s, "retry limit exceeded"):
		return ErrorRetry
	case strings.Contains(s, "index out of range"),
		strings.Contains(s, "out of bounds"),
		strings.Contains(s, "slice index out of range"),
		strings.Contains(s, "index out of bounds"):
		return ErrorIndex
	case strings.Contains(s, "invalid operation: int("),
		strings.Contains(s, "invalid operation: int64("),
		strings.Contains(s, "invalid operation: float("),
		strings.Contains(s, "invalid operation: bool("),
		strings.Contains(s, "cannot convert"),
		strings.Contains(s, "strconv."):
		return ErrorConversion
	case strings.Contains(s, "nil pointer"),
		strings.Contains(s, "invalid memory address"),
		strings.Contains(s, "nil map"),
		strings.Contains(s, "from <nil>"),
		strings.Contains(s, "from nil"):
		return ErrorNil
	case strings.Contains(s, "type assertion"),
		strings.Contains(s, "cannot use"),
		strings.Contains(s, "cannot call"),
		strings.Contains(s, "cannot fetch"),
		strings.Contains(s, "cannot get"),
		strings.Contains(s, "cannot slice"),
		strings.Contains(s, "invalid argument"),
		strings.Contains(s, "invalid operation"),
		strings.Contains(s, "not defined on"),
		strings.Contains(s, "interface conversion"):
		return ErrorType
	default:
		return ErrorCustom
	}
}
