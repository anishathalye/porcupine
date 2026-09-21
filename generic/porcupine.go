package generic

import (
	"time"

	"github.com/anishathalye/porcupine"
)

// CheckOperations checks whether a history is linearizable.
func CheckOperations[S, I, O any](model Model[S, I, O], history []Operation[I, O]) bool {
	return porcupine.CheckOperations(model.ToModel(), convertToUntypedOperations(history))
}

// CheckOperationsTimeout checks whether a history is linearizable, with a
// timeout.
//
// A timeout of 0 is interpreted as an unlimited timeout.
func CheckOperationsTimeout[S, I, O any](model Model[S, I, O], history []Operation[I, O], timeout time.Duration) porcupine.CheckResult {
	return porcupine.CheckOperationsTimeout(model.ToModel(), convertToUntypedOperations(history), timeout)
}

// CheckOperationsVerbose checks whether a history is linearizable while
// computing data that can be used to visualize the history and linearization.
//
// The returned LinearizationInfo can be used with [Visualize].
func CheckOperationsVerbose[S, I, O any](model Model[S, I, O], history []Operation[I, O], timeout time.Duration) (porcupine.CheckResult, porcupine.LinearizationInfo) {
	return porcupine.CheckOperationsVerbose(model.ToModel(), convertToUntypedOperations(history), timeout)
}

// CheckEvents checks whether a history is linearizable.
func CheckEvents[S, I, O any](model Model[S, I, O], history []Event[I, O]) bool {
	return porcupine.CheckEvents(model.ToModel(), convertToUntypedEvents(history))
}

// CheckEventsTimeout checks whether a history is linearizable, with a timeout.
//
// A timeout of 0 is interpreted as an unlimited timeout.
func CheckEventsTimeout[S, I, O any](model Model[S, I, O], history []Event[I, O], timeout time.Duration) porcupine.CheckResult {
	return porcupine.CheckEventsTimeout(model.ToModel(), convertToUntypedEvents(history), timeout)
}

// CheckEventsVerbose checks whether a history is linearizable while computing
// data that can be used to visualize the history and linearization.
//
// The returned LinearizationInfo can be used with [Visualize].
func CheckEventsVerbose[S, I, O any](model Model[S, I, O], history []Event[I, O], timeout time.Duration) (porcupine.CheckResult, porcupine.LinearizationInfo) {
	return porcupine.CheckEventsVerbose(model.ToModel(), convertToUntypedEvents(history), timeout)
}
