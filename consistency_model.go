package porcupine

import "sort"

func Linearizability(nodes []*Node) (map[*Node]map[*Node]struct{}, error) {
	type entry struct {
		kind entryKind
		time int64
		node *Node
	}
	var entries []entry
	for _, n := range nodes {
		entries = append(entries, entry{callEntry, n.Call, n}, entry{returnEntry, n.Ret, n})
	}
	sort.Slice(entries, func(i, j int) bool {
		n1, n2 := entries[i], entries[j]
		return n1.time < n2.time
	})
	edges := make(map[*Node]map[*Node]struct{})
	rets := make(map[*Node]struct{})
	for _, entry := range entries {
		if entry.kind == callEntry {
			edges[entry.node] = make(map[*Node]struct{})
			for ret := range rets {
				edges[ret][entry.node] = struct{}{}
			}
		} else {
			for ret := range rets {
				if _, ok := edges[ret][entry.node]; ok {
					delete(rets, ret)
				}
			}
			rets[entry.node] = struct{}{}
		}
	}
	return edges, nil
}
