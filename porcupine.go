package porcupine

import "time"

func CheckOperations(model Model, history []Operation) bool {
	ok, _ := checkOperations(model, history, false, 0)
	return ok
}

// timeout = 0 means no timeout
// if this operation times out, then a false positive is possible
func CheckOperationsTimeout(model Model, history []Operation, timeout time.Duration) bool {
	ok, _ := checkOperations(model, history, false, timeout)
	return ok
}

// timeout = 0 means no timeout
// if this operation times out, then a false positive is possible
func CheckOperationsVerbose(model Model, history []Operation, timeout time.Duration) (bool, linearizationInfo) {
	return checkOperations(model, history, true, timeout)
}

func CheckEvents(model Model, history []Event) bool {
	ok, _ := checkEvents(model, history, false, 0)
	return ok
}

// timeout = 0 means no timeout
// if this operation times out, then a false positive is possible
func CheckEventsTimeout(model Model, history []Event, timeout time.Duration) bool {
	ok, _ := checkEvents(model, history, false, timeout)
	return ok
}

// timeout = 0 means no timeout
// if this operation times out, then a false positive is possible
func CheckEventsVerbose(model Model, history []Event, timeout time.Duration) (bool, linearizationInfo) {
	return checkEvents(model, history, true, timeout)
}
