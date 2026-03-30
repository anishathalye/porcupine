package porcupine

type ClientId int
type OperationIdx int

type ClientOperation struct {
	op    Operation
	id    int            // global operation id
	start []OperationIdx // start[c] is the index of the first operation from client c that has no outgoing dependency to this op
}

func newClientOperation(op Operation, numClients int, id int) ClientOperation {
	return ClientOperation{op: op, start: make([]OperationIdx, numClients), id: id}
}

type client struct {
	id     ClientId
	cltOps []ClientOperation // operations by this client
	head   OperationIdx      // head is not pushed to the stack yet
}

type chains struct {
	clients  []client
	frontier []ClientId // The head of these clients are in the frontier. Sorted by which client should be serialized first.
	numOps   int
}

func newChains(history [][]Operation, consistency Consistency) chains {

	numClients := len(history)
	clients := make([]client, numClients)
	n := 0
	for i, c := range history {
		clientOps := make([]ClientOperation, 0)
		for _, op := range c {
			clientOps = append(clientOps, newClientOperation(op, numClients, n))
			n++
		}
		clients[i] = client{
			id:     ClientId(i),
			cltOps: clientOps,
			head:   0,
		}
	}

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
			case HardAfter:
				i_op.start[j] = 1
			}
		}
	}

	frontier := []ClientId{}
	for i := 0; i < numClients; i++ {
		if len(clients[i].cltOps) == 0 {
			continue
		}
		blocked := false
		for j := 0; j < numClients; j++ {
			if i != j && clients[j].head < clients[i].cltOps[0].start[j] {
				blocked = true
				break
			}
		}
		if !blocked {
			frontier = append(frontier, ClientId(i))
		}
	}

	return chains{clients: clients, frontier: frontier, numOps: n}
}

type stackEntry struct { // entry in the stack
	cltOp ClientOperation // operation has ClientID in it. This operation was pushed on the stack
	state interface{}     // state before this node was pushed on the stack
	opIdx OperationIdx    // index of the operation in the client's history
	// frontier  []int        // frontier after this node was pushed on the stack, i.e., it does not contain the node
}

func (c *client) lift(model Model, oldState interface{}, stack *[]stackEntry, cache map[uint64][]cacheEntry, serialized *bitset) (interface{}, bool) {
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

func (c *client) unlift(top stackEntry, serialized *bitset) {
	if c.id != ClientId(top.cltOp.op.ClientId) {
		panic("Tried to unlift someone else's operation")
	}
	c.head--
	serialized.clear(uint(top.cltOp.id))
}

func (ch *chains) lift(model Model, consistency Consistency, oldState interface{}, stack *[]stackEntry, cache map[uint64][]cacheEntry, serialized *bitset) (interface{}, bool) {
	numClients := len(ch.clients)
	for len(ch.frontier) != 0 {
		updated := false
		// Keep on trying to lift until we have exhausted the frontier
		for _, i := range ch.frontier {
			i_op := ch.clients[i].cltOps[ch.clients[i].head]
			canLift := true

			// Is this allowed by the consistency order? Check with all other clients
			var j ClientId
			for j = 0; j < ClientId(numClients); j++ {
				if i == j || ch.clients[j].head == 0 {
					continue
				}
				j_op := ch.clients[j].cltOps[ch.clients[j].head-1]

				order, err := consistency.Check(&i_op.op, &j_op.op)
				if err != nil {
					panic(err)
				}
				if order == HardBefore {
					updated = true
					// i happened before j. Pop the stack till j comes out
					for true {
						top := ch.unlift(stack, serialized)
						oldState = top.state
						if ClientId(top.cltOp.op.ClientId) == j {
							top.cltOp.start[i] = ch.clients[i].head + 1 // update start
							break
						}
						if ClientId(top.cltOp.op.ClientId) == i {
							canLift = false
						}
					}
				}
			}

			if !canLift {
				break
			}
			// I am ready to lift this operation!
			newState, success := ch.clients[i].lift(model, oldState, stack, cache, serialized)
			if success {
				updated = true
				ch.update(i)
				return newState, success
			}
		}
		if !updated {
			break
		}
	}
	return nil, false
}

func (ch *chains) unlift(stack *[]stackEntry, serialized *bitset) stackEntry {
	top := (*stack)[len(*stack)-1]
	client := &ch.clients[top.cltOp.op.ClientId]
	client.unlift(top, serialized)
	ch.update(ClientId(top.cltOp.op.ClientId))
	*stack = (*stack)[:len(*stack)-1]
	return top
}

func (ch *chains) update(i ClientId) {
	// Rebuild the entire frontier: check ALL clients for eligibility,
	// not just the ones previously in the frontier. When client i's head
	// changes, previously-blocked clients may become unblocked.
	inFrontier := make(map[ClientId]bool)
	for _, j := range ch.frontier {
		if j != i {
			inFrontier[j] = true
		}
	}

	frontier := []ClientId{}
	for j := 0; j < len(ch.clients); j++ {
		jId := ClientId(j)
		if int(ch.clients[j].head) >= len(ch.clients[j].cltOps) {
			continue
		}
		blocked := false
		for k := 0; k < len(ch.clients); k++ {
			if jId != ClientId(k) && ch.clients[k].head < ch.clients[j].cltOps[ch.clients[j].head].start[k] {
				blocked = true
				break
			}
		}
		if !blocked {
			frontier = append(frontier, jId)
		}
	}
	ch.frontier = frontier
}

func checkSingle(model Model, consistency Consistency, history OperationHistory, computePartial bool, kill *int32) (bool, []*[]int) {
	// Initialize chains
	ch := newChains(history, consistency)

	serialized := newBitset(uint(ch.numOps))
	cache := make(map[uint64][]cacheEntry) // map from hash to cache entry

	stack := []stackEntry{}
	state := model.Init()

	isComplete := true
	for _, c := range ch.clients {
		if int(c.head) != len(c.cltOps) {
			isComplete = false
			break
		}
	}

	// while all operations are not serialized, explore permutations
	for !isComplete {
		// try serializing an operations from frontier
		newState, ok := ch.lift(model, consistency, state, &stack, cache, &serialized)
		if ok {
			state = newState
		} else {
			if len(stack) == 0 {
				// no possible serialization
				return false, nil
			}
			top := ch.unlift(&stack, &serialized)
			state = top.state
		}

		isComplete = true
		for _, c := range ch.clients {
			if int(c.head) != len(c.cltOps) {
				isComplete = false
				break
			}
		}
	}

	return true, nil
}
