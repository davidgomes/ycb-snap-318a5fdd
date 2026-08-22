package runtime

import (
	"fmt"
	"reflect"
	"strings"
)

// CustomError is the error raised by throw(value).
type CustomError struct {
	Message string
}

func (e *CustomError) Error() string {
	return e.Message
}

// RetryExhaustedError reports that retry has exceeded its limit.
type RetryExhaustedError struct{}

func (RetryExhaustedError) Error() string {
	return "retry limit exceeded"
}

// TryBlock contains the bytecode targets for a try expression.
type TryBlock struct {
	BodyStart      int
	CatchStart     int
	FinallyStart   int
	End            int
	CatchSubstring string
	HasFinally     bool
}

// ErrorType classifies an expression error for the errtype builtin.
func ErrorType(value any) string {
	if IsNil(value) {
		return "none"
	}

	err, ok := value.(error)
	if !ok {
		return "custom"
	}
	if _, ok := err.(RetryExhaustedError); ok {
		return "retry"
	}
	if _, ok := err.(*RetryExhaustedError); ok {
		return "retry"
	}
	if _, ok := err.(*CustomError); ok {
		return "custom"
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "index out of range"),
		strings.Contains(message, "slice bounds"),
		strings.Contains(message, "out of bounds"):
		return "index"
	case strings.Contains(message, "invalid operation: int("),
		strings.Contains(message, "invalid operation: int64("),
		strings.Contains(message, "invalid operation: float("),
		strings.Contains(message, "invalid operation: bool("),
		strings.Contains(message, "cannot convert"),
		strings.Contains(message, "conversion"):
		return "conversion"
	case strings.Contains(message, "nil pointer"),
		strings.Contains(message, "type nil"),
		strings.Contains(message, "nil reference"),
		strings.Contains(message, "cannot call nil"),
		strings.Contains(message, "cannot get") && strings.Contains(message, "nil"),
		strings.Contains(message, "cannot fetch") && strings.Contains(message, "nil"),
		strings.Contains(message, "invalid memory address"),
		strings.Contains(message, "<nil>"):
		return "nil"
	case strings.Contains(message, "mismatched"),
		strings.Contains(message, "interface conversion"),
		strings.Contains(message, "not callable"),
		strings.Contains(message, "not comparable"),
		strings.Contains(message, "must be"),
		strings.Contains(message, "invalid argument"),
		strings.Contains(message, "cannot use"):
		return "type"
	default:
		return "custom"
	}
}

// AsError converts any recovered panic into an error value for catch bindings.
func AsError(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("%v", value)
}

// IsTypedNil reports whether value is a nil reference, including typed nils.
func IsTypedNil(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Ptr, reflect.Interface, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
