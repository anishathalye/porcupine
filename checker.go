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
		if entry.linearized.equals(elem.linearized) && model.Equal(entry.state, elem.state) {
			return true
		}
	}
	return false
}

type node struct {
	id        int
	input     interface{}
	output    interface{}
	hint      interface{}
	startTime int64
}

type comparator func(a, b interface{}) PrecKind
type dag struct {
	ops    []*node // map from id to op
	depts  [][]int
	lDepts [][]int
	nDeps  []int
	nLDeps []int
	front  map[int]struct{}
	comp   comparator
}

func setToSlice(set map[*node]struct{}) []int {
	s := make([]int, 0, len(set))
	for k := range set {
		s = append(s, k.id)
	}
	return s
}

func (d *dag) Init(history []entry) {
	if d.comp == nil {
		d.comp = func(a, b interface{}) PrecKind { return Unconstrained }
	}
	n := len(history) / 2
	d.ops = make([]*node, n)
	depts := make([]map[*node]struct{}, n)
	d.depts = make([][]int, n)
	lDepts := make([]map[*node]struct{}, n)
	d.lDepts = make([][]int, n)
	d.nDeps = make([]int, n)
	d.nLDeps = make([]int, n)
	d.front = make(map[int]struct{})

	concurrent := make(map[int]*node)
	rets := make(map[int]struct{})
	for _, elem := range history {
		if elem.kind == callEntry {
			depts[elem.id] = make(map[*node]struct{})
			lDepts[elem.id] = make(map[*node]struct{})
			n := node{id: elem.id, input: elem.value, hint: elem.hint, startTime: elem.time}
			d.ops[elem.id] = &n
			concurrent[elem.id] = &n
			for ret := range rets {
				depts[ret][&n] = struct{}{}
				d.nDeps[elem.id]++
			}
		} else {
			op := d.ops[elem.id]
			op.output = elem.value
			delete(concurrent, elem.id)
			for _, other := range concurrent {
				switch d.comp(other.hint, elem.hint) {
				case HappensAfter:
					depts[op.id][other] = struct{}{}
					d.nDeps[other.id]++
				case HappensBefore:
					depts[other.id][op] = struct{}{}
					d.nDeps[op.id]++
				case LikelyAfter:
					lDepts[op.id][other] = struct{}{}
					d.nLDeps[other.id]++
				case LikelyBefore:
					lDepts[other.id][op] = struct{}{}
					d.nLDeps[op.id]++
				default:
					if op.startTime < other.startTime {
						lDepts[op.id][other] = struct{}{}
						d.nLDeps[other.id]++
					} else {
						lDepts[other.id][op] = struct{}{}
						d.nLDeps[op.id]++
					}
				}
			}
			for ret := range rets {
				if _, ok := depts[ret][op]; ok {
					delete(rets, ret)
				}
			}
			rets[op.id] = struct{}{}
		}
	}
	for i := 0; i < n; i++ {
		d.depts[i] = setToSlice(depts[i])
		d.lDepts[i] = setToSlice(lDepts[i])
	}
	for _, elem := range history {
		if elem.kind == callEntry {
			if d.nDeps[elem.id] == 0 {
				d.front[elem.id] = struct{}{}
			}
		} else {
			break
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

type stackEntry struct {
	node  *node
	state interface{}
	front []int
}

func checkSingle(model Model, history []entry, computePartial bool, kill *int32) (bool, []*[]int) {
	d := dag{comp: model.Prec}
	d.Init(history)
	n := len(d.ops)
	linearized := newBitset(uint(n))
	cache := make(map[uint64][]cacheEntry) // map from hash to cache entry
	var stack []stackEntry
	var front []int
	// longest linearizable prefix that includes the given entry
	longest := make([]*[]int, n)
	front = d.getOrderedFront()

	state := model.Init()

	for len(d.front) > 0 {
		if atomic.LoadInt32(kill) != 0 {
			return false, longest
		}
		if len(front) == 0 {
			if len(stack) == 0 {
				return false, longest
			}

			if computePartial {
				callsLen := len(stack)
				var seq []int = nil
				for _, v := range stack {
					if longest[v.node.id] == nil || callsLen > len(*longest[v.node.id]) {
						// create seq lazily
						if seq == nil {
							seq = make([]int, len(stack))
							for i, v := range stack {
								seq[i] = v.node.id
							}
						}
						longest[v.node.id] = &seq
					}
				}
			}

			stackTop := stack[len(stack)-1]
			state = stackTop.state
			linearized.clear(uint(stackTop.node.id))
			front = append([]int(nil), stackTop.front...)
			stack = stack[:len(stack)-1]
			d.unlift(stackTop.node.id)
			continue
		}
		opId := front[len(front)-1]
		op := d.ops[opId]
		front = front[:len(front)-1]

		ok, newState := model.Step(state, op.input, op.output)
		if ok {
			newLinearized := linearized.clone().set(uint(op.id))
			newCacheEntry := cacheEntry{newLinearized, newState}
			if !cacheContains(model, cache, newCacheEntry) {
				hash := newLinearized.hash()
				cache[hash] = append(cache[hash], newCacheEntry)
				stack = append(stack, stackEntry{op, state, append([]int{}, front...)})
				d.lift(op.id)
				front = d.getOrderedFront()
				state = newState
				linearized.set(uint(op.id))
			}
		}
	}

	// longest linearization is the complete linearization, which is calls
	seq := make([]int, len(stack))
	for i, v := range stack {
		seq[i] = v.node.id
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
