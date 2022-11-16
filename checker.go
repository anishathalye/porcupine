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
	value    any
	id       int
	time     int64
	clientId int
}

func entryValueAsInput[I any](e entry) I {
	return e.value.(I)
}

func entryValueAsOutput[O any](e entry) O {
	return e.value.(O)
}

type linearizationInfo struct {
	history               [][]entry // for each partition, a list of entries
	partialLinearizations [][][]int // for each partition, a set of histories (list of ids)
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

func makeEntries[I any, O any](history []Operation[I, O]) []entry {
	var entries []entry = nil
	id := 0
	for _, elem := range history {
		entries = append(entries, entry{
			callEntry, elem.Input, id, elem.Call, elem.ClientId})
		entries = append(entries, entry{
			returnEntry, elem.Output, id, elem.Return, elem.ClientId})
		id++
	}
	sort.Sort(byTime(entries))
	return entries
}

type node struct {
	value any
	match *node // call if match is nil, otherwise return
	id    int
	next  *node
	prev  *node
}

func nodeValueAsInput[I any](e *node) I {
	return e.value.(I)
}

func nodeValueAsOutput[O any](e *node) O {
	return e.value.(O)
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

func length(n *node) int {
	l := 0
	for n != nil {
		n = n.next
		l++
	}
	return l
}

func renumber[I any, O any](events []Event[I, O]) []Event[I, O] {
	var e []Event[I, O]
	m := make(map[int]int) // renumbering
	id := 0
	for _, v := range events {
		if r, ok := m[v.Id]; ok {
			e = append(e, Event[I, O]{v.ClientId, v.Kind, v.Value, r})
		} else {
			e = append(e, Event[I, O]{v.ClientId, v.Kind, v.Value, id})
			m[v.Id] = id
			id++
		}
	}
	return e
}

func convertEntries[I any, O any](events []Event[I, O]) []entry {
	var entries []entry
	for i, elem := range events {
		kind := callEntry
		if elem.Kind == ReturnEvent {
			kind = returnEntry
		}
		// use index as "time"
		entries = append(entries, entry{kind, elem.Value, elem.Id, int64(i), elem.ClientId})
	}
	return entries
}

func makeLinkedEntries(entries []entry) *node {
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

type cacheEntry[S State[S]] struct {
	linearized bitset
	state      S
}

func cacheContains[S State[S]](cache map[uint64][]cacheEntry[S], entry cacheEntry[S]) bool {
	for _, elem := range cache[entry.linearized.hash()] {
		if entry.linearized.equals(elem.linearized) && entry.state.Equals(elem.state) {
			return true
		}
	}
	return false
}

type callsEntry[S State[S]] struct {
	entry *node
	state S
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

func checkSingle[S State[S], I any, O any](model Model[S, I, O], history []entry, computePartial bool, kill *int32) (bool, []*[]int) {
	entry := makeLinkedEntries(history)
	n := length(entry) / 2
	linearized := newBitset(uint(n))
	cache := make(map[uint64][]cacheEntry[S]) // map from hash to cache entry
	var calls []callsEntry[S]
	// longest linearizable prefix that includes the given entry
	longest := make([]*[]int, n)

	state := model.Init()
	headEntry := insertBefore(&node{value: nil, match: nil, id: -1}, entry)
	for headEntry.next != nil {
		if atomic.LoadInt32(kill) != 0 {
			return false, longest
		}
		if entry.match != nil {
			matching := entry.match // the return entry
			ok, newState := model.Step(state.Clone(), nodeValueAsInput[I](entry), nodeValueAsOutput[O](matching))
			if ok {
				newLinearized := linearized.clone().set(uint(entry.id))
				newCacheEntry := cacheEntry[S]{newLinearized, newState}
				if !cacheContains(cache, newCacheEntry) {
					hash := newLinearized.hash()
					cache[hash] = append(cache[hash], newCacheEntry)
					calls = append(calls, callsEntry[S]{entry, state})
					state = newState
					linearized.set(uint(entry.id))
					lift(entry)
					entry = headEntry.next
				} else {
					entry = entry.next
				}
			} else {
				entry = entry.next
			}
		} else {
			if len(calls) == 0 {
				return false, longest
			}
			// longest
			if computePartial {
				callsLen := len(calls)
				var seq []int = nil
				for _, v := range calls {
					if longest[v.entry.id] == nil || callsLen > len(*longest[v.entry.id]) {
						// create seq lazily
						if seq == nil {
							seq = make([]int, len(calls))
							for i, v := range calls {
								seq[i] = v.entry.id
							}
						}
						longest[v.entry.id] = &seq
					}
				}
			}
			callsTop := calls[len(calls)-1]
			entry = callsTop.entry
			state = callsTop.state
			linearized.clear(uint(entry.id))
			calls = calls[:len(calls)-1]
			unlift(entry)
			entry = entry.next
		}
	}
	// longest linearization is the complete linearization, which is calls
	seq := make([]int, len(calls))
	for i, v := range calls {
		seq[i] = v.entry.id
	}
	for i := 0; i < n; i++ {
		longest[i] = &seq
	}
	return true, longest
}

func fillDefault[S State[S], I any, O any](model Model[S, I, O]) Model[S, I, O] {
	if model.Partition == nil {
		model.Partition = noPartition[I, O]
	}
	if model.PartitionEvent == nil {
		model.PartitionEvent = noPartitionEvent[I, O]
	}
	if model.DescribeOperation == nil {
		model.DescribeOperation = defaultDescribeOperation[I, O]
	}
	return model
}

func checkParallel[S State[S], I any, O any](model Model[S, I, O], history [][]entry, computeInfo bool, timeout time.Duration) (CheckResult, linearizationInfo) {
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
	var info linearizationInfo
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

func checkEvents[S State[S], I any, O any](model Model[S, I, O], history []Event[I, O], verbose bool, timeout time.Duration) (CheckResult, linearizationInfo) {
	model = fillDefault(model)
	partitions := model.PartitionEvent(history)
	l := make([][]entry, len(partitions))
	for i, subhistory := range partitions {
		l[i] = convertEntries(renumber(subhistory))
	}
	return checkParallel(model, l, verbose, timeout)
}

func checkOperations[S State[S], I any, O any](model Model[S, I, O], history []Operation[I, O], verbose bool, timeout time.Duration) (CheckResult, linearizationInfo) {
	model = fillDefault(model)
	partitions := model.Partition(history)
	l := make([][]entry, len(partitions))
	for i, subhistory := range partitions {
		l[i] = makeEntries(subhistory)
	}
	return checkParallel(model, l, verbose, timeout)
}
