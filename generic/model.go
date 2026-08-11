package generic

import (
	"fmt"
	"strings"

	"github.com/anishathalye/porcupine"
)

// An Operation is an element of a history.
//
// This package supports two different representations of histories, as a
// sequence of Operation or [Event]. In the Operation representation, function
// call/returns are packaged together, along with timestamps of when the
// function call was made and when the function call returned.
//
// The interval [Call, Return] is interpreted as a closed interval, so an
// operation with interval [10, 20] is concurrent with another operation with
// interval [20, 30].
type Operation[I, O any] struct {
	ClientId int // optional, unless you want a visualization; zero-indexed
	Input    I
	Call     int64 // invocation timestamp
	Output   O
	Return   int64 // response timestamp
}

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
type Event[I any, O any] struct {
	ClientId int // optional, unless you want a visualization; zero-indexed
	Kind     porcupine.EventKind
	Input    I
	Output   O
	Id       int
}

// A Model is a sequential specification of a system.
//
// Note: models in this package are expected to be purely functional. That is,
// the model Step function should not modify the given state (or input or
// output), but return a new state.
//
// Only the Init, Step, and Equal functions are necessary to specify if you
// just want to test histories for linearizability.
//
// Implementing the partition functions can greatly improve performance. If
// you're implementing the partition function, the model Init and Step
// functions can be per-partition. For example, if your specification is for a
// key-value store and you partition by key, then the per-partition state
// representation can just be a single value rather than a map.
//
// Implementing DescribeOperation and DescribeState will produce nicer
// visualizations.
//
// It may be helpful to look at this package's [test code] for examples of how
// to write models, including models that include partition functions.
//
// [test code]: https://github.com/anishathalye/porcupine/blob/master/porcupine_test.go
type Model[S any, I any, O any] struct {
	// Partition functions, such that a history is linearizable if and only
	// if each partition is linearizable. If left nil, this package will
	// skip partitioning.
	Partition      func(history []Operation[I, O]) [][]Operation[I, O]
	PartitionEvent func(history []Event[I, O]) [][]Event[I, O]
	// Initial state of the system.
	Init func() S
	// Step function for the system. Returns whether or not the system
	// could take this step with the given inputs and outputs and also
	// returns the new state. This function must be a pure function: it
	// cannot mutate the given state.
	Step func(state S, input I, output O) (bool, S)
	// Equality on states. If left nil, this package will use == as a
	// fallback ([ShallowEqual]).
	Equal func(state1, state2 S) bool
	// For visualization, describe an operation as a string. For example,
	// "Get('x') -> 'y'". Can be omitted if you're not producing
	// visualizations.
	DescribeOperation func(input I, output O) string
	// For visualization purposes, describe a state as a string. For
	// example, "{'x' -> 'y', 'z' -> 'w'}". Can be omitted if you're not
	// producing visualizations.
	DescribeState func(state S) string
}

func (m *Model[S, I, O]) ToModel() porcupine.Model {
	model := porcupine.Model{}
	if m.Partition != nil {
		model.Partition = func(history []porcupine.Operation) [][]porcupine.Operation {
			return applyTypedPartitionOperation(m.Partition, history)
		}
	}
	if m.PartitionEvent != nil {
		model.PartitionEvent = func(history []porcupine.Event) [][]porcupine.Event {
			return applyTypedPartitionEvent(m.PartitionEvent, history)
		}
	}
	if m.Init != nil {
		model.Init = func() any { return m.Init() }
	}
	if m.Step != nil {
		model.Step = func(state any, input any, output any) (bool, any) {
			return m.Step(state.(S), input.(I), output.(O))
		}
	}
	if m.Equal != nil {
		model.Equal = func(state1 any, state2 any) bool {
			return m.Equal(state1.(S), state2.(S))
		}
	}
	if m.DescribeOperation != nil {
		model.DescribeOperation = func(input any, output any) string {
			return m.DescribeOperation(input.(I), output.(O))
		}
	}
	if m.DescribeState != nil {
		model.DescribeState = func(state any) string {
			return m.DescribeState(state.(S))
		}
	}
	return model
}

func applyTypedPartitionEvent[I, O any](partitionFunc func(history []Event[I, O]) [][]Event[I, O], history []porcupine.Event) [][]porcupine.Event {
	if partitionFunc == nil {
		return nil
	}
	typedHistory := convertToTypedEvents[I, O](history)
	partitions := partitionFunc(typedHistory)
	res := make([][]porcupine.Event, len(partitions))
	for i, partition := range partitions {
		res[i] = convertToUntypedEvents(partition)
	}
	return res
}

func convertToUntypedEvents[I, O any](partition []Event[I, O]) []porcupine.Event {
	untypedHistory := make([]porcupine.Event, len(partition))
	for j, ev := range partition {
		var value any
		if ev.Kind == porcupine.CallEvent {
			value = ev.Input
		} else {
			value = ev.Output
		}
		untypedHistory[j] = porcupine.Event{
			ClientId: ev.ClientId,
			Kind:     ev.Kind,
			Value:    value,
			Id:       ev.Id,
		}
	}
	return untypedHistory
}

func convertToTypedEvents[I, O any](history []porcupine.Event) []Event[I, O] {
	typedHistory := make([]Event[I, O], len(history))
	for i, ev := range history {
		var input I
		var output O
		if ev.Kind == porcupine.CallEvent {
			input = ev.Value.(I)
		} else {
			output = ev.Value.(O)
		}

		typedHistory[i] = Event[I, O]{
			ClientId: ev.ClientId,
			Kind:     ev.Kind,
			Input:    input,
			Output:   output,
			Id:       ev.Id,
		}
	}
	return typedHistory
}

func applyTypedPartitionOperation[I, O any](partitionFunc func(history []Operation[I, O]) [][]Operation[I, O], history []porcupine.Operation) [][]porcupine.Operation {
	if partitionFunc == nil {
		return nil
	}
	typedHistory := convertToTypedOperations[I, O](history)
	partitions := partitionFunc(typedHistory)
	res := make([][]porcupine.Operation, len(partitions))
	for i, partition := range partitions {
		res[i] = convertToUntypedOperations(partition)
	}
	return res
}

func convertToUntypedOperations[I, O any](partition []Operation[I, O]) []porcupine.Operation {
	untypedHistory := make([]porcupine.Operation, len(partition))
	for j, op := range partition {
		untypedHistory[j] = porcupine.Operation{
			ClientId: op.ClientId,
			Input:    op.Input,
			Call:     op.Call,
			Output:   op.Output,
			Return:   op.Return,
		}
	}
	return untypedHistory
}

func convertToTypedOperations[I, O any](history []porcupine.Operation) []Operation[I, O] {
	typedHistory := make([]Operation[I, O], len(history))
	for i, op := range history {
		typedHistory[i] = Operation[I, O]{
			ClientId: op.ClientId,
			Input:    op.Input.(I),
			Call:     op.Call,
			Output:   op.Output.(O),
			Return:   op.Return,
		}
	}
	return typedHistory
}

// A NondeterministicModel is a nondeterministic sequential specification of a
// system.
//
// For basics on models, see the documentation for [Model].  In contrast to
// Model, NondeterministicModel has a step function that returns a set of
// states, indicating all possible next states. It can be converted to a Model
// using the [NondeterministicModel.ToModel] function.
//
// It may be helpful to look at this package's [test code] for examples of how
// to write and use nondeterministic models.
//
// [test code]: https://github.com/anishathalye/porcupine/blob/master/porcupine_test.go
type NondeterministicModel[S any, I any, O any] struct {
	// Partition functions, such that a history is linearizable if and only
	// if each partition is linearizable. If left nil, this package will
	// skip partitioning.
	Partition      func(history []Operation[I, O]) [][]Operation[I, O]
	PartitionEvent func(history []Event[I, O]) [][]Event[I, O]
	// Initial states of the system.
	Init func() []S
	// Step function for the system. Returns all possible next states for
	// the given state, input, and output. If the system cannot step with
	// the given state/input to produce the given output, this function
	// should return an empty slice.
	Step func(state S, input I, output O) []S
	// Equality on states. If left nil, this package will use == as a
	// fallback ([ShallowEqual]).
	Equal func(state1, state2 S) bool
	// For visualization, describe an operation as a string. For example,
	// "Get('x') -> 'y'". Can be omitted if you're not producing
	// visualizations.
	DescribeOperation func(input I, output O) string
	// For visualization purposes, describe a state as a string. For
	// example, "{'x' -> 'y', 'z' -> 'w'}". Can be omitted if you're not
	// producing visualizations.
	DescribeState func(state S) string
}

// ToUntypedModel converts a [NondeterministicModel] to a [Model] using a power set
// construction.
//
// This makes it suitable for use in linearizability checking operations like
// [CheckOperations]. This is a general construction that can be used for any
// nondeterministic model. It relies on the NondeterministicModel's Equal
// function to merge states. You may be able to achieve better performance by
// implementing a Model directly.
func (nm *NondeterministicModel[S, I, O]) ToUntypedModel() porcupine.Model {
	model := porcupine.NondeterministicModel{}
	if nm.Partition != nil {
		model.Partition = func(history []porcupine.Operation) [][]porcupine.Operation {
			return applyTypedPartitionOperation(nm.Partition, history)
		}
	}
	if nm.PartitionEvent != nil {
		model.PartitionEvent = func(history []porcupine.Event) [][]porcupine.Event {
			return applyTypedPartitionEvent(nm.PartitionEvent, history)
		}
	}
	if nm.Init != nil {
		model.Init = func() []any {
			return convertSliceToAny(nm.Init())
		}
	}
	if nm.Step != nil {
		model.Step = func(state any, input any, output any) []any {
			return convertSliceToAny(nm.Step(state.(S), input.(I), output.(O)))
		}
	}
	if nm.Equal != nil {
		model.Equal = func(state1 any, state2 any) bool {
			return nm.Equal(state1.(S), state2.(S))
		}
	}
	if nm.DescribeOperation != nil {
		model.DescribeOperation = func(input any, output any) string {
			return nm.DescribeOperation(input.(I), output.(O))
		}
	}
	if nm.DescribeState != nil {
		model.DescribeState = func(state any) string {
			return nm.DescribeState(state.(S))
		}
	}
	return model.ToModel()
}

func convertSliceToAny[S any](s []S) []any {
	res := make([]any, len(s))
	for i := range s {
		res[i] = s[i]
	}
	return res
}

// func (nm *NondeterministicModel[S, I, O]) ToModel() Model[[]S, I, O] {
// }

// ToModel converts a [NondeterministicModel] to a [Model] using a power set
// construction.
//
// This makes it suitable for use in linearizability checking operations like
// [CheckOperations]. This is a general construction that can be used for any
// nondeterministic model. It relies on the NondeterministicModel's Equal
// function to merge states. You may be able to achieve better performance by
// implementing a Model directly.
func (nm *NondeterministicModel[S, I, O]) ToModel() Model[[]S, I, O] {
	// like fillDefaults
	equal := nm.Equal
	if equal == nil {
		equal = shallowEqual[S]
	}
	describeOperation := nm.DescribeOperation
	if describeOperation == nil {
		describeOperation = defaultDescribeOperation[I, O]
	}
	describeState := nm.DescribeState
	if describeState == nil {
		describeState = defaultDescribeState[S]
	}
	return Model[[]S, I, O]{
		Partition:      nm.Partition,
		PartitionEvent: nm.PartitionEvent,
		// we need this wrapper to convert a []interface{} to an interface{}
		Init: func() []S {
			return merge(nm.Init(), nm.Equal)
		},
		Step: func(state []S, input I, output O) (bool, []S) {
			var allNextStates []S
			for _, state := range state {
				allNextStates = append(allNextStates, nm.Step(state, input, output)...)
			}
			uniqueNextStates := merge(allNextStates, equal)
			return len(uniqueNextStates) > 0, uniqueNextStates
		},
		// this operates on sets of states that have been merged, so we
		// don't need to check inclusion in both directions
		Equal: func(state1, state2 []S) bool {
			if len(state1) != len(state2) {
				return false
			}
			for _, s1 := range state1 {
				found := false
				for _, s2 := range state2 {
					if equal(s1, s2) {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
			return true
		},
		DescribeOperation: describeOperation,
		DescribeState: func(states []S) string {
			var descriptions []string
			for _, state := range states {
				descriptions = append(descriptions, describeState(state))
			}
			return fmt.Sprintf("{%s}", strings.Join(descriptions, ", "))
		},
	}
}

func merge[S any](states []S, eq func(state1, state2 S) bool) []S {
	var uniqueStates []S
	for _, state := range states {
		unique := true
		for _, us := range uniqueStates {
			if eq(state, us) {
				unique = false
				break
			}
		}
		if unique {
			uniqueStates = append(uniqueStates, state)
		}
	}
	return uniqueStates
}

// shallowEqual is a fallback equality function that compares two states using
// ==.
func shallowEqual[S any](state1, state2 S) bool {
	s1 := any(state1)
	s2 := any(state2)
	return s1 == s2
}

// defaultDescribeOperation is a fallback to convert an operation to a string.
// It renders inputs and outputs using the "%v" format specifier.
func defaultDescribeOperation[I, O any](input I, output O) string {
	return fmt.Sprintf("%v -> %v", input, output)
}

// defaultDescribeState is a fallback to convert a state to a string. It
// renders the state using the "%v" format specifier.
func defaultDescribeState[S any](state S) string {
	return fmt.Sprintf("%v", state)
}
