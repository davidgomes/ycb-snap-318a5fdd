package snapshot

import (
	"errors"
	"fmt"
)

// Error codes returned by ErrorCode.
const (
	CodeNoModules           = "no_modules"
	CodeModuleClosed        = "module_closed"
	CodeNilBaseline         = "nil_baseline"
	CodeNilSnapshot         = "nil_snapshot"
	CodeModuleCountMismatch = "module_count_mismatch"
	CodeIncompatibleModule  = "incompatible_module"
	CodeInsufficientMemory  = "insufficient_memory"
	CodeInvalidData         = "invalid_data"
)

// Error is returned by the functions in this package. Code is stable across
// releases, while the message is not.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return "snapshot: " + e.Message
}

func newError(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrorCode returns the code of an Error in err's chain, or an empty string
// if there is none.
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
