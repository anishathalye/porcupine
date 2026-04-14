package porcupine

import (
	"sort"
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
	opKind   OperationKind
	metadata interface{}
	hint     interface{}
}

type entries []entry

type LinearizationInfo struct {
	history               []entries // for each partition, a list of entries
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

type byTime entries

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

func makeEntries(history []Operation, numClients int) (entries, OperationHistory) {
	var entries entries = nil
	id := 0
	operationHistory := make(OperationHistory, numClients)
	for _, elem := range history {
		entries = append(entries, entry{
			kind: callEntry, value: elem.Input, id: id, time: elem.Call,
			clientId: elem.ClientId, opKind: elem.OpKind, metadata: elem.Metadata,
		})
		entries = append(entries, entry{
			kind: returnEntry, value: elem.Output, id: id, time: elem.Return,
			clientId: elem.ClientId, opKind: elem.OpKind, metadata: elem.Metadata,
			hint: elem.OrderHint,
		})
		clientOp := newclientOperation(elem, numClients, id)
		operationHistory[elem.ClientId] = append(operationHistory[elem.ClientId], clientOp)
		id++
	}
	sort.Sort(byTime(entries))
	return entries, operationHistory
}

type node struct {
	value interface{}
	match *node // call if match is nil, otherwise return
	id    int
	next  *node
	prev  *node
}

func insertBefore(n *node, mark *node) *node {
	if mark != nil {
		beforeMark := mark.prev
		mark.prev = n
		n.next = mark
		if beforeMark != nil {
			n.prev = beforeMark
			beforeMark.next = n
		}
	}
	return n
}

func renumber(events []Event) []Event {
	var e []Event
	m := make(map[int]int) // renumbering
	id := 0
	for _, v := range events {
		if r, ok := m[v.Id]; ok {
			e = append(e, Event{ClientId: v.ClientId, Kind: v.Kind, Value: v.Value, Id: r, Metadata: v.Metadata, OpKind: v.OpKind, Hint: v.Hint})
		} else {
			e = append(e, Event{ClientId: v.ClientId, Kind: v.Kind, Value: v.Value, Id: id, Metadata: v.Metadata, OpKind: v.OpKind, Hint: v.Hint})
			m[v.Id] = id
			id++
		}
	}
	return e
}

func convertEntries(events []Event) (entries, int) {
	var entries entries
	maxClientId := 0
	for i, elem := range events {
		kind := callEntry
		if elem.Kind == ReturnEvent {
			kind = returnEntry
		}
		// use index as "time"
		entries = append(entries, entry{
			kind:     kind,
			value:    elem.Value,
			id:       elem.Id,
			time:     int64(i),
			clientId: elem.ClientId,
			opKind:   elem.OpKind,
			metadata: elem.Metadata,
			hint:     elem.Hint,
		})
		if elem.ClientId > maxClientId {
			maxClientId = elem.ClientId
		}
	}
	return entries, maxClientId + 1
}

func makeLinkedEntries(entries entries) *node {
	var root *node = nil
	match := make(map[int]*node)
	for i := len(entries) - 1; i >= 0; i-- {
		elem := entries[i]
		if elem.kind == returnEntry {
			entry := &node{value: elem.value, match: nil, id: elem.id}
			match[elem.id] = entry
			insertBefore(entry, root)
			root = entry
		} else {
			entry := &node{value: elem.value, match: match[elem.id], id: elem.id}
			insertBefore(entry, root)
			root = entry
		}
	}
	return root
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

type callsEntry struct {
	entry *node
	state interface{}
}

func lift(entry *node) {
	entry.prev.next = entry.next
	entry.next.prev = entry.prev
	match := entry.match
	match.prev.next = match.next
	if match.next != nil {
		match.next.prev = match.prev
	}
}

func unlift(entry *node) {
	match := entry.match
	match.prev.next = match
	if match.next != nil {
		match.next.prev = match
	}
	entry.prev.next = entry
	entry.next.prev = entry
}

// func checkSingle(model Model, history OperationHistory, computePartial bool, kill *int32) (bool, []*[]int) {
// 	entry := makeLinkedEntries(history)
// 	n := length(entry) / 2
// 	linearized := newBitset(uint(n))
// 	cache := make(map[uint64][]cacheEntry) // map from hash to cache entry
// 	var calls []callsEntry
// 	// longest linearizable prefix that includes the given entry
// 	longest := make([]*[]int, n)

// 	state := model.Init()
// 	headEntry := insertBefore(&node{value: nil, match: nil, id: -1}, entry)
// 	for headEntry.next != nil {
// 		if atomic.LoadInt32(kill) != 0 {
// 			return false, longest
// 		}
// 		if entry.match != nil {
// 			matching := entry.match // the return entry
// 			ok, newState := model.Step(state, entry.value, matching.value)
// 			if ok {
// 				newLinearized := linearized.clone().set(uint(entry.id))
// 				newCacheEntry := cacheEntry{newLinearized, newState}
// 				if !cacheContains(model, cache, newCacheEntry) {
// 					hash := newLinearized.hash()
// 					cache[hash] = append(cache[hash], newCacheEntry)
// 					calls = append(calls, callsEntry{entry, state})
// 					state = newState
// 					linearized.set(uint(entry.id))
// 					lift(entry)
// 					entry = headEntry.next
// 				} else {
// 					entry = entry.next
// 				}
// 			} else {
// 				entry = entry.next
// 			}
// 		} else {
// 			if len(calls) == 0 {
// 				return false, longest
// 			}
// 			// longest
// 			if computePartial {
// 				callsLen := len(calls)
// 				var seq []int = nil
// 				for _, v := range calls {
// 					if longest[v.entry.id] == nil || callsLen > len(*longest[v.entry.id]) {
// 						// create seq lazily
// 						if seq == nil {
// 							seq = make([]int, len(calls))
// 							for i, v := range calls {
// 								seq[i] = v.entry.id
// 							}
// 						}
// 						longest[v.entry.id] = &seq
// 					}
// 				}
// 			}
// 			callsTop := calls[len(calls)-1]
// 			entry = callsTop.entry
// 			state = callsTop.state
// 			linearized.clear(uint(entry.id))
// 			calls = calls[:len(calls)-1]
// 			unlift(entry)
// 			entry = entry.next
// 		}
// 	}
// 	// longest linearization is the complete linearization, which is calls
// 	seq := make([]int, len(calls))
// 	for i, v := range calls {
// 		seq[i] = v.entry.id
// 	}
// 	for i := 0; i < n; i++ {
// 		longest[i] = &seq
// 	}
// 	return true, longest
// }

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

func checkParallel(model Model, consistency Consistency, history []OperationHistory, entries []entries, computeInfo bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	if len(history) == 0 {
		return Ok, LinearizationInfo{}
	}
	ok := true
	timedOut := false
	results := make(chan bool, len(history))
	longest := make([][]*[]int, len(history))
	kill := int32(0)
	for i, subhistory := range history {
		go func(i int, subhistory OperationHistory) {
			ok, l := checkSingle(model, consistency, subhistory, computeInfo, &kill)
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
		info.history = entries
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

func checkEvents(model Model, consistency Consistency, history []Event, verbose bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	model = fillDefault(model)
	partitions := model.PartitionEvent(history)
	l := make([]entries, len(partitions))
	operationHistories := make([]OperationHistory, 0, len(partitions))
	var numClients int
	for i, subhistory := range partitions {
		l[i], numClients = convertEntries(renumber(subhistory))
		operationHistory := make([][]ClientOperation, 0)
		clientOperations := make(map[int][]ClientOperation)
		maxClientId := 0
		for j, ev := range l[i] {
			if ev.kind == false {
				op := Operation{
					ClientId: ev.clientId,
					OpKind:   ev.opKind,
					Input:    ev.value,
					Call:     int64(j),
				}
				clientOperations[ev.clientId] = append(clientOperations[ev.clientId], newclientOperation(op, numClients, ev.id))
			} else {
				call := clientOperations[ev.clientId][len(clientOperations[ev.clientId])-1]
				call.Op.Output = ev.value
				call.Op.Return = int64(j)
				call.Op.OrderHint = ev.hint
				clientOperations[ev.clientId][len(clientOperations[ev.clientId])-1] = call
			}
			if ev.clientId > maxClientId {
				maxClientId = ev.clientId
			}
		}

		for i := 0; i <= maxClientId; i++ {
			operationHistory = append(operationHistory, clientOperations[i])
		}
		operationHistories = append(operationHistories, operationHistory)
	}

	for i := range operationHistories {
		consistency.Preprocess(operationHistories[i])
	}

	return checkParallel(model, consistency, operationHistories, l, verbose, timeout)
}

func checkOperations(model Model, consistency Consistency, history []Operation, verbose bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	model = fillDefault(model)
	maxClientId := 0
	for _, op := range history {
		if op.ClientId > maxClientId {
			maxClientId = op.ClientId
		}
	}
	partitions := model.Partition(history)
	l := make([]entries, len(partitions))
	operationHistories := make([]OperationHistory, len(partitions))

	for i, subhistory := range partitions {
		l[i], operationHistories[i] = makeEntries(subhistory, maxClientId+1)
	}
	for i := range operationHistories {
		consistency.Preprocess(operationHistories[i])
	}
	return checkParallel(model, consistency, operationHistories, l, verbose, timeout)
}
