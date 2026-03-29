package zookeeper

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"
	"olympos.io/encoding/edn"
)

const numKeys = 10

var vizDir = flag.String("vizDir", "", "")
var logFile = flag.String("logFile", "./zookeeper_clock-scrambler_RR0.33_CLI25.edn", "")

type Input struct {
	Op    string
	Value int
	Id    int
}

type Output struct {
	Value int
	Zxid  int
}

type State int

type Zxid int

type ZookeeperInvokeEntry struct {
	Type    edn.Keyword `edn:"type"`
	F       edn.Keyword `edn:"f"`
	Process interface{} `edn:"process"`
	Value   interface{} `edn:"value"`
}

type ZookeeperReturnEntry struct {
	Type    edn.Keyword `edn:"type"`
	F       edn.Keyword `edn:"f"`
	Process interface{} `edn:"process"`
	Value   interface{} `edn:"value"`
	Cxid    int         `edn:"czxid"`
	Mxid    int         `edn:"mzxid"`
}

func parseLogFile(path string) ([]porcupine.Event, error) {
	var events []porcupine.Event

	pendingCalls := make(map[int]ZookeeperInvokeEntry)
	pendingCallIDs := make(map[int]int)
	pendindCallIdxs := make(map[int]int)
	globalEventID := 0
	var i int = 0

	err := ParseEDNLog(path, func(dec *edn.Decoder) error {
		for {
			var rawEntry ZookeeperReturnEntry
			if err := dec.Decode(&rawEntry); err != nil {
				if err.Error() == "EOF" {
					break
				}
				return err
			}

			opRaw := strings.TrimPrefix(string(rawEntry.F), ":")
			opType := "Read"
			if opRaw == "write" {
				opType = "Write"
			}

			status := strings.TrimPrefix(string(rawEntry.Type), ":")
			procID, isClient := ToInt(rawEntry.Process)

			if !isClient {
				continue
			}
			if opRaw != "read" && opRaw != "write" {
				continue
			}

			i = i + 1

			if status == "invoke" {
				invokeEntry := ZookeeperInvokeEntry{
					Type:    rawEntry.Type,
					F:       rawEntry.F,
					Process: rawEntry.Process,
					Value:   rawEntry.Value,
				}

				pendingCalls[procID] = invokeEntry
				pendingCallIDs[procID] = globalEventID
				pendindCallIdxs[procID] = i

				valInt := 0
				if opType == "Write" {
					valInt, _ = ToInt(invokeEntry.Value)
				}

				events = append(events, porcupine.Event{
					Kind: porcupine.CallEvent,
					Value: Input{
						Op:    opType,
						Value: valInt,
						Id:    globalEventID,
					},
					Id:       globalEventID,
					ClientId: procID,
				})
				globalEventID++
				continue
			}
			if status == "ok" || status == "fail" {
				if _, exists := pendingCalls[procID]; !exists {
					continue
				}

				callID := pendingCallIDs[procID]
				callIdx := pendindCallIdxs[procID]
				delete(pendingCalls, procID)
				delete(pendingCallIDs, procID)
				delete(pendindCallIdxs, procID)

				valInt := 0
				if opType == "Read" {
					valInt, _ = ToInt(rawEntry.Value)
				}
				zxid := 0
				switch opType {
				case "Read":
					zxid = rawEntry.Mxid
				case "Write":
					zxid = rawEntry.Mxid
				}

				events = append(events, porcupine.Event{
					Kind: porcupine.ReturnEvent,
					Value: Output{
						Value: valInt,
						Zxid:  zxid,
					},
					Id:       callID,
					ClientId: procID,
					Hint:     zxid,
				})
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return events, nil
}

// func orderedSequentialConsistency(nodes []*porcupine.Node) (map[*porcupine.Node]map[*porcupine.Node]struct{}, error) {
// 	type entry struct {
// 		kind porcupine.EventKind // call and return
// 		time int64
// 		node *porcupine.Node
// 	}
// 	var entries []entry
// 	for _, n := range nodes {
// 		entries = append(entries, entry{porcupine.CallEvent, n.Call, n}, entry{porcupine.ReturnEvent, n.Ret, n})
// 	}
// 	// sort entries by time
// 	sort.Slice(entries, func(i, j int) bool {
// 		n1, n2 := entries[i], entries[j]
// 		return n1.time < n2.time
// 	})
// 	edges := make(map[*porcupine.Node]map[*porcupine.Node]struct{})
// 	maximalWrites := make(map[*porcupine.Node]struct{})
// 	maximumClientOp := make(map[int]*porcupine.Node)

// 	for _, entry := range entries {
// 		hint := entry.node.Hint.(Hint)
// 		if entry.kind == porcupine.CallEvent {
// 			edges[entry.node] = make(map[*porcupine.Node]struct{})
// 			if _, ok := edges[maximumClientOp[entry.node.ClientId]]; ok {
// 				edges[maximumClientOp[entry.node.ClientId]][entry.node] = struct{}{}
// 			}
// 			if hint.kind == false { // Write
// 				for write := range maximalWrites {
// 					edges[write][entry.node] = struct{}{}
// 				}
// 			}
// 		} else {
// 			maximumClientOp[entry.node.ClientId] = entry.node
// 			if hint.kind == false { // Write
// 				for write := range maximalWrites {
// 					if _, ok := edges[write][entry.node]; ok {
// 						delete(maximalWrites, write)
// 					}
// 				}
// 				maximalWrites[entry.node] = struct{}{}
// 			}
// 		}
// 	}
// 	return edges, nil
// }

func ZkOracle(a *porcupine.Node, b *porcupine.Node) (porcupine.OrderKind, error) {
	a_zxid, b_zxid := a.Hint.(Zxid), b.Hint.(Zxid)
	if a_zxid < b_zxid {
		return porcupine.HardBefore, nil
	}
	if a_zxid > b_zxid {
		return porcupine.HardAfter, nil
	}
	// same zxid
	if a.OpKind == porcupine.Write && b.OpKind == porcupine.Read {
		return porcupine.HardBefore, nil
	}
	if a.OpKind == porcupine.Read && b.OpKind == porcupine.Write {
		return porcupine.HardAfter, nil
	}
	if a.OpKind == porcupine.Write && b.OpKind == porcupine.Write {
		return porcupine.Unconstrained, errors.New("Two writes have same zxid")
	}
	// two reads with same zxid
	return porcupine.Unconstrained, nil
}

var ZkOracles []porcupine.Oracle = append(porcupine.OrderedSequentialConsistencyOracles, ZkOracle)

func TestZookeeper(t *testing.T) {

	events, err := parseLogFile(*logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Parsed %d events\n", len(events))

	initState := make([]int, numKeys)
	for i := range initState {
		initState[i] = 0
	}

	model := porcupine.Model{
		Init: func() interface{} { return State(0) },
		Step: func(state, input, output interface{}) (bool, interface{}) {
			s := state.(State)
			in := input.(Input)

			switch in.Op {
			case "Write":
				return true, State(in.Value)

			case "Read":
				if output == nil {
					return true, s
				}
				out := output.(Output)
				return int(s) == out.Value, s
			}
			return false, s
		},
		DescribeOperation: func(input, output interface{}) string {
			in := input.(Input)
			out := output.(Output)
			desc := in.Op + " id=" + strconv.Itoa(in.Id)
			if in.Op == "Write" {
				desc += " val=" + strconv.Itoa(in.Value)
			}
			if in.Op == "Read" {
				desc += " val=" + strconv.Itoa(out.Value)
			}
			desc += " zxid=" + fmt.Sprint(out.Zxid)
			return desc
		},
	}

	timeout := 5 * time.Minute
	ok, info := porcupine.CheckEventsVerbose(model, events, timeout)
	switch ok {
	case porcupine.Ok:
		t.Log("✅ LINEARIZABLE")
	case porcupine.Unknown:
		t.Log("⌛ TIMEOUT")
	default:
		t.Log("❌ NOT LINEARIZABLE")
		fmt.Printf("Longest linearizable prefix = %d\n", info.PartialLinearizations())
	}

	if *vizDir != "" {
		vizFile, err := os.CreateTemp(*vizDir, "viz_*.html")
		if err != nil {
			t.Fatalf("failed to create temp file")
		}
		porcupine.Visualize(model, info, vizFile)
	}

}
