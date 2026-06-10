package porcupine

import (
	"fmt"
	"sync/atomic"
)

type OperationHistory [][]ClientOperation

type ClientOperation struct {
	Op      Operation
	Id      int   // global operation id
	id      int   // alias kept for internal use
	Start   []int // Start[c] is the index of the first operation from client c that has no outgoing dependency to this op
	visited bool  // whether the Start of this op has been computed
}

func newclientOperation(op Operation, numClients int, id int) ClientOperation {
	return ClientOperation{Op: op, Id: id, id: id, Start: nil, visited: false}
}

type client struct {
	id     int
	cltOps []ClientOperation // operations by this client
	head   int               // head is index of first op of this client not pushed to the stack yet
	nDeps  int               // number of unsatisfied dependencies for operation at head
}

func (ch *chains) computeStart(c int, consistency Consistency) {
	if ch.clients[c].head >= len(ch.clients[c].cltOps) || ch.clients[c].cltOps[ch.clients[c].head].visited {
		return
	}
	op := &ch.clients[c].cltOps[ch.clients[c].head]

	// Binary search for the first client that does not have an outgoing dependency to this op
	for d := 0; d < len(ch.clients); d++ {
		if c == d {
			continue
		}
		start := 0
		if ch.clients[c].head > 0 {
			start = ch.clients[c].cltOps[ch.clients[c].head-1].Start[d]
		}
		i := start
		step := 1
		for i < len(ch.clients[d].cltOps) {
			order, err := consistency.Check(&ch.clients[d].cltOps[i], op)
			if err != nil {
				panic(err)
			}
			if order == HardBefore {
				i += step
				step *= 2
			} else {
				break
			}
		}
		high := i
		if high > len(ch.clients[d].cltOps) {
			high = len(ch.clients[d].cltOps)
		}
		low := i - step/2
		if low < start {
			low = start
		}
		for low < high {
			mid := low + (high-low)/2
			order, err := consistency.Check(&ch.clients[d].cltOps[mid], op)
			if err != nil {
				panic(err)
			}
			if order == HardBefore {
				low = mid + 1
			} else {
				high = mid
			}
		}
		op.Start[d] = low
	}
	op.visited = true
}

type chains struct {
	clients  []client
	numOps   int
	frontier []int
}

func (ch *chains) addToFrontier(c int, consistency Consistency) {
	for _, v := range ch.frontier {
		if v == c {
			return
		}
	}
	for i, v := range ch.frontier {
		opC := &ch.clients[c].cltOps[ch.clients[c].head]
		opV := &ch.clients[v].cltOps[ch.clients[v].head]
		order, err := consistency.Check(opC, opV)
		if err != nil {
			panic(err)
		}
		if order == SoftAfter || (order == Unconstrained && c > v) {
			ch.frontier = append(ch.frontier, 0)
			copy(ch.frontier[i+1:], ch.frontier[i:])
			ch.frontier[i] = c
			return
		}
	}
	ch.frontier = append(ch.frontier, c)
}

func (ch *chains) removeFromFrontier(c int) {
	for i, v := range ch.frontier {
		if v == c {
			ch.frontier = append(ch.frontier[:i], ch.frontier[i+1:]...)
			return
		}
	}
}

func (chains *chains) isComplete() bool {
	for _, c := range chains.clients {
		if int(c.head) != len(c.cltOps) {
			return false
		}
	}
	return true
}

// Build the initial chains and frontier
func newChains(history OperationHistory, consistency Consistency) chains {
	numClients := len(history)
	clients := make([]client, numClients)
	n := 0
	// Add all operations to clients
	for i, c := range history {
		clients[i] = client{
			id:     int(i),
			cltOps: c,
			head:   0,
		}
		n += len(c)
	}

	starts := make([]int, n*numClients)
	for i := 0; i < numClients; i++ {
		for j := 0; j < len(clients[i].cltOps); j++ {
			clients[i].cltOps[j].Start = starts[clients[i].cltOps[j].id*numClients : (clients[i].cltOps[j].id+1)*numClients]
		}
	}

	ch := chains{clients: clients, numOps: n, frontier: make([]int, 0, numClients)}
	for i := 0; i < numClients; i++ {
		ch.computeStart(i, consistency)
		ch.clients[i].nDeps = 0
		if len(ch.clients[i].cltOps) > 0 {
			op := ch.clients[i].cltOps[0]
			for j := 0; j < numClients; j++ {
				if j != i && op.Start[j] > 0 {
					ch.clients[i].nDeps++
				}
			}
		}
	}
	for i := 0; i < numClients; i++ {
		if len(ch.clients[i].cltOps) > 0 && ch.clients[i].nDeps == 0 {
			ch.addToFrontier(i, consistency)
		}
	}

	return ch
}

type stackEntry struct { // entry in the stack
	opId        int         // global operation id
	clientId    int         // client of the operation
	state       interface{} // state before this node was pushed on the stack
	opIdx       int         // index of the operation in the client's history
	frontierIdx int         // index of the operation in the frontier, sorted in descending order of soft constraints
}

// Try to lift the operation at the head of this client.
func (c *client) lift(model Model, consistency Consistency, oldState interface{},
	cache map[uint64][]cacheEntry, serialized *bitset) (interface{}, bool) {
	// Is this allowed by the sequential specification?
	cltOp := c.cltOps[c.head]
	ok, newState := consistency.Valid.Step(oldState, cltOp.Op.Input, cltOp.Op.Output, model)
	if !ok {
		return newState, ok
	}

	// check cache
	serialized.set(uint(cltOp.id))
	hash := serialized.hash()

	if entries, ok := cache[hash]; ok {
		for _, elem := range entries {
			if serialized.equals(elem.linearized) && model.Equal(newState, elem.state) {
				serialized.clear(uint(cltOp.id))
				return oldState, false
			}
		}
	}

	cache[hash] = append(cache[hash], cacheEntry{serialized.clone(), newState})
	c.head++
	return newState, ok
}

// Unlift the operation at the top of the stack.
func (c *client) unlift(top stackEntry, serialized *bitset) {
	if c.id != top.clientId {
		panic("Tried to unlift someone else's operation")
	}
	c.head--
	serialized.clear(uint(top.opId))
}

// Try to lift an operation from the frontier. If successful, push it on the stack and return the new state.
func (ch *chains) lift(model Model, consistency Consistency, oldState interface{}, stack *[]stackEntry,
	cache map[uint64][]cacheEntry, serialized *bitset, liftFrom int) (interface{}, bool) {
	// Try to lift an operation from the frontier starting from "liftFrom"
	for idx := liftFrom; idx >= 0; idx-- {
		i := ch.frontier[idx]
		e := stackEntry{
			opId:        ch.clients[i].cltOps[ch.clients[i].head].id,
			clientId:    ch.clients[i].id,
			state:       oldState,
			opIdx:       ch.clients[i].head,
			frontierIdx: idx,
		}
		newState, success := ch.clients[i].lift(model, consistency, oldState, cache, serialized)
		if success {
			*stack = append(*stack, e)
			ch.computeStart(i, consistency)

			ch.removeFromFrontier(i)

			if ch.clients[i].head < len(ch.clients[i].cltOps) {
				opI := ch.clients[i].cltOps[ch.clients[i].head]
				ch.clients[i].nDeps = 0
				for k := 0; k < len(ch.clients); k++ {
					if k != i && opI.Start[k] > ch.clients[k].head {
						ch.clients[i].nDeps++
					}
				}
				if ch.clients[i].nDeps == 0 {
					ch.addToFrontier(i, consistency)
				}
			}

			for j := 0; j < len(ch.clients); j++ {
				if j != i && ch.clients[j].head < len(ch.clients[j].cltOps) {
					if ch.clients[j].cltOps[ch.clients[j].head].Start[i] == ch.clients[i].head {
						ch.clients[j].nDeps--
						if ch.clients[j].nDeps == 0 {
							ch.addToFrontier(j, consistency)
						}
					}
				}
			}
			return newState, success
		}
	}
	return nil, false
}

// Unlift an operation from the stack and return the state and the client id of the unlifted operation
func (ch *chains) unlift(stack *[]stackEntry, serialized *bitset, consistency Consistency) (interface{}, int) {
	top := (*stack)[len(*stack)-1]
	clientId := top.clientId
	client := &ch.clients[clientId]

	ch.removeFromFrontier(clientId)
	client.unlift(top, serialized)
	*stack = (*stack)[:len(*stack)-1]

	ch.clients[clientId].nDeps = 0

	for j := 0; j < len(ch.clients); j++ {
		if j != clientId && ch.clients[j].head < len(ch.clients[j].cltOps) {
			if ch.clients[j].cltOps[ch.clients[j].head].Start[clientId] == ch.clients[clientId].head+1 {
				if ch.clients[j].nDeps == 0 {
					ch.removeFromFrontier(j)
				}
				ch.clients[j].nDeps++
			}
		}
	}
	ch.addToFrontier(clientId, consistency)

	return top.state, top.frontierIdx
}

func checkSingle(model Model, consistency Consistency, history OperationHistory, computePartial bool, kill *int32) (bool, []*[]int) {
	// Initialize chains
	ch := newChains(history, consistency)

	serialized := newBitset(uint(ch.numOps))
	cache := make(map[uint64][]cacheEntry) // map from hash to cache entry

	stack := []stackEntry{}
	state := consistency.Valid.Init(model) // initial state

	longest := make([]*[]int, ch.numOps)

	// The index of the first client to try to lift from the frontier
	liftFrom := len(ch.frontier) - 1

	// Counter for ch.unlift() calls
	pops := 0

	// while all operations are not serialized, explore permutations
	for !ch.isComplete() {
		if atomic.LoadInt32(kill) != 0 {
			fmt.Printf("Total_pop: %d\n", pops)
			return false, longest
		}
		// try serializing an operation from frontier
		newState, ok := ch.lift(model, consistency, state, &stack, cache, &serialized, liftFrom)
		if ok {
			state = newState
			liftFrom = len(ch.frontier) - 1
		} else {
			if len(stack) == 0 {
				// no possible serialization
				fmt.Printf("Total_pop: %d\n", pops)
				return false, longest
			}
			if computePartial {
				callsLen := len(stack)
				var seq []int = nil
				for _, v := range stack {
					if longest[v.opId] == nil || callsLen > len(*longest[v.opId]) {
						// create seq lazily
						if seq == nil {
							seq = make([]int, len(stack))
							for j, w := range stack {
								seq[j] = w.opId
							}
						}
						longest[v.opId] = &seq
					}
				}
			}
			top, unlifted_id := ch.unlift(&stack, &serialized, consistency)
			pops++ // increment pop counter
			state = top
			liftFrom = unlifted_id - 1
		}
	}

	seq := make([]int, len(stack))
	for i, v := range stack {
		seq[i] = v.opId
	}
	for i := 0; i < ch.numOps; i++ {
		longest[i] = &seq
	}
	fmt.Printf("Total_pop: %d\n", pops)
	return true, longest
}
