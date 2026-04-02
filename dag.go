package porcupine

import "sync/atomic"

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
	clients []client
	numOps  int
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

	ch := chains{clients: clients, numOps: n}
	for i := 0; i < numClients; i++ {
		ch.computeStart(i, consistency)
	}

	return ch
}

type stackEntry struct { // entry in the stack
	cltOp clientOperation // operation has ClientID in it. This operation was pushed on the stack
	state interface{}     // state before this node was pushed on the stack
	opIdx int             // index of the operation in the client's history
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
	cache map[uint64][]cacheEntry, serialized *bitset, liftFrom int) (interface{}, bool, int) {
	numClients := len(ch.clients)
	// Try to lift an operation from the frontier starting from "liftFrom"
	for i := liftFrom; i < len(ch.clients); i++ {
		if ch.clients[i].head >= len(ch.clients[i].cltOps) {
			continue
		}
		op := ch.clients[i].cltOps[ch.clients[i].head]
		canLift := true

		// check if all dependencies are serialized
		for j := 0; j < numClients; j++ {
			if op.start[j] > ch.clients[j].head {
				canLift = false
				break
			}
		}
		if !canLift {
			continue
		}
		newState, success := ch.clients[i].lift(model, oldState, stack, cache, serialized)
		if success {
			return newState, success, i
		}
	}
	return nil, false, -1
}

// Unlift an operation from the stack and return the state and the client id of the unlifted operation
func (ch *chains) unlift(stack *[]stackEntry, serialized *bitset, consistency Consistency) (interface{}, int) {
	top := (*stack)[len(*stack)-1]
	clientId := int(top.cltOp.op.ClientId)
	client := &ch.clients[clientId]
	client.unlift(top, serialized)
	*stack = (*stack)[:len(*stack)-1]
	return top.state, clientId
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
	liftFrom := 0

	// while all operations are not serialized, explore permutations
	for !ch.isComplete() {
		if atomic.LoadInt32(kill) != 0 {
			return false, longest
		}
		// try serializing an operation from frontier
		newState, ok, liftedIdx := ch.lift(model, consistency, state, &stack, cache, &serialized, liftFrom)
		if ok {
			state = newState
			liftFrom = 0
			ch.computeStart(liftedIdx, consistency)
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
			liftFrom = unlifted_id + 1
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
