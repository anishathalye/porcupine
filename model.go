package porcupine

type Operation struct {
	Input  interface{}
	Call   int64 // invocation time
	Output interface{}
	Return int64 // response time
}

type Model struct {
	Partition func(history []Operation) [][]Operation
	Init      func() interface{}
	Step      func(state interface{}, input interface{}, output interface{}) (bool, interface{})
}
