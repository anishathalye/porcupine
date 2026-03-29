package porcupine

import (
	"sort"
	"time"
)

type OperationIdx int32

type client struct {
	id   ClientId
	ops  []Operation  // operations by this client
	head OperationIdx // head is not pushed to the stack yet
}

type chains struct {
	clients  []client
	frontier []ClientId // The head of these clients are in the frontier
}

// A node in the DAG
// type Node struct {
// 	Id       int
// 	ClientId int
// 	OpKind   OperationKind
// 	Input    interface{}
// 	Output   interface{}
// 	Hint     interface{}
// 	Call     int64
// 	Ret      int64
// }

// type dag struct {
// 	ops    []*Node          // map from id to Node. Every operation (across clients) has a unique ID
// 	depts  [][]int          // outgoing edges
// 	lDepts [][]int          // likely outgoing edges
// 	nDeps  []int            // number of incoming edges
// 	nLDeps []int            // number of incoming likely edges
// 	front  map[int]struct{} // frontier used while enumerating topological orders
// }

type stackEntry struct { // entry in the stack
	operation Operation   // operation has ClientID in it. This operation was pushed on the stack
	state     interface{} // state before this node was pushed on the stack
	// front     []int       // frontier after this node was pushed on the stack, i.e., it does not contain the node
}

func (c *client) lift(model Model, oldState interface{}) (stackEntry, interface{}, bool) {
	op := c.ops[c.head]
	ok, newState := model.Step(oldState, op.Input, op.Output)
	if !ok {
		return stackEntry{}, newState, ok
	}

	e := stackEntry{
		operation: op,
		state:     oldState,
	}
	c.head++
	return e, newState, ok
}

func (c *client) unlift(top stackEntry) interface{} {
	if top.operation.ClientId != c.id {
		panic("Tried to unlift someone else's operation")
	}
	c.head--
	return top.state

}

func (ch *chains) lift(model Model, oldState interface{}) (stackEntry, interface{}, bool) {
	// delete(d.front, id)
	// for _, dept := range d.depts[id] {
	// 	d.nDeps[dept]--
	// 	if d.nDeps[dept] == 0 {
	// 		d.front[dept] = struct{}{}
	// 	}
	// }
	// for _, dept := range d.lDepts[id] {
	// 	d.nLDeps[dept]--
	// }
}

func (ch *chains) unlift(top stackEntry) interface{} {
	// for _, dept := range d.depts[id] {
	// 	if d.nDeps[dept] == 0 {
	// 		delete(d.front, dept)
	// 	}
	// 	d.nDeps[dept]++
	// }
	// d.front[id] = struct{}{}
	// for _, dept := range d.lDepts[id] {
	// 	d.nLDeps[dept]++
	// }
}

func (d *dag) getOrderedFront() []int {
	s := make([]int, 0, len(d.front))
	for k := range d.front {
		s = append(s, k)
	}
	sort.Slice(s, func(i, j int) bool {
		id1, id2 := s[i], s[j]
		return d.nLDeps[id1] > d.nLDeps[id2]
	})

	return s
}

func checkOperations(model Model, consistency Consistency, history [][]Operation, verbose bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	return Ok, LinearizationInfo{}
}
