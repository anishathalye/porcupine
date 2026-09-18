package porcupine

import "time"

// CheckOperations checks whether a history is linearizable.
func CheckOperations(model Model, history []Operation) bool {
	res, _ := checkOperations(model, Linearizability, history, false, 0)
	return res == Ok
}

// CheckOperationsTimeout checks whether a history is linearizable, with a
// timeout.
//
// A timeout of 0 is interpreted as an unlimited timeout.
func CheckOperationsTimeout(model Model, history []Operation, timeout time.Duration) CheckResult {
	res, _ := checkOperations(model, Linearizability, history, false, timeout)
	return res
}

// CheckOperationsVerbose checks whether a history is linearizable while
// computing data that can be used to visualize the history and linearization.
//
// The returned LinearizationInfo can be used with [Visualize].
func CheckOperationsVerbose(model Model, history []Operation, timeout time.Duration) (CheckResult, LinearizationInfo) {
	return checkOperations(model, Linearizability, history, true, timeout)
}

// CheckEvents checks whether a history is linearizable.
func CheckEvents(model Model, history []Event) bool {
	res, _ := checkEvents(model, Linearizability, history, false, 0)
	return res == Ok
}

// CheckEventsTimeout checks whether a history is linearizable, with a timeout.
//
// A timeout of 0 is interpreted as an unlimited timeout.
func CheckEventsTimeout(model Model, history []Event, timeout time.Duration) CheckResult {
	res, _ := checkEvents(model, Linearizability, history, false, timeout)
	return res
}

// CheckEventsVerbose checks whether a history is linearizable while computing
// data that can be used to visualize the history and linearization.
//
// The returned LinearizationInfo can be used with [Visualize].
func CheckEventsVerbose(model Model, history []Event, timeout time.Duration) (CheckResult, LinearizationInfo) {
	return checkEvents(model, Linearizability, history, true, timeout)
}

// Check general consistency models

func CheckOperationsConsistency(model Model, consistency Consistency, history []Operation) bool {
	res, _ := checkOperations(model, consistency, history, false, 0)
	return res == Ok
}

func CheckOperationsConsistencyTimeout(model Model, consistency Consistency, history []Operation, timeout time.Duration) CheckResult {
	res, _ := checkOperations(model, consistency, history, false, timeout)
	return res
}

func CheckOperationsConsistencyVerbose(model Model, consistency Consistency, history []Operation, timeout time.Duration) (CheckResult, LinearizationInfo) {
	return checkOperations(model, consistency, history, true, timeout)
}

func CheckEventsConsistency(model Model, consistency Consistency, history []Event) bool {
	res, _ := checkEvents(model, consistency, history, false, 0)
	return res == Ok
}

func CheckEventsConsistencyTimeout(model Model, consistency Consistency, history []Event, timeout time.Duration) CheckResult {
	res, _ := checkEvents(model, consistency, history, false, timeout)
	return res
}

func CheckEventsConsistencyVerbose(model Model, consistency Consistency, history []Event, timeout time.Duration) (CheckResult, LinearizationInfo) {
	return checkEvents(model, consistency, history, true, timeout)
}
