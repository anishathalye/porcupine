package porcupine

import "sync/atomic"

type clientOperation struct {
	op    Operation
	id    int   // global operation id
	start []int // start[c] is the index of the first operation from client c that has no outgoing dependency to this op
}

func newclientOperation(op Operation, numClients int, id int) clientOperation {
	return clientOperation{op: op, start: make([]int, numClients), id: id}
}

type client struct {
	id     int
	cltOps []clientOperation // operations by this client
	head   int               // head is not pushed to the stack yet
	nDeps  int               // number of incoming dependencies of the head
}

func addToFrontier(frontier []int, val int) []int {
	for _, v := range frontier {
		if v == val {
			return frontier
		}
	}
	return append(frontier, val)
}

func removeFromFrontier(frontier []int, val int) []int {
	for i, v := range frontier {
		if v == val {
			return append(frontier[:i], frontier[i+1:]...)
		}
	}
	return frontier
}

type chains struct {
	clients  []client
	frontier []int // The head of these clients are in the frontier.
	numOps   int
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

	// Check consistency order and build the initial frontier
	for i := 0; i < numClients; i++ {
		if len(clients[i].cltOps) == 0 {
			continue
		}
		i_op := clients[i].cltOps[0]
		for j := i + 1; j < numClients; j++ {
			if len(clients[j].cltOps) == 0 {
				continue
			}
			j_op := clients[j].cltOps[0]
			order, err := consistency.Check(&i_op.op, &j_op.op)
			if err != nil {
				panic(err)
			}
			switch order {
			case HardBefore:
				j_op.start[i] = 1
				clients[j].nDeps++
			case HardAfter:
				i_op.start[j] = 1
				clients[i].nDeps++
			}
		}
	}

	// create frontier
	frontier := make([]int, 0)
	for i := 0; i < numClients; i++ {
		if clients[i].nDeps == 0 && len(clients[i].cltOps) > clients[i].head {
			frontier = addToFrontier(frontier, int(i))
		}
	}

	return chains{clients: clients, frontier: frontier, numOps: n}
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
	cache map[uint64][]cacheEntry, serialized *bitset) (interface{}, bool) {
	numClients := len(ch.clients)
	for len(ch.frontier) != 0 {
		updated := false
		// Keep on trying to lift until we have exhausted the frontier
		for _, i := range ch.frontier {
			i_op := ch.clients[i].cltOps[ch.clients[i].head]
			canLift := true

			// Is this allowed by the consistency order? Check with all other clients
			for j := 0; j < numClients; j++ {
				if i == j || ch.clients[j].head == 0 {
					continue
				}
				// The last operation from client j that has been lifted
				j_op := ch.clients[j].cltOps[ch.clients[j].head-1]

				order, err := consistency.Check(&i_op.op, &j_op.op)
				if err != nil {
					panic(err)
				}
				if order == HardBefore {
					updated = true
					canLift = false
					// i happened before j. Pop the stack till j comes out
					for len(*stack) > 0 {
						top := ch.unlift(stack, serialized)
						oldState = top.state
						if int(top.cltOp.op.ClientId) == j {
							top.cltOp.start[i] = ch.clients[i].head + 1
							ch.clients[j].nDeps++
							ch.frontier = removeFromFrontier(ch.frontier, j)
							break
						}
					}
					break
				}
			}
			if !canLift {
				if updated {
					break
				}
				continue
			}
			newState, success := ch.clients[i].lift(model, oldState, stack, cache, serialized)
			if success {
				ch.frontier = removeFromFrontier(ch.frontier, i)
				if ch.clients[i].head < len(ch.clients[i].cltOps) {
					nDeps := 0
					for k := 0; k < numClients; k++ {
						if i != k && ch.clients[k].head < ch.clients[i].cltOps[ch.clients[i].head].start[k] {
							nDeps++
						}
					}
					ch.clients[i].nDeps = nDeps
					if nDeps == 0 {
						ch.frontier = addToFrontier(ch.frontier, i)
					} else {
						ch.frontier = removeFromFrontier(ch.frontier, i)
					}
				}
				for j := 0; j < numClients; j++ {
					if i != j && ch.clients[j].head < len(ch.clients[j].cltOps) {
						if ch.clients[i].head == ch.clients[j].cltOps[ch.clients[j].head].start[i] {
							ch.clients[j].nDeps--
							if ch.clients[j].nDeps == 0 {
								ch.frontier = addToFrontier(ch.frontier, j)
							} else {
								ch.frontier = removeFromFrontier(ch.frontier, j)
							}
						}
					}
				}
				return newState, success
			}
		}
		if !updated {
			break
		}
	}
	return nil, false
}

// Unlift the operation at the top of the stack and return it.
func (ch *chains) unlift(stack *[]stackEntry, serialized *bitset) stackEntry {
	top := (*stack)[len(*stack)-1]
	clientId := int(top.cltOp.op.ClientId)
	client := &ch.clients[clientId]
	client.unlift(top, serialized)
	nDeps := 0

	for j := 0; j < len(ch.clients); j++ {
		if clientId != j && ch.clients[j].head < client.cltOps[client.head].start[j] {
			nDeps++
		}
	}
	client.nDeps = nDeps
	if nDeps == 0 {
		ch.frontier = addToFrontier(ch.frontier, clientId)
	} else {
		ch.frontier = removeFromFrontier(ch.frontier, clientId)
	}

	for j := 0; j < len(ch.clients); j++ {
		if clientId != j && ch.clients[j].head < len(ch.clients[j].cltOps) {
			if ch.clients[clientId].head+1 == ch.clients[j].cltOps[ch.clients[j].head].start[clientId] {
				ch.clients[j].nDeps++
				ch.frontier = removeFromFrontier(ch.frontier, j)
			}
		}
	}

	*stack = (*stack)[:len(*stack)-1]
	return top
}

func checkSingle(model Model, consistency Consistency, history OperationHistory, computePartial bool, kill *int32) (bool, []*[]int) {
	// Initialize chains
	ch := newChains(history, consistency)

	serialized := newBitset(uint(ch.numOps))
	cache := make(map[uint64][]cacheEntry) // map from hash to cache entry

	stack := []stackEntry{}
	state := model.Init() // initial state

	longest := make([]*[]int, ch.numOps)

	// while all operations are not serialized, explore permutations
	for !ch.isComplete() {
		if atomic.LoadInt32(kill) != 0 {
			return false, longest
		}
		// try serializing an operation from frontier
		newState, ok := ch.lift(model, consistency, state, &stack, cache, &serialized)
		if ok {
			state = newState
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
			top := ch.unlift(&stack, &serialized)
			state = top.state
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
