package porcupine

import "errors"

// OrderKind represents the kind of precedence relationship between two operations
type OrderKind int

const (
	// Indicates that event a should be strictly ordered before event b.
	HardBefore OrderKind = -2
	// Indicates that event a should likely be ordered before event b.
	SoftBefore OrderKind = -1
	// Indicates that there is no information about the relative order of operations a and b.
	Unconstrained OrderKind = 0
	// Indicates that event a should likely be ordered after event b.
	SoftAfter OrderKind = 1
	// Indicates that event a should be strictly ordered after event b.
	HardAfter OrderKind = 2
)

type Oracle func(a *Operation, b *Operation) (OrderKind, error)

type Consistency struct {
	// e.g., linearizability; etcd revision number, etc
	Oracles []Oracle
}

func (c *Consistency) Check(a *Operation, b *Operation) (OrderKind, error) {
	ok := Unconstrained
	for _, o := range c.Oracles {
		order, err := o(a, b)
		if err != nil {
			return ok, err
		}
		switch order {
		case HardAfter:
			if ok == HardBefore {
				return ok, errors.New("conflicting order constraints")
			}
			ok = HardAfter
		case HardBefore:
			if ok == HardAfter {
				return ok, errors.New("conflicting order constraints")
			}
			ok = HardBefore
		case SoftAfter:
			if ok == Unconstrained {
				ok = SoftAfter
			}
		case SoftBefore:
			if ok == Unconstrained {
				ok = SoftBefore
			}
		case Unconstrained:
			// nothing to do
		}
	}
	return ok, nil
}

func GeneralLikely(a *Operation, b *Operation) (OrderKind, error) {
	if a.Call < b.Call {
		return SoftBefore, nil
	}
	if a.Call > b.Call {
		return SoftAfter, nil
	}
	return Unconstrained, nil
}

func RealTime(a *Operation, b *Operation) (OrderKind, error) {
	if a.Return < b.Call {
		return HardBefore, nil
	}
	if a.Call > b.Return {
		return HardAfter, nil
	}
	return Unconstrained, nil
}

var LinearizabilityOracles = []Oracle{RealTime, GeneralLikely}

func RealTimeWrites(a *Operation, b *Operation) (OrderKind, error) {
	if a.OpKind == Write && b.OpKind == Write {
		return RealTime(a, b)
	}
	return Unconstrained, nil
}

var OrderedSequentialConsistencyOracles = []Oracle{RealTimeWrites, GeneralLikely}

/* This gets a list of nodes where every node contains all the details about an
 * operation. It returns an adjacency list. */
// func Linearizability(nodes []*Node) (map[*Node]map[*Node]struct{}, error) {
// 	// Nodes are unsorted. So we create call and return entries to sort them
// 	type entry struct {
// 		kind entryKind // true: call, false: return
// 		time int64
// 		node *Node
// 	}
// 	var entries []entry

// 	// just create call-return entries
// 	for _, n := range nodes {
// 		entries = append(entries, entry{callEntry, n.Call, n}, entry{returnEntry, n.Ret, n})
// 	}
// 	// sort entries by time
// 	sort.Slice(entries, func(i, j int) bool {
// 		n1, n2 := entries[i], entries[j]
// 		return n1.time < n2.time
// 	})
// 	// initialize an empty adjacency list which will be returned by this method
// 	edges := make(map[*Node]map[*Node]struct{})
// 	// "frontier" of returns
// 	// Consider this history
// 	// c0, r0, c1, c2, r2, r1, c3, r3
// 	// c3 should get connected to r1 and r2.
// 	// r1 and r2 will be in rets when we get to c3; r0 will not be there
// 	rets := make(map[*Node]struct{})
// 	for _, entry := range entries {
// 		if entry.kind == callEntry {
// 			// say this is c3 from above history
// 			edges[entry.node] = make(map[*Node]struct{})
// 			for ret := range rets {
// 				// add r1, r2 for c3
// 				edges[ret][entry.node] = struct{}{}
// 			}
// 		} else {
// 			// say this is r1
// 			for ret := range rets {
// 				if _, ok := edges[ret][entry.node]; ok {
// 					// delete r0 since c1->r0 is an edge
// 					// but maintain r2 in rets since c1->r2 is not an edge
// 					delete(rets, ret)
// 				}
// 			}
// 			// add r1 to rets
// 			rets[entry.node] = struct{}{}
// 		}
// 	}
// 	return edges, nil
// }
