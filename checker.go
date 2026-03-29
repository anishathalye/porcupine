package porcupine

import (
	"sort"
	"strconv"
	"sync/atomic"
	"time"
)

type entryKind bool

const (
	callEntry   entryKind = false
	returnEntry entryKind = true
)

type entry struct {
	kind     entryKind
	value    interface{}
	id       int
	time     int64
	clientId int
	metadata interface{}
	hint     interface{}
}

type LinearizationInfo struct {
	history               [][]entry // for each partition, a list of entries
	partialLinearizations [][][]int // for each partition, a set of histories (list of ids)
	annotations           []Annotation
}

// PartialLinearizations returns partial linearizations found during the
// linearizability check, as sets of operation IDs.
//
// For each partition, it returns a set of possible linearization histories,
// where each history is represented as a sequence of operation IDs. If the
// history is linearizable, this will contain a complete linearization. If not
// linearizable, it contains the maximal partial linearizations found.
func (li *LinearizationInfo) PartialLinearizations() [][][]int {
	return li.partialLinearizations
}

// PartialLinearizationsOperations returns partial linearizations found during
// the linearizability check, as sets of sequences of [Operation].
//
// For each partition, it returns a set of possible linearization histories,
// where each history is represented as a sequence of [Operation]. If the
// history is linearizable, this will contain a complete linearization. If not
// linearizable, it contains the maximal partial linearizations found.
func (li *LinearizationInfo) PartialLinearizationsOperations() [][][]Operation {
	result := make([][][]Operation, len(li.history))
	for p, partition := range li.history {
		// reconstruct operations based on entries
		callMap := make(map[int]entry)
		retMap := make(map[int]entry)
		for _, e := range partition {
			if e.kind == callEntry {
				callMap[e.id] = e
			} else {
				retMap[e.id] = e
			}
		}

		opMap := make(map[int]Operation)
		for id, call := range callMap {
			ret, ok := retMap[id]
			if !ok {
				// this should never happen, because the LinearizationInfo
				// object should always contain valid partial linearizations,
				// where there is a return for every call
				panic("cannot find corresponding return for call")
			}
			// prefer return metadata over call metadata
			metadata := call.metadata
			if ret.metadata != nil {
				metadata = ret.metadata
			}
			opMap[id] = Operation{
				ClientId: call.clientId,
				Input:    call.value,
				Call:     call.time,
				Output:   ret.value,
				Return:   ret.time,
				Metadata: metadata,
			}
		}

		partials := make([][]Operation, len(li.partialLinearizations[p]))
		for i, linearization := range li.partialLinearizations[p] {
			partials[i] = make([]Operation, len(linearization))
			for j, id := range linearization {
				op, exists := opMap[id]
				if !exists {
					// this should never happen, because the LinearizationInfo
					// object should always contain valid partial
					// linearizations, where every ID in the partial
					// linearization is in the history
					panic("cannot find operation for given id in linearization")
				}
				partials[i][j] = op
			}
		}
		result[p] = partials
	}
	return result
}

type byTime []entry

func (a byTime) Len() int {
	return len(a)
}

func (a byTime) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byTime) Less(i, j int) bool {
	if a[i].time != a[j].time {
		return a[i].time < a[j].time
	}
	// if the timestamps are the same, we need to make sure we order calls
	// before returns
	return a[i].kind == callEntry && a[j].kind == returnEntry
}

func makeEntries(history []Operation) []entry {
	var entries []entry = nil
	id := 0
	for _, elem := range history {
		entries = append(entries, entry{
			callEntry, elem.Input, id, elem.Call, elem.ClientId, elem.Metadata, elem.Hint})
		entries = append(entries, entry{
			returnEntry, elem.Output, id, elem.Return, elem.ClientId, elem.Metadata, elem.Hint})
		id++
	}
	// this is where we sort operations by time
	sort.Sort(byTime(entries))
	return entries
}

func renumber(events []Event) []Event {
	var e []Event
	m := make(map[int]int) // renumbering
	id := 0
	for _, v := range events {
		if r, ok := m[v.Id]; ok {
			e = append(e, Event{ClientId: v.ClientId, Kind: v.Kind, Value: v.Value, Id: r, Metadata: v.Metadata, Hint: v.Hint})
		} else {
			e = append(e, Event{ClientId: v.ClientId, Kind: v.Kind, Value: v.Value, Id: id, Metadata: v.Metadata, Hint: v.Hint})
			m[v.Id] = id
			id++
		}
	}
	return e
}

func convertEntries(events []Event) []entry {
	entries := make([]entry, len(events))
	m := make(map[int]int)
	for i, elem := range events {
		var kind entryKind
		if elem.Kind == CallEvent {
			kind = callEntry
			m[elem.Id] = i
		}
		if elem.Kind == ReturnEvent {
			kind = returnEntry
			callIdx, ok := m[elem.Id]
			if !ok {
				panic("return entry with id: " + strconv.Itoa(elem.Id) + " has no matching call")
			}
			entries[callIdx].hint = elem.Hint
			delete(m, elem.Id)
		}
		// use index as "time"
		entries[i] = entry{kind, elem.Value, elem.Id, int64(i), elem.ClientId, elem.Metadata, elem.Hint}
	}
	return entries
}

type cacheEntry struct {
	linearized bitset
	state      interface{}
}

func cacheContains(model Model, cache map[uint64][]cacheEntry, entry cacheEntry) bool {
	for _, elem := range cache[entry.linearized.hash()] {
		// cache entry is valid if bitset is the same and the state is the same. We
		// will skip this linearization!
		if entry.linearized.equals(elem.linearized) && model.Equal(entry.state, elem.state) {
			return true
		}
	}
	return false
}

// takes two operations and returns the type of order
type comparator func(a, b interface{}) OrderKind

type dag struct {
	ops    []*Node          // map from id to Node. Every operation (across clients) has a unique ID
	depts  [][]int          // outgoing edges
	lDepts [][]int          // likely outgoing edges
	nDeps  []int            // number of incoming edges
	nLDeps []int            // number of incoming likely edges
	front  map[int]struct{} // frontier used while enumerating topological orders
	comp   comparator       // user-provided comparator
}

func setToSlice(set map[*Node]struct{}) []int {
	s := make([]int, 0, len(set))
	for k := range set {
		s = append(s, k.Id)
	}
	return s
}

// start with a history: list of call and returns across all clients, sorted by
// real-time
func (d *dag) Init(history []entry, model Model) {
	if d.comp == nil {
		d.comp = func(a, b interface{}) OrderKind { return Unconstrained }
	}
	n := len(history) / 2
	d.ops = make([]*Node, n)
	d.depts = make([][]int, n)
	d.lDepts = make([][]int, n)
	d.nDeps = make([]int, n)
	d.nLDeps = make([]int, n)

	for _, elem := range history {
		if elem.kind == callEntry {
			// create the node at call
			node := Node{Id: elem.id, Input: elem.value, Hint: elem.hint, Call: elem.time, ClientId: elem.clientId}
			d.ops[elem.id] = &node
		} else {
			// update the node at return
			op := d.ops[elem.id]
			op.Output = elem.value
			op.Ret = elem.time
		}
	}

	if model.ConsistencyModel == nil {
		model.ConsistencyModel = Linearizability
	}
	// pass all the nodes to the consistency model and get back the consistency
	// model's adjacency list, i.e., system-specific order hints are not yet used
	adj, err := model.ConsistencyModel(d.ops)
	if err != nil {
		panic("failed to build dag using consistency model: " + err.Error())
	}

	// local variables: total incoming edges for each node. This will increment
	// when we "discover" new edges via order hints. This will decrement as we
	// create frontiers and move through the DAG
	localNDeps := make([]int, n)
	for node, dep := range adj {
		for other := range dep {
			// initialize outgoing edges of each node using the adjacency list
			d.depts[node.Id] = append(d.depts[node.Id], other.Id)
			// initialize counts of incoming edges. This will never be decremented
			// as we process the DAG
			d.nDeps[other.Id]++
			// initialize counts of incoming edges in a local variable. This *will* be
			// decremented as we process the DAG
			localNDeps[other.Id]++
		}
	}

	front := make(map[int]struct{})
	candidates := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if localNDeps[i] == 0 {
			// candidate set has nodes with no incoming edges
			candidates = append(candidates, i)
		}
	}

	for len(front) > 0 || len(candidates) > 0 {

		// pick operation from front, compare with front, update candidates
		if len(front) > 0 {
			var currId int
			for id := range front {
				currId = id
				break
			}
			currNode := d.ops[currId]

			// add likely edges
			for otherId := range front {
				if otherId == currId {
					continue
				}
				otherNode := d.ops[otherId]
				comp := d.comp(currNode.Hint, otherNode.Hint)

				if comp == SoftBefore || (comp == Unconstrained && currNode.Call < otherNode.Call) {
					d.lDepts[currId] = append(d.lDepts[currId], otherId)
					d.nLDeps[otherId]++
				} else if comp == SoftAfter || (comp == Unconstrained && currNode.Call > otherNode.Call) {
					d.lDepts[otherId] = append(d.lDepts[otherId], currId)
					d.nLDeps[currId]++
				}
			}
			delete(front, currId)

			// update candidates
			for _, childId := range d.depts[currId] {
				localNDeps[childId]--
				if localNDeps[childId] == 0 {
					candidates = append(candidates, childId)
				}
			}
		}

		// process candidates, update front
		if len(candidates) > 0 {
			for i := 0; i < len(candidates); i++ {
				for j := i + 1; j < len(candidates); j++ {
					id1, id2 := candidates[i], candidates[j]
					n1, n2 := d.ops[id1], d.ops[id2]

					compResult := d.comp(n1.Hint, n2.Hint)

					switch compResult {
					case HardBefore:
						d.depts[id1] = append(d.depts[id1], id2)
						d.nDeps[id2]++
						localNDeps[id2]++
					case HardAfter:
						d.depts[id2] = append(d.depts[id2], id1)
						d.nDeps[id1]++
						localNDeps[id1]++
					}
				}
			}
			for _, cId := range candidates {
				if localNDeps[cId] == 0 {
					front[cId] = struct{}{}
				}
			}
			candidates = candidates[:0]
		}
	}

	d.front = make(map[int]struct{})
	for i := 0; i < n; i++ {
		if d.nDeps[i] == 0 {
			d.front[i] = struct{}{}
		}
	}
}

func (d *dag) lift(id int) {
	delete(d.front, id)
	for _, dept := range d.depts[id] {
		d.nDeps[dept]--
		if d.nDeps[dept] == 0 {
			d.front[dept] = struct{}{}
		}
	}
	for _, dept := range d.lDepts[id] {
		d.nLDeps[dept]--
	}
}

func (d *dag) unlift(id int) {
	for _, dept := range d.depts[id] {
		if d.nDeps[dept] == 0 {
			delete(d.front, dept)
		}
		d.nDeps[dept]++
	}
	d.front[id] = struct{}{}
	for _, dept := range d.lDepts[id] {
		d.nLDeps[dept]++
	}
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

type stackEntry struct { // entry in the stack
	node  *Node       // node<>operation that was pushed on the stack
	state interface{} // state before this node was pushed on the stack
	front []int       // frontier after this node was pushed on the stack, i.e., it does not contain the node
}

func checkSingle(model Model, history []entry, computePartial bool, kill *int32) (bool, []*[]int) {
	d := dag{comp: model.Order}
	d.Init(history, model)
	// DAG is built. Now we need to find a valid topological order from this DAG

	n := len(d.ops)
	linearized := newBitset(uint(n))
	cache := make(map[uint64][]cacheEntry) // map from hash to cache entry
	var stack []stackEntry
	var front []int
	// longest linearizable prefix that includes the given entry. For debugging only
	longest := make([]*[]int, n)
	// get frontier from the DAG: list of nodes sorted by likely order
	front = d.getOrderedFront()

	state := model.Init()

	// d.front is the actual frontier of the DAG. front is the "remaining"
	// frontier that we still need to check.
	for len(d.front) > 0 {
		// kill process after timeout
		if atomic.LoadInt32(kill) != 0 {
			return false, longest
		}
		// actual frontier of the DAG is not empty, but nothing in the frontier can
		// be linearized
		if len(front) == 0 {
			if len(stack) == 0 {
				// since there is nothing left on the stack, we can't pop anything out
				// and populate the pending frontier. Give up.
				return false, longest
			}

			if computePartial {
				callsLen := len(stack)
				var seq []int = nil
				for _, v := range stack {
					if longest[v.node.Id] == nil || callsLen > len(*longest[v.node.Id]) {
						// create seq lazily
						if seq == nil {
							seq = make([]int, len(stack))
							for i, v := range stack {
								seq[i] = v.node.Id
							}
						}
						longest[v.node.Id] = &seq
					}
				}
			}

			// the front is empty, pop from the stack!
			stackTop := stack[len(stack)-1]
			state = stackTop.state                        // recover the current state
			linearized.clear(uint(stackTop.node.Id))      // clear from bitmap
			front = append([]int(nil), stackTop.front...) // recover the frontier from the stack. This avoids infinite looping
			stack = stack[:len(stack)-1]                  // pop from stack
			d.unlift(stackTop.node.Id)                    // put the node back in the DAG
			continue
		}

		// there is something in the pending frontier.
		opId := front[len(front)-1] // take the last operation, since it was sorted by likely orders
		op := d.ops[opId]
		front = front[:len(front)-1] // remove it from the pending frontier

		ok, newState := model.Step(state, op.Input, op.Output) // find new state
		if ok {
			// push only if it was allowed by the sequential specification
			newLinearized := linearized.clone().set(uint(op.Id)) // add to bit set
			newCacheEntry := cacheEntry{newLinearized, newState} //
			if !cacheContains(model, cache, newCacheEntry) {
				hash := newLinearized.hash()
				cache[hash] = append(cache[hash], newCacheEntry)
				stack = append(stack, stackEntry{op, state, append([]int{}, front...)}) // push to the stack
				d.lift(op.Id)                                                           // remove from the DAG. this will update the frontier of the DAG
				front = d.getOrderedFront()                                             // get the new pending frontier
				state = newState                                                        // update state
				linearized.set(uint(op.Id))
			} else {
				// skip checking this order because we already checked another
				// "equivalent" order
			}
		}
	}

	// longest linearization is the complete linearization, which is calls
	seq := make([]int, len(stack))
	for i, v := range stack {
		seq[i] = v.node.Id
	}
	for i := 0; i < n; i++ {
		longest[i] = &seq
	}
	return true, longest
}

func fillDefault(model Model) Model {
	if model.Partition == nil {
		model.Partition = noPartition
	}
	if model.PartitionEvent == nil {
		model.PartitionEvent = noPartitionEvent
	}
	if model.Equal == nil {
		model.Equal = shallowEqual
	}
	if model.DescribeOperation == nil {
		model.DescribeOperation = defaultDescribeOperation
	}
	if model.DescribeState == nil {
		model.DescribeState = defaultDescribeState
	}
	if model.DescribeOperationMetadata == nil {
		model.DescribeOperationMetadata = defaultDescribeOperationMetadata
	}
	return model
}

func checkParallel(model Model, history [][]entry, computeInfo bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	if len(history) == 0 {
		return Ok, LinearizationInfo{}
	}
	ok := true
	timedOut := false
	results := make(chan bool, len(history))
	longest := make([][]*[]int, len(history))
	kill := int32(0)
	for i, subhistory := range history {
		go func(i int, subhistory []entry) {
			ok, l := checkSingle(model, subhistory, computeInfo, &kill)
			longest[i] = l
			results <- ok
		}(i, subhistory)
	}
	var timeoutChan <-chan time.Time
	if timeout > 0 {
		timeoutChan = time.After(timeout)
	}
	count := 0
loop:
	for {
		select {
		case result := <-results:
			count++
			ok = ok && result
			if !ok && !computeInfo {
				atomic.StoreInt32(&kill, 1)
				break loop
			}
			if count >= len(history) {
				break loop
			}
		case <-timeoutChan:
			timedOut = true
			atomic.StoreInt32(&kill, 1)
			break loop // if we time out, we might get a false positive
		}
	}
	var info LinearizationInfo
	if computeInfo {
		// make sure we've waited for all goroutines to finish,
		// otherwise we might race on access to longest[]
		for count < len(history) {
			<-results
			count++
		}
		// return longest linearizable prefixes that include each history element
		partialLinearizations := make([][][]int, len(history))
		for i := 0; i < len(history); i++ {
			var partials [][]int
			// turn longest into a set of unique linearizations
			set := make(map[*[]int]struct{})
			for _, v := range longest[i] {
				if v != nil {
					set[v] = struct{}{}
				}
			}
			for k := range set {
				arr := make([]int, len(*k))
				copy(arr, *k)
				partials = append(partials, arr)
			}
			partialLinearizations[i] = partials
		}
		info.history = history
		info.partialLinearizations = partialLinearizations
	}
	var result CheckResult
	if !ok {
		result = Illegal
	} else {
		if timedOut {
			result = Unknown
		} else {
			result = Ok
		}
	}
	return result, info
}

func checkEvents(model Model, history []Event, verbose bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	model = fillDefault(model)
	partitions := model.PartitionEvent(history)
	l := make([][]entry, len(partitions))
	for i, subhistory := range partitions {
		l[i] = convertEntries(renumber(subhistory))
	}
	return checkParallel(model, l, verbose, timeout)
}

func checkOperations(model Model, history []Operation, verbose bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	model = fillDefault(model)
	partitions := model.Partition(history)
	l := make([][]entry, len(partitions))
	for i, subhistory := range partitions {
		l[i] = makeEntries(subhistory)
	}
	return checkParallel(model, l, verbose, timeout)
}
