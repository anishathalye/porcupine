package porcupine

import (
	"sync/atomic"
)

type clientOperation struct {
	op      Operation
	id      int   // global operation id
	start   []int // start[c] is the index of the first operation from client c that has no outgoing dependency to this op
	visited bool  // whether the start of this op has been computed
}

func newclientOperation(op Operation, numClients int, id int) clientOperation {
	return clientOperation{op: op, start: make([]int, numClients), visited: false, id: id}
}

type client struct {
	id     int
	cltOps []clientOperation // operations by this client
	head   int               // head is not pushed to the stack yet
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
			start = ch.clients[c].cltOps[ch.clients[c].head-1].start[d]
		}
		i := start
		step := 1
		for i < len(ch.clients[d].cltOps) {
			order, err := consistency.Check(&ch.clients[d].cltOps[i].op, &op.op)
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
			order, err := consistency.Check(&ch.clients[d].cltOps[mid].op, &op.op)
			if err != nil {
				panic(err)
			}
			if order == HardBefore {
				low = mid + 1
			} else {
				high = mid
			}
		}
		op.start[d] = low
	}
	op.visited = true
}

type chains struct {
	clients  []client
	numOps   int
	frontier []int
}

func (ch *chains) canLift(c int) bool {
	if ch.clients[c].head >= len(ch.clients[c].cltOps) {
		return false
	}
	op := ch.clients[c].cltOps[ch.clients[c].head]
	for j := 0; j < len(ch.clients); j++ {
		if op.start[j] > ch.clients[j].head {
			return false
		}
	}
	return true
}

func (ch *chains) addToFrontier(c int, consistency Consistency) {
	for _, v := range ch.frontier {
		if v == c {
			return
		}
	}
	for i, v := range ch.frontier {
		opC := &ch.clients[c].cltOps[ch.clients[c].head].op
		opV := &ch.clients[v].cltOps[ch.clients[v].head].op
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
func newChains(history [][]Operation, consistency Consistency) chains {
	numClients := len(history)
	clients := make([]client, numClients)
	n := 0
	// Add all operations to clients and assign them global ids.
	for i, c := range history {
		clientOps := make([]clientOperation, 0)
		for _, op := range c {
			clientOps = append(clientOps, newclientOperation(op, numClients, n))
			n++
		}
		clients[i] = client{
			id:     int(i),
			cltOps: clientOps,
			head:   0,
		}
	}

	ch := chains{clients: clients, numOps: n, frontier: make([]int, 0, numClients)}
	for i := 0; i < numClients; i++ {
		ch.computeStart(i, consistency)
	}
	for i := 0; i < numClients; i++ {
		if ch.canLift(i) {
			ch.addToFrontier(i, consistency)
		}
	}

	return ch
}

type stackEntry struct { // entry in the stack
	cltOp       clientOperation // operation has ClientID in it. This operation was pushed on the stack
	state       interface{}     // state before this node was pushed on the stack
	opIdx       int             // index of the operation in the client's history
	frontierIdx int             // index of the operation in the frontier, sorted in descending order of soft constraints
}

// Try to lift the operation at the head of this client. If successful, push
// it on the stack and return the new state.
func (c *client) lift(model Model, oldState interface{}, stack *[]stackEntry,
	cache map[uint64][]cacheEntry, serialized *bitset) (interface{}, bool) {
	// Is this allowed by the sequential specification?
	cltOp := c.cltOps[c.head]
	ok, newState := model.Step(oldState, cltOp.op.Input, cltOp.op.Output)
	if !ok {
		return newState, ok
	}

	// check cache
	newSerialized := serialized.clone().set(uint(cltOp.id)) // add to bit set
	newCacheEntry := cacheEntry{newSerialized, newState}
	if cacheContains(model, cache, newCacheEntry) {
		return oldState, false
	}

	hash := newSerialized.hash()
	cache[hash] = append(cache[hash], newCacheEntry)
	serialized.set(uint(cltOp.id))
	e := stackEntry{
		cltOp: cltOp,
		state: oldState,
		opIdx: c.head,
	}
	*stack = append(*stack, e)
	c.head++
	return newState, ok
}

// Unlift the operation at the top of the stack.
func (c *client) unlift(top stackEntry, serialized *bitset) {
	if c.id != int(top.cltOp.op.ClientId) {
		panic("Tried to unlift someone else's operation")
	}
	c.head--
	serialized.clear(uint(top.cltOp.id))
}

// Try to lift an operation from the frontier. If successful, push it on the stack and return the new state.
func (ch *chains) lift(model Model, consistency Consistency, oldState interface{}, stack *[]stackEntry,
	cache map[uint64][]cacheEntry, serialized *bitset, liftFrom int) (interface{}, bool) {
	// Try to lift an operation from the frontier starting from "liftFrom"
	for idx := liftFrom; idx >= 0; idx-- {
		i := ch.frontier[idx]
		newState, success := ch.clients[i].lift(model, oldState, stack, cache, serialized)
		if success {
			(*stack)[len(*stack)-1].frontierIdx = idx
			ch.computeStart(i, consistency)

			ch.removeFromFrontier(i)
			if ch.canLift(i) {
				ch.addToFrontier(i, consistency)
			}
			for j := 0; j < len(ch.clients); j++ {
				if j != i && ch.canLift(j) {
					ch.addToFrontier(j, consistency)
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
	clientId := int(top.cltOp.op.ClientId)
	client := &ch.clients[clientId]

	ch.removeFromFrontier(clientId)
	client.unlift(top, serialized)
	*stack = (*stack)[:len(*stack)-1]

	for j := 0; j < len(ch.clients); j++ {
		if j != clientId && !ch.canLift(j) {
			ch.removeFromFrontier(j)
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
	state := model.Init() // initial state

	longest := make([]*[]int, ch.numOps)

	// The index of the first client to try to lift from the frontier
	liftFrom := len(ch.frontier) - 1

	// while all operations are not serialized, explore permutations
	for !ch.isComplete() {
		if atomic.LoadInt32(kill) != 0 {
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
				return false, longest
			}
			if computePartial {
				callsLen := len(stack)
				var seq []int = nil
				for _, v := range stack {
					if longest[v.cltOp.id] == nil || callsLen > len(*longest[v.cltOp.id]) {
						// create seq lazily
						if seq == nil {
							seq = make([]int, len(stack))
							for i, v := range stack {
								seq[i] = v.cltOp.id
							}
						}
						longest[v.cltOp.id] = &seq
					}
				}
			}
			top, unlifted_id := ch.unlift(&stack, &serialized, consistency)
			state = top
			liftFrom = unlifted_id - 1
		}
	}

	seq := make([]int, len(stack))
	for i, v := range stack {
		seq[i] = v.cltOp.id
	}
	for i := 0; i < ch.numOps; i++ {
		longest[i] = &seq
	}
	return true, longest
}
