package porcupine

import (
	"time"
)

type ClientId int
type OperationIdx int

type ClientOperation struct {
	op    Operation
	start []OperationIdx // start[c] is the index of the first operation from client c that has no outgoing dependency to this op
}

func newClientOperation(op Operation, numClients int) ClientOperation {
	return ClientOperation{op: op, start: make([]OperationIdx, numClients)}
}

type client struct {
	id   ClientId
	ops  []ClientOperation // operations by this client
	head OperationIdx      // head is not pushed to the stack yet
}

type chains struct {
	clients  []client
	frontier []ClientId // The head of these clients are in the frontier. Sorted by which client should be serialized first.
}

func newChains(clients []client, consistency Consistency) chains {
	numClients := len(clients)
	for i := 0; i < numClients; i++ {
		i_op := clients[i].ops[0]
		for j := i + 1; j < numClients; j++ {
			j_op := clients[j].ops[0]
			order, err := consistency.Check(&i_op.op, &j_op.op)
			if err != nil {
				panic(err)
			}
			switch order {
			case HardBefore:
				j_op.start[i] = 1
			case HardAfter:
				i_op.start[j] = 1
			}
		}
	}

	frontier := []ClientId{}
	for i := 0; i < numClients; i++ {
		blocked := false
		for j := 0; j < numClients; j++ {
			if i != j && clients[j].head < clients[i].ops[0].start[j] {
				blocked = true
				break
			}
		}
		if !blocked {
			frontier = append(frontier, ClientId(i))
		}
	}

	return chains{clients: clients, frontier: frontier}
}

type stackEntry struct { // entry in the stack
	operation Operation    // operation has ClientID in it. This operation was pushed on the stack
	state     interface{}  // state before this node was pushed on the stack
	opIdx     OperationIdx // index of the operation in the client's history
	// frontier  []int        // frontier after this node was pushed on the stack, i.e., it does not contain the node
}

func (c *client) lift(model Model, oldState interface{}, stack []stackEntry) (interface{}, bool) {
	// Is this allowed by the sequential specification?
	op := c.ops[c.head].op
	ok, newState := model.Step(oldState, op.Input, op.Output)
	if !ok {
		return newState, ok
	}

	e := stackEntry{
		operation: op,
		state:     oldState,
		opIdx:     c.head,
	}
	stack = append(stack, e)
	c.head++
	return newState, ok
}

func (c *client) unlift(top stackEntry) {
	if c.id != ClientId(top.operation.ClientId) {
		panic("Tried to unlift someone else's operation")
	}
	c.head--
}

func (ch *chains) lift(model Model, consistency Consistency, oldState interface{}, stack []stackEntry) (interface{}, bool) {
	numClients := len(ch.clients)
	for len(ch.frontier) != 0 {
		// Keep on trying to lift until we have exhausted the frontier
		for _, i := range ch.frontier {
			i_op := ch.clients[i].ops[ch.clients[i].head]
			canLift := true
			// Is this allowed by the consistency order
			var j ClientId
			for j = 0; j < ClientId(numClients); j++ {
				if i == j || ch.clients[j].head == 0 {
					continue
				}
				j_op := ch.clients[j].ops[ch.clients[j].head-1]

				order, err := consistency.Check(&i_op.op, &j_op.op)
				if err != nil {
					panic(err)
				}
				if order == HardBefore {
					// i happened before j. Pop the stack till j
					for true {
						top := ch.unlift(stack)
						oldState = top.state
						if ClientId(top.operation.ClientId) == j {
							break
						}
						if ClientId(top.operation.ClientId) == i {
							canLift = false
						}
					}
				}
			}

			if !canLift {
				break
			}
			// I am ready to lift this operation!
			newState, success := ch.clients[i].lift(model, oldState, stack)
			if success {
				// update my frontier
				return newState, success
			}
		}
	}
	return nil, false
}

func (ch *chains) unlift(stack []stackEntry) stackEntry {
	top := stack[len(stack)-1]
	client := ch.clients[top.operation.ClientId]
	// TODO: Update frontier
	client.unlift(top)
	stack = stack[:len(stack)-1]
	return top
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
	// Initialize chains
	ch := &chains{}
	numClients := len(history)
	for i, c := range history {
		clientOps := make([]ClientOperation, 0)
		for _, op := range c {
			clientOps = append(clientOps, newClientOperation(op, numClients))
		}
		ch.clients = append(ch.clients, client{
			id:   ClientId(i),
			ops:  clientOps,
			head: 0,
		})
	}

	// Initialize cache
	n := 0
	for _, c := range history {
		n += len(c)
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
