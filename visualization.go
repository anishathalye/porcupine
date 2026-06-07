package porcupine

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type historyElement struct {
	ClientId      int
	Start         int
	OriginalStart string
	End           int
	OriginalEnd   string
	Description   string
	Metadata      string
	Id            int
	StartDeps     []int
}

type annotation struct {
	ClientId        int
	Tag             string
	Start           int
	End             int
	Description     string
	Details         string
	Annotation      bool // always true
	TextColor       string
	BackgroundColor string
}

type linearizationStep struct {
	Index            int
	StateDescription string
}

type partialLinearization = []linearizationStep

type partitionVisualizationData struct {
	History               []historyElement
	PartialLinearizations []partialLinearization
	Largest               map[int]int
}

type visualizationData struct {
	Partitions  []partitionVisualizationData
	Annotations []annotation
}

// Annotations to add to histories.
//
// Either a ClientId or Tag must be supplied. The End is optional, for "point
// in time" annotations. If the end is left unspecified, the framework
// interprets it as Start. The text given in Description is shown in the main
// visualization, and the text given in Details (optional) is shown in the
// tooltip for the annotation. TextColor and BackgroundColor are both optional;
// if specified, they should be valid CSS colors, e.g., "#efaefc".
//
// To attach annotations to a visualization, use
// [LinearizationInfo.AddAnnotations].
type Annotation struct {
	ClientId        int
	Tag             string
	Start           int64
	End             int64
	Description     string
	Details         string
	TextColor       string
	BackgroundColor string
	_               struct{} // disallow positional literals, for extensibility
}

// AddAnnotations adds extra annotations to a visualization.
//
// This can be used to add extra client operations  or it can be used to add
// standalone annotations with arbitrary tags, e.g., associated with "servers"
// rather than clients, or even a "test framework".
//
// See documentation on [Annotation] for what kind of annotations you can add.
func (li *LinearizationInfo) AddAnnotations(annotations []Annotation) {
	for _, elem := range annotations {
		end := elem.End
		if end < elem.Start {
			end = elem.Start
		}
		li.annotations = append(li.annotations, Annotation{
			ClientId:        elem.ClientId,
			Tag:             elem.Tag,
			Start:           elem.Start,
			End:             end,
			Description:     elem.Description,
			Details:         elem.Details,
			TextColor:       elem.TextColor,
			BackgroundColor: elem.BackgroundColor,
		})
	}
}

// timestampMapping applies a monotonic map to compress timestamps.
//
// This function applies a monotonic map to timestamps so that the encoding of
// timestamps in JSON keeps integers smaller than Number.MAX_SAFE_INTEGER.
// Additionally, this function ensures that the minimum delta between any two
// timestamps is at least 100, to coordinate with index.js, where it is
// convenient to be able to adjust timestamps by an epsilon value (epsilon = 16)
// without them overlapping with other adjusted timestamps.
func timestampMapping(info LinearizationInfo) map[int64]int {
	// find all timestamps
	allTimestamps := make(map[int64]struct{})
	for _, partition := range info.history {
		for _, cltOps := range partition {
			for _, cltOp := range cltOps {
				allTimestamps[cltOp.Op.Call] = struct{}{}
				allTimestamps[cltOp.Op.Return] = struct{}{}
			}
		}
	}
	for _, elem := range info.annotations {
		allTimestamps[elem.Start] = struct{}{}
		allTimestamps[elem.End] = struct{}{}
	}

	// sort
	timestamps := make([]int64, 0, len(allTimestamps))
	for ts := range allTimestamps {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i] < timestamps[j]
	})

	// construct mapping
	mapping := make(map[int64]int)
	for i, ts := range timestamps {
		mapping[ts] = i * 100 // ensure minimum delta of 100 between timestamps
	}
	return mapping
}

func computeVisualizationData(model Model, info LinearizationInfo) visualizationData {
	timeMap := timestampMapping(info)
	model = fillDefault(model)
	partitions := make([]partitionVisualizationData, len(info.history))
	for partition := 0; partition < len(info.history); partition++ {
		// history: count total ops across all clients in this partition
		numOps := 0
		for _, cltOps := range info.history[partition] {
			numOps += len(cltOps)
		}
		history := make([]historyElement, numOps)
		callValue := make(map[int]interface{})
		returnValue := make(map[int]interface{})
		for _, cltOps := range info.history[partition] {
			for _, cltOp := range cltOps {
				op := cltOp.Op
				id := cltOp.Id
				history[id].ClientId = op.ClientId
				history[id].Start = timeMap[op.Call]
				history[id].OriginalStart = fmt.Sprintf("%d", op.Call)
				history[id].End = timeMap[op.Return]
				history[id].OriginalEnd = fmt.Sprintf("%d", op.Return)
				history[id].Description = model.DescribeOperation(op.Input, op.Output)
				history[id].Metadata = model.DescribeOperationMetadata(op.Metadata)
				history[id].Id = cltOp.Id
				history[id].StartDeps = cltOp.Start
				callValue[id] = op.Input
				returnValue[id] = op.Output
			}
			// historyElement.Annotation defaults to false, so we
			// don't need to explicitly set it here; all of these
			// are non-annotation elements
		}
		// partial linearizations
		largestIndex := make(map[int]int)
		largestSize := make(map[int]int)
		linearizations := make([]partialLinearization, len(info.partialLinearizations[partition]))
		partials := info.partialLinearizations[partition]
		sort.Slice(partials, func(i, j int) bool {
			return len(partials[i]) > len(partials[j])
		})
		for i, partial := range partials {
			linearization := make(partialLinearization, len(partial))
			state := model.Init()
			for j, histId := range partial {
				var ok bool
				ok, state = model.Step(state, callValue[histId], returnValue[histId])
				if !ok {
					panic("valid partial linearization returned non-ok result from model step")
				}
				stateDesc := model.DescribeState(state)
				linearization[j] = linearizationStep{histId, stateDesc}
				if largestSize[histId] < len(partial) {
					largestSize[histId] = len(partial)
					largestIndex[histId] = i
				}
			}
			linearizations[i] = linearization
		}
		partitions[partition] = partitionVisualizationData{
			History:               history,
			PartialLinearizations: linearizations,
			Largest:               largestIndex,
		}
	}
	annotations := make([]annotation, len(info.annotations))
	for i, elem := range info.annotations {
		annotations[i] = annotation{
			ClientId:        elem.ClientId,
			Tag:             elem.Tag,
			Start:           timeMap[elem.Start],
			End:             timeMap[elem.End],
			Description:     elem.Description,
			Details:         elem.Details,
			Annotation:      true,
			TextColor:       elem.TextColor,
			BackgroundColor: elem.BackgroundColor,
		}
	}
	data := visualizationData{
		Partitions:  partitions,
		Annotations: annotations,
	}

	return data
}

// Visualize produces a visualization of a history and (partial) linearization
// as an HTML file that can be viewed in a web browser.
//
// If the history is linearizable, the visualization shows the linearization of
// the history. If the history is not linearizable, the visualization shows
// partial linearizations and illegal linearization points.
//
// To get the LinearizationInfo that this function requires, you can use
// [CheckOperationsVerbose] / [CheckEventsVerbose].
//
// This function writes the visualization, an HTML file with embedded
// JavaScript and data, to the given output.
func Visualize(model Model, info LinearizationInfo, output io.Writer) error {
	data := computeVisualizationData(model, info)
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	templateB, _ := visualizationFS.ReadFile("visualization/index.html")
	template := string(templateB)
	css, _ := visualizationFS.ReadFile("visualization/index.css")
	js, _ := visualizationFS.ReadFile("visualization/index.js")
	_, err = fmt.Fprintf(output, template, css, js, jsonData)
	if err != nil {
		return err
	}

	if f, ok := output.(*os.File); ok {
		name := f.Name()
		if name != "" && name != os.Stdout.Name() && name != os.Stderr.Name() {
			dagPath := strings.TrimSuffix(name, ".html") + "_dag.html"
			if f2, err := os.Create(dagPath); err == nil {
				defer f2.Close()
				if err := VisualizeDAG(data, f2); err != nil {
					fmt.Printf("VisualizeDAG error: %v\n", err)
				}
			}
		}
	}
	return nil
}

// VisualizePath is a wrapper around [Visualize] to write the visualization to
// a file path.
func VisualizePath(model Model, info LinearizationInfo, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return Visualize(model, info, f)
}

//go:embed visualization
var visualizationFS embed.FS

type dagNode struct {
	Id       int
	ClientId int
	Index    int
	Desc     string
	Start    string
	End      string
	Level    int
}

func VisualizeDAG(data visualizationData, output io.Writer) error {
	if len(data.Partitions) == 0 {
		return nil
	}

	var longestSerialization []linearizationStep
	if len(data.Partitions) > 0 {
		for _, lin := range data.Partitions[0].PartialLinearizations {
			if len(lin) > len(longestSerialization) {
				longestSerialization = lin
			}
		}
	}

	inLongest := make(map[int]bool)
	for _, step := range longestSerialization {
		inLongest[step.Index] = true
	}

	history := data.Partitions[0].History

	originalIndex := make(map[int]int)
	clientOpCount := make(map[int]int)
	for _, op := range history {
		originalIndex[op.Id] = clientOpCount[op.ClientId]
		clientOpCount[op.ClientId]++
	}

	extraCount := make(map[int]int)
	clientOps := make(map[int][]historyElement)

	for _, op := range history {
		if inLongest[op.Id] {
			clientOps[op.ClientId] = append(clientOps[op.ClientId], op)
		} else if extraCount[op.ClientId] < 10 {
			clientOps[op.ClientId] = append(clientOps[op.ClientId], op)
			extraCount[op.ClientId]++
		}
	}

	nodes := make(map[int]*dagNode)
	nodeByIndex := make(map[int]map[int]*dagNode)

	for cid, ops := range clientOps {
		nodeByIndex[cid] = make(map[int]*dagNode)
		for _, op := range ops {
			start := op.OriginalStart
			if start == "" {
				start = fmt.Sprintf("%v", op.Start)
			}
			end := op.OriginalEnd
			if end == "" {
				end = fmt.Sprintf("%v", op.End)
			}

			origIdx := originalIndex[op.Id]

			n := &dagNode{
				Id:       op.Id,
				ClientId: op.ClientId,
				Index:    origIdx,
				Desc:     op.Description,
				Start:    start,
				End:      end,
			}
			nodes[op.Id] = n
			nodeByIndex[cid][origIdx] = n
		}
	}

	// Build adjacency list. Use an edgeSet to avoid duplicate edges.
	adj := make(map[int][]int)
	inDegree := make(map[int]int)
	for id := range nodes {
		inDegree[id] = 0
	}
	type edgeKey struct{ from, to int }
	edgeSet := make(map[edgeKey]bool)

	addEdge := func(from, to int) {
		k := edgeKey{from, to}
		if edgeSet[k] {
			return
		}
		edgeSet[k] = true
		adj[from] = append(adj[from], to)
		inDegree[to]++
	}

	// Intra-client (session-order) edges for included operations.
	for _, ops := range clientOps {
		for i := 1; i < len(ops); i++ {
			addEdge(ops[i-1].Id, ops[i].Id)
		}
	}

	// Inter-client dependency edges.
	for _, ops := range clientOps {
		for _, op := range ops {
			for depCid, count := range op.StartDeps {
				if count > 0 {
					depIndex := count - 1
					if depNode, ok := nodeByIndex[depCid][depIndex]; ok && depNode.Id != op.Id {
						addEdge(depNode.Id, op.Id)
					}
				}
			}
		}
	}

	// Topological sort to assign levels
	deg := make(map[int]int, len(inDegree))
	for k, v := range inDegree {
		deg[k] = v
	}
	var queue []int
	for id, d := range deg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	var topo []int
	levels := make(map[int]int)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		topo = append(topo, u)
		for _, v := range adj[u] {
			if levels[u]+1 > levels[v] {
				levels[v] = levels[u] + 1
			}
			deg[v]--
			if deg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}

	hasCycle := len(topo) != len(nodes)
	if hasCycle {
		for _, n := range nodes {
			n.Level = n.Index
		}
	} else {
		for id, level := range levels {
			nodes[id].Level = level
		}
	}

	// Cycle detection
	cycleEdge := make(map[edgeKey]bool)
	if hasCycle {
		color := make(map[int]int) // 0=white,1=gray,2=black
		var dfs func(u int)
		dfs = func(u int) {
			color[u] = 1
			for _, v := range adj[u] {
				if color[v] == 1 {
					cycleEdge[edgeKey{u, v}] = true
				} else if color[v] == 0 {
					dfs(v)
				}
			}
			color[u] = 2
		}
		for id := range nodes {
			if color[id] == 0 {
				dfs(id)
			}
		}
	}

	// Transitive reduction
	// reach[u] = set of all nodes reachable from u via adj (not u itself).
	// An edge u->w is redundant if w is reachable from some other child v of u.
	redundant := make(map[edgeKey]bool)
	if !hasCycle {
		reach := make(map[int]map[int]bool, len(nodes))
		for i := len(topo) - 1; i >= 0; i-- {
			u := topo[i]
			reach[u] = make(map[int]bool)
			children := adj[u]
			for _, v := range children {
				reach[u][v] = true
				for w := range reach[v] {
					reach[u][w] = true
				}
			}
			// u->w is redundant if w is reachable via a different child of u.
			for ci, v := range children {
				for cj, w := range children {
					if ci != cj && reach[v][w] {
						redundant[edgeKey{u, w}] = true
					}
				}
			}
		}
	}

	// Compute Y positions
	const (
		lineHeight = 20
		boxPadding = 20
		nodeGap    = 20
		levelGap   = 40
		xSpacing   = 200
	)

	nodeH := func(n *dagNode) int {
		return (strings.Count(n.Desc, " ")+1)*lineHeight + boxPadding
	}

	levelBuckets := make(map[int][]int)
	for id, n := range nodes {
		levelBuckets[n.Level] = append(levelBuckets[n.Level], id)
	}

	subY := make(map[int]int, len(nodes))
	for _, ids := range levelBuckets {
		sort.Slice(ids, func(i, j int) bool {
			ni, nj := nodes[ids[i]], nodes[ids[j]]
			if ni.ClientId != nj.ClientId {
				return ni.ClientId < nj.ClientId
			}
			return ni.Index < nj.Index
		})
		cumY := 0
		for _, id := range ids {
			subY[id] = cumY
			cumY += nodeH(nodes[id]) + nodeGap
		}
	}

	maxLevel := 0
	for _, n := range nodes {
		if n.Level > maxLevel {
			maxLevel = n.Level
		}
	}
	levelBaseY := make(map[int]int, maxLevel+1)
	for lv := 1; lv <= maxLevel; lv++ {
		prevMax := 0
		for _, id := range levelBuckets[lv-1] {
			if bot := subY[id] + nodeH(nodes[id]); bot > prevMax {
				prevMax = bot
			}
		}
		levelBaseY[lv] = levelBaseY[lv-1] + prevMax + levelGap
	}

	// Build HTML nodes & edges
	type HtmlNode struct {
		Id    int    `json:"id"`
		Label string `json:"label"`
		Title string `json:"title,omitempty"`
		Group int    `json:"group"`
		X     int    `json:"x"`
		Y     int    `json:"y"`
	}

	type HtmlEdge struct {
		From   int         `json:"from"`
		To     int         `json:"to"`
		Arrows string      `json:"arrows"`
		Color  interface{} `json:"color,omitempty"`
		Width  int         `json:"width,omitempty"`
	}

	htmlNodes := make([]HtmlNode, 0, len(nodes))
	for _, n := range nodes {
		htmlNodes = append(htmlNodes, HtmlNode{
			Id:    n.Id,
			Label: strings.ReplaceAll(n.Desc, " ", "\n"),
			Title: n.Desc,
			Group: n.ClientId,
			X:     n.ClientId * xSpacing,
			Y:     levelBaseY[n.Level] + subY[n.Id],
		})
	}

	htmlEdges := make([]HtmlEdge, 0, len(edgeSet))
	for k := range edgeSet {
		isCyclic := cycleEdge[k]
		isRedundant := redundant[k]
		fromNode, toNode := nodes[k.from], nodes[k.to]
		isIntra := fromNode.ClientId == toNode.ClientId && toNode.Index == fromNode.Index+1

		var edgeColor interface{}
		edgeWidth := 0
		if isCyclic {
			edgeColor = map[string]interface{}{"color": "red", "inherit": false}
			edgeWidth = 3
		}

		if isIntra || isCyclic || !isRedundant {
			htmlEdges = append(htmlEdges, HtmlEdge{
				From:   k.from,
				To:     k.to,
				Arrows: "to",
				Color:  edgeColor,
				Width:  edgeWidth,
			})
		}
	}

	for i := 0; i < len(longestSerialization)-1; i++ {
		fromId := longestSerialization[i].Index
		toId := longestSerialization[i+1].Index
		htmlEdges = append(htmlEdges, HtmlEdge{
			From:   fromId,
			To:     toId,
			Arrows: "to",
			Color:  map[string]interface{}{"color": "black", "inherit": false},
			Width:  3,
		})
	}

	nodesJson, err := json.Marshal(htmlNodes)
	if err != nil {
		return err
	}
	edgesJson, err := json.Marshal(htmlEdges)
	if err != nil {
		return err
	}

	templateB, err := visualizationFS.ReadFile("visualization/dag.html")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, string(templateB), nodesJson, edgesJson)
	return err
}
