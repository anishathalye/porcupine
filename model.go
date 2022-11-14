package porcupine

import "fmt"

// State describes the state of the system at a given point in time.
type State[S any] interface {
	// Stringer implements String() which returns a state representation for visualization purposes.
	// For example, "{'x' -> 'y', 'z' -> 'w'}".
	fmt.Stringer

	// Clone makes a deep copy of the given state, this is used to ensure every step
	// operates on its own state to avoid mutation issues.
	Clone() S
	// Equals returns true when this state equals the other state.
	Equals(otherState S) bool
}

// An Operation is an element of a history.
//
// This package supports two different representations of histories, as a
// sequence of Operation or [Event]. In the Operation representation, function
// call/returns are packaged together, along with timestamps of when the
// function call was made and when the function call returned.
type Operation struct {
	ClientId int // optional, unless you want a visualization; zero-indexed
	Input    interface{}
	Call     int64 // invocation timestamp
	Output   interface{}
	Return   int64 // response timestamp
}

// An EventKind tags an [Event] as either a function call or a return.
type EventKind bool

const (
	CallEvent   EventKind = false
	ReturnEvent EventKind = true
)

// An Event is an element of a history, a function call event or a return
// event.
//
// This package supports two different representations of histories, as a
// sequence of Event or [Operation]. In the Event representation, function
// calls and returns are only relatively ordered and do not have absolute
// timestamps.
//
// The Id field is used to match a function call event with its corresponding
// return event.
type Event struct {
	ClientId int // optional, unless you want a visualization; zero-indexed
	Kind     EventKind
	Value    interface{}
	Id       int
}

// A Model is a sequential specification of a system.
//
// Only the Init and Step functions are necessary to specify if you
// just want to test histories for linearizability.
//
// Implementing the partition functions can greatly improve performance. If
// you're implementing the partition function, the model Init and Step
// functions can be per-partition. For example, if your specification is for a
// key-value store, and you partition by key, then the per-partition state
// representation can just be a single value rather than a map.
//
// Implementing DescribeOperation and DescribeState will produce nicer
// visualizations.
//
// It may be helpful to look at this package's [test code] for examples of how
// to write models, including models that include partition functions.
//
// [test code]: https://github.com/anishathalye/porcupine/blob/master/porcupine_test.go
type Model[S State[S]] struct {
	// Partition functions, such that a history is linearizable if and only
	// if each partition is linearizable. If left nil, this package will
	// skip partitioning.
	Partition      func(history []Operation) [][]Operation
	PartitionEvent func(history []Event) [][]Event
	// Init returns the initial state of the system
	Init func() S
	// Step function for the system. Returns whether the system
	// could take this step with the given inputs and outputs and also
	// returns the new state. This function can mutate the state.
	Step func(state S, input interface{}, output interface{}) (bool, S)
	// For visualization, describe an operation as a string. For example,
	// "Get('x') -> 'y'". Can be omitted if you're not producing
	// visualizations.
	DescribeOperation func(input interface{}, output interface{}) string
}

// noPartition is a fallback partition function that partitions the history
// into a single partition containing all of the operations.
func noPartition(history []Operation) [][]Operation {
	return [][]Operation{history}
}

// noPartitionEvent is a fallback partition function that partitions the
// history into a single partition containing all of the events.
func noPartitionEvent(history []Event) [][]Event {
	return [][]Event{history}
}

// defaultDescribeOperation is a fallback to convert an operation to a string.
// It renders inputs and outputs using the "%v" format specifier.
func defaultDescribeOperation(input interface{}, output interface{}) string {
	return fmt.Sprintf("%v -> %v", input, output)
}

// A CheckResult is the result of a linearizability check.
//
// Checking for linearizability is decidable, but it is an NP-hard problem, so
// the checker might take a long time. If a timeout is not given, functions in
// this package will always return Ok or Illegal, but if a timeout is supplied,
// then some functions may return Unknown. Depending on the use case, you can
// interpret an Unknown result as Ok (i.e., the tool didn't find a
// linearizability violation within the given timeout).
type CheckResult string

const (
	Unknown CheckResult = "Unknown" // timed out
	Ok      CheckResult = "Ok"
	Illegal CheckResult = "Illegal"
)
