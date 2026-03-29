package porcupine

import "sort"

/* This gets a list of nodes where every node contains all the details about an
 * operation. It returns an adjacency list. */
func Linearizability(nodes []*Node) (map[*Node]map[*Node]struct{}, error) {
	// Nodes are unsorted. So we create call and return entries to sort them
	type entry struct {
		kind entryKind // true: call, false: return
		time int64
		node *Node
	}
	var entries []entry

	// just create call-return entries
	for _, n := range nodes {
		entries = append(entries, entry{callEntry, n.Call, n}, entry{returnEntry, n.Ret, n})
	}
	// sort entries by time
	sort.Slice(entries, func(i, j int) bool {
		n1, n2 := entries[i], entries[j]
		return n1.time < n2.time
	})
	// initialize an empty adjacency list which will be returned by this method
	edges := make(map[*Node]map[*Node]struct{})
	// "frontier" of returns
	// Consider this history
	// c0, r0, c1, c2, r2, r1, c3, r3
	// c3 should get connected to r1 and r2.
	// r1 and r2 will be in rets when we get to c3; r0 will not be there
	rets := make(map[*Node]struct{})
	for _, entry := range entries {
		if entry.kind == callEntry {
			// say this is c3 from above history
			edges[entry.node] = make(map[*Node]struct{})
			for ret := range rets {
				// add r1, r2 for c3
				edges[ret][entry.node] = struct{}{}
			}
		} else {
			// say this is r1
			for ret := range rets {
				if _, ok := edges[ret][entry.node]; ok {
					// delete r0 since c1->r0 is an edge
					// but maintain r2 in rets since c1->r2 is not an edge
					delete(rets, ret)
				}
			}
			// add r1 to rets
			rets[entry.node] = struct{}{}
		}
	}
	return edges, nil
}
