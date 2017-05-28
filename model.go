package porcupine

type Operation struct {
	Input  interface{}
	Call   int64 // invocation time
	Output interface{}
	Return int64 // response time
}

type EventKind bool

const (
	CallEvent   EventKind = false
	ReturnEvent EventKind = true
)

type Event struct {
	Kind  EventKind
	Value interface{}
	Id    uint
}

type Model struct {
	Partition      func(history []Operation) [][]Operation
	PartitionEvent func(history []Event) [][]Event
	Init           func() interface{}
	Step           func(state interface{}, input interface{}, output interface{}) (bool, interface{})
	Equal func(state1, state2 interface{}) bool
}

func NoPartition(history []Operation) [][]Operation {
	return [][]Operation{history}
}

func NoPartitionEvent(history []Event) [][]Event {
	return [][]Event{history}
}

func ShallowEqual(state1, state2 interface{}) bool {
	return state1 == state2
}
