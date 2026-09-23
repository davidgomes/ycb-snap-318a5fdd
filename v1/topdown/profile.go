package topdown

// RuleProfileRecorder receives one EnterRule call for every rule definition
// entered during evaluation and one SucceedRule call if that entry produces
// a value.
type RuleProfileRecorder interface {
	EnterRule(path string)
	SucceedRule(path string)
}
