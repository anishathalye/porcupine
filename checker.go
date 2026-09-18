package porcupine

import (
	"context"
	"sort"
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
	history               []operationHistory // for each partition, a list of client operations
	partialLinearizations [][][]int          // for each partition, a set of histories (list of ids)
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
	for p, clientOps := range li.history {
		// build a map from global operation id to Operation
		opMap := make(map[int]Operation)
		for _, cltOps := range clientOps {
			for _, cltOp := range cltOps {
				opMap[cltOp.globalId] = cltOp.Op
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

func makeEntries(history []Operation, numClients int) (entries, operationHistory) {
	var entries entries = nil
	id := 0
	operationHistory := make(operationHistory, numClients)
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

type cacheEntry struct {
	linearized bitset
	state      interface{}
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
	switch {
	case model.Step == nil && model.StepContext == nil:
		panic("model must define Step or StepContext")
	case model.Step == nil:
		ctx := context.Background()
		model.Step = func(state, input, output interface{}) (bool, interface{}) {
			return model.StepContext(ctx, state, input, output)
		}
	case model.StepContext == nil:
		model.StepContext = func(ctx context.Context, state interface{}, input interface{}, output interface{}) (bool, interface{}) {
			return model.Step(state, input, output)
		}
	}
	return model
}

func checkParallel(model Model, consistency Consistency, history []operationHistory, entries []entries, computeInfo bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	if len(history) == 0 {
		return Ok, LinearizationInfo{}
	}
	ok := true
	timedOut := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan bool, len(history))
	longest := make([][]*[]int, len(history))
	for i, subhistory := range history {
		go func(i int, subhistory operationHistory) {
			ok, l := checkSingle(ctx, model, consistency, subhistory, computeInfo)
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
				cancel()
				break loop
			}
			if count >= len(history) {
				break loop
			}
		case <-timeoutChan:
			timedOut = true
			cancel()
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

func checkEvents(model Model, consistency Consistency, history []Event, verbose bool, timeout time.Duration) (CheckResult, LinearizationInfo) {
	model = fillDefault(model)
	partitions := model.PartitionEvent(history)
	l := make([]entries, len(partitions))
	operationHistories := make([]operationHistory, 0, len(partitions))
	var numClients int
	for i, subhistory := range partitions {
		l[i], numClients = convertEntries(renumber(subhistory))
		operationHistory := make([][]clientOperation, 0)
		clientOperations := make(map[int][]clientOperation)
		maxClientId := 0
		for j, ev := range l[i] {
			if !ev.kind { // call
				op := Operation{
					ClientId: ev.clientId,
					OpKind:   ev.opKind,
					Input:    ev.value,
					Call:     int64(j),
					Metadata: ev.metadata,
				}
				clientOperations[ev.clientId] = append(clientOperations[ev.clientId], newclientOperation(op, numClients, ev.id))
			} else { //return
				call := clientOperations[ev.clientId][len(clientOperations[ev.clientId])-1]
				call.Op.Output = ev.value
				call.Op.Return = int64(j)
				call.Op.OrderHint = ev.hint
				if ev.metadata != nil {
					call.Op.Metadata = ev.metadata
				}
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
	operationHistories := make([]operationHistory, len(partitions))

	for i, subhistory := range partitions {
		l[i], operationHistories[i] = makeEntries(subhistory, maxClientId+1)
	}
	return checkParallel(model, consistency, operationHistories, l, verbose, timeout)
}
