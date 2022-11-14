package porcupine

import "time"

// CheckOperations checks whether a history is linearizable.
func CheckOperations[S State[S], I any, O any](model Model[S, I, O], history []Operation[I, O]) bool {
	res, _ := checkOperations(model, history, false, 0)
	return res == Ok
}

// CheckOperationsTimeout checks whether a history is linearizable, with a
// timeout.
//
// A timeout of 0 is interpreted as an unlimited timeout.
func CheckOperationsTimeout[S State[S], I any, O any](model Model[S, I, O], history []Operation[I, O], timeout time.Duration) CheckResult {
	res, _ := checkOperations(model, history, false, timeout)
	return res
}

// CheckOperationsVerbose checks whether a history is linearizable while
// computing data that can be used to visualize the history and linearization.
//
// The returned linearizationInfo can be used with [Visualize].
func CheckOperationsVerbose[S State[S], I any, O any](model Model[S, I, O], history []Operation[I, O], timeout time.Duration) (CheckResult, linearizationInfo) {
	return checkOperations(model, history, true, timeout)
}

// CheckEvents checks whether a history is linearizable.
func CheckEvents[S State[S], I any, O any](model Model[S, I, O], history []Event[I, O]) bool {
	res, _ := checkEvents(model, history, false, 0)
	return res == Ok
}

// CheckEventsTimeout checks whether a history is linearizable, with a timeout.
//
// A timeout of 0 is interpreted as an unlimited timeout.
func CheckEventsTimeout[S State[S], I any, O any](model Model[S, I, O], history []Event[I, O], timeout time.Duration) CheckResult {
	res, _ := checkEvents(model, history, false, timeout)
	return res
}

// CheckEventsVerbose checks whether a history is linearizable while computing
// data that can be used to visualize the history and linearization.
//
// The returned linearizationInfo can be used with [Visualize].
func CheckEventsVerbose[S State[S], I any, O any](model Model[S, I, O], history []Event[I, O], timeout time.Duration) (CheckResult, linearizationInfo) {
	return checkEvents(model, history, true, timeout)
}
