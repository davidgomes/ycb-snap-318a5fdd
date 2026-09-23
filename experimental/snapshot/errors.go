package snapshot

import (
	"errors"
	"fmt"
)

// Codes returned by ErrorCode. Unlike error messages, these are stable.
const (
	CodeNoModules           = "no_modules"
	CodeModuleClosed        = "module_closed"
	CodeNilSnapshot         = "nil_snapshot"
	CodeModuleCountMismatch = "module_count_mismatch"
	CodeIncompatibleModule  = "incompatible_module"
	CodeInsufficientMemory  = "insufficient_memory"
	CodeInvalidEncoding     = "invalid_encoding"
)

type codedError struct {
	code string
	msg  string
}

func (e *codedError) Error() string {
	return e.msg
}

func errorf(code, format string, args ...any) error {
	return &codedError{code: code, msg: fmt.Sprintf(format, args...)}
}

// ErrorCode returns the code of the first error in err's chain that was
// returned by this package, such as CodeInsufficientMemory, or "" if there is
// none.
func ErrorCode(err error) string {
	var e *codedError
	if errors.As(err, &e) {
		return e.code
	}
	return ""
}
