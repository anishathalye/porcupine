package porcupine

import "testing"

func TestRegisterModel(t *testing.T) {
	// inputs
	type registerInput struct {
		op    bool // false = read, true = write
		value int
	}
	// output
	type registerOutput int // we don't care about return value for write
	registerModel := Model{
		Partition:      NoPartition,
		PartitionEvent: NoPartitionEvent,
		Init:           func() interface{} { return 0 },
		Step: func(state interface{}, input interface{}, output interface{}) (bool, interface{}) {
			st := state.(int)
			inp := input.(registerInput)
			out := output.(int)
			if inp.op == false {
				// read
				return out == st, state
			} else {
				// write
				return true, inp.value
			}
		},
	}

	// examples taken from http://nil.csail.mit.edu/6.824/2017/quizzes/q2-17-ans.pdf
	// section VII

	ops := []Operation{
		Operation{registerInput{true, 100}, 0, 0, 100},
		Operation{registerInput{false, 0}, 25, 100, 75},
		Operation{registerInput{false, 0}, 30, 0, 60},
	}
	res := CheckOperations(registerModel, ops)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	// same example as above, but with Event
	events := []Event{
		Event{CallEvent, registerInput{true, 100}, 0},
		Event{CallEvent, registerInput{false, 0}, 1},
		Event{CallEvent, registerInput{false, 0}, 2},
		Event{ReturnEvent, 0, 2},
		Event{ReturnEvent, 100, 1},
		Event{ReturnEvent, 0, 0},
	}
	res = CheckEvents(registerModel, events)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	ops = []Operation{
		Operation{registerInput{true, 200}, 0, 0, 100},
		Operation{registerInput{false, 0}, 10, 200, 30},
		Operation{registerInput{false, 0}, 40, 0, 90},
	}
	res = CheckOperations(registerModel, ops)
	if res != false {
		t.Fatal("expected operations to not be linearizable")
	}

	// same example as above, but with Event
	events = []Event{
		Event{CallEvent, registerInput{true, 200}, 0},
		Event{CallEvent, registerInput{false, 0}, 1},
		Event{ReturnEvent, 200, 1},
		Event{CallEvent, registerInput{false, 0}, 2},
		Event{ReturnEvent, 0, 2},
		Event{ReturnEvent, 0, 0},
	}
	res = CheckEvents(registerModel, events)
	if res != false {
		t.Fatal("expected operations to not be linearizable")
	}
}
