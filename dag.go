package porcupine

import (
	"time"
)

type ClientId int
type OperationIdx int32

type client struct {
	id    ClientId
	ops   []Operation      // operations by this client
	head  OperationIdx     // head is not pushed to the stack yet
	start [][]OperationIdx // start[i][c] is the index of the first operation from client c that has no outgoing dependency to op i
}

type chains struct {
	clients []client
	// frontier []int // The head of these clients are in the frontier
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
	operation Operation    // operation has ClientID in it. This operation was pushed on the stack
	state     interface{}  // state before this node was pushed on the stack
	opIdx     OperationIdx // index of the operation in the client's history
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
		opIdx:     c.head,
	}
	c.head++
	return e, newState, ok
}

func (c *client) unlift(top stackEntry) interface{} {
	if c.id != ClientId(top.operation.ClientId) {
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
	return stackEntry{}, nil, true
}

// TODO: sort by likely order, update start
func (ch *chains) getFront() []int {
	front := []int{}
	for _, c := range ch.clients {
		if c.head >= OperationIdx(len(c.ops)) {
			continue
		}
		blocked := false
		for _, other := range ch.clients {
			// c is blocked until other.head >= c.start[c.head][other.id]
			if c.id != other.id && other.head < c.start[c.head][other.id] {
				blocked = true
				break
			}
		}
		if !blocked {
			front = append(front, int(c.id))
		}
	}
	return front
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
	return nil
}

// func (d *dag) getOrderedFront() []int {
// 	s := make([]int, 0, len(d.front))
// 	for k := range d.front {
// 		s = append(s, k)
// 	}
// 	sort.Slice(s, func(i, j int) bool {
// 		id1, id2 := s[i], s[j]
// 		return d.nLDeps[id1] > d.nLDeps[id2]
// 	})

// 	return s
// }

func checkOperations(model Model, consistency Consistency, history [][]Operation, verbose bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	ch := &chains{}
	n := 0
	for _, c := range history {
		n += len(c)
	}

	// create global id for operations
	globalIdMap := make([][]int, len(history))
	idCounter := 0
	for i, c := range history {
		globalIdMap[i] = make([]int, len(c))
		for j := range c {
			globalIdMap[i][j] = idCounter
			idCounter++
		}

		start := make([][]OperationIdx, len(c))
		for k := range start {
			start[k] = make([]OperationIdx, len(history))
			// Pre-calculate real-time dependencies
			for otherId, otherOps := range history {
				if i == otherId {
					continue
				}
				for m, otherOp := range otherOps {
					if otherOp.Return < c[k].Call {
						start[k][otherId] = OperationIdx(m + 1)
					} else {
						break
					}
				}
			}
		}
		ch.clients = append(ch.clients, client{
			id:    ClientId(i),
			ops:   c,
			head:  0,
			start: start,
		})
	}

	linearized := newBitset(uint(n))
	cache := make(map[uint64][]cacheEntry) // map from hash to cache entry
	front := ch.getFront()
	stack := []stackEntry{}
	state := model.Init()

	for len(front) != 0 && len(stack) != 0 {
		serialized := false
		// try serializing an op from front
		for _, cIdx := range front {
			c := ch.clients[cIdx]
			op := c.ops[c.head]
			stackTop := stack[len(stack)-1]
			comp, err := consistency.Check(&stackTop.operation, &op)
			if err != nil {
				panic("Error in consistency check: " + err.Error())
			}
			// stackTop not after op: possible to serialize
			if comp != HardAfter {
				ok, newState := model.Step(state, op.Input, op.Output)
				if ok {
					newLinearized := linearized.clone().set(uint(cIdx ^ int(c.head)))
					newCacheEntry := cacheEntry{newLinearized, newState}
					if !cacheContains(model, cache, newCacheEntry) {
						hash := newLinearized.hash()
						cache[hash] = append(cache[hash], newCacheEntry)
						stack = append(stack, stackEntry{op, state, c.head})
						state = newState
						serialized = true
					}
					break
				}
			}
			// pop operations from stack if they are before op
			for comp == HardAfter {
				// pop from stack
				stack = stack[:len(stack)-1]
				state = c.unlift(stackTop)
				// update start of stackTop
				ch.clients[stackTop.operation.ClientId].start[stackTop.opIdx][cIdx] = c.head
				stackTop = stack[len(stack)-1]
				comp, err = consistency.Check(&stackTop.operation, &op)
				if err != nil {
					panic("Error in consistency check: " + err.Error())
				}
			}
			break // only serialize the one operation from front
		}
		// cannot serialize any operation, pop from stack
		if !serialized {
			if len(stack) == 0 {
				return Illegal, LinearizationInfo{}
			}
			// pop from stack
			stackTop := stack[len(stack)-1]
			cId := stackTop.operation.ClientId
			ch.clients[cId].unlift(stackTop)
			stack = stack[:len(stack)-1]
			state = stackTop.state
			linearized.clear(uint(stackTop.opIdx))
		}
		front = ch.getFront()
	}

	return Ok, LinearizationInfo{}
}
