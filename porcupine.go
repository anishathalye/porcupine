package porcupine

import (
	"fmt"
	"reflect"
	"sort"
)

type entryKind bool

const (
	callEntry   entryKind = false
	returnEntry           = true
)

type entry struct {
	kind  entryKind
	value interface{}
	id    int
	time  int64
}

type byTime []entry

func (a byTime) Len() int {
	return len(a)
}

func (a byTime) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byTime) Less(i, j int) bool {
	return a[i].time < a[j].time
}

func makeEntries(history []Operation) []entry {
	var entries []entry = nil
	id := 0
	for _, elem := range history {
		entries = append(entries, entry{
			callEntry, elem.Input, id, elem.Call})
		entries = append(entries, entry{
			returnEntry, elem.Output, id, elem.Return})
		id++
	}
	sort.Sort(byTime(entries))
	return entries
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

func length(n *node) int {
	l := 0
	for n != nil {
		n = n.next
		l++
	}
	return l
}

func convertEntries(events []Event) []entry {
	var entries []entry
	for _, elem := range events {
		kind := callEntry
		if elem.Kind == ReturnEvent {
			kind = returnEntry
		}
		entries = append(entries, entry{kind, elem.Value, elem.Id, -1})
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

type cacheEntry struct {
	linearized map[int]bool
	state      interface{}
}

func copyBitset(bitset map[int]bool) map[int]bool {
	copied := make(map[int]bool)
	for k, v := range bitset {
		copied[k] = v
	}
	return copied
}

func cacheContains(cache []cacheEntry, entry cacheEntry) bool {
	for _, elem := range cache {
		if reflect.DeepEqual(elem, entry) {
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

func printList(n *node) {
	fmt.Print("[")
	for n != nil {
		fmt.Printf("%v, ", *n)
		n = n.next
	}
	fmt.Println("]")
}

func checkSingle(model Model, subhistory *node) bool {
	n := length(subhistory) / 2
	// TODO use a proper bitset for linearized
	linearized := make(map[int]bool)
	for i := 0; i < n; i++ {
		linearized[i] = false
	}
	// TODO use a better data structure than a list to avoid linear search
	var cache []cacheEntry
	var calls []callsEntry

	state := model.Init()
	headEntry := insertBefore(&node{value: nil, match: nil, id: -1}, subhistory)
	entry := subhistory
	for headEntry.next != nil {
		if entry.match != nil {
			matching := entry.match // the return entry
			ok, newState := model.Step(state, entry.value, matching.value)
			if ok {
				newLinearized := copyBitset(linearized)
				newLinearized[entry.id] = true
				newCacheEntry := cacheEntry{newLinearized, newState}
				if !cacheContains(cache, newCacheEntry) {
					cache = append(cache, newCacheEntry)
					calls = append(calls, callsEntry{entry, state})
					state = newState
					linearized[entry.id] = true
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
				return false
			}
			callsTop := calls[len(calls)-1]
			entry = callsTop.entry
			state = callsTop.state
			linearized[entry.id] = false
			calls = calls[:len(calls)-1]
			unlift(entry)
			entry = entry.next
		}
	}
	return true
}

func CheckOperations(model Model, history []Operation) bool {
	partitions := model.Partition(history)
	ok := true
	results := make(chan bool)
	for _, subhistory := range partitions {
		l := makeLinkedEntries(makeEntries(subhistory))
		go func() {
			results <- checkSingle(model, l)
		}()
	}
	for range partitions {
		result := <-results
		ok = ok && result
	}
	return ok
}

func CheckEvents(model Model, history []Event) bool {
	partitions := model.PartitionEvent(history)
	ok := true
	results := make(chan bool)
	for _, subhistory := range partitions {
		l := makeLinkedEntries(convertEntries(subhistory))
		go func() {
			results <- checkSingle(model, l)
		}()
	}
	for range partitions {
		result := <-results
		ok = ok && result
	}
	return ok
}
