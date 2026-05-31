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
	Id            int   // global operation id
	StartDeps     []int // StartDeps[c] = index of first op in client c with no outgoing dep to this op
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
				VisualizeDAG(data, f2)
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

type dagEdge struct {
	From int
	To   int
	Type string
}

func VisualizeDAG(data visualizationData, output io.Writer) error {
	if len(data.Partitions) == 0 {
		return nil
	}
	history := data.Partitions[0].History

	clientOps := make(map[int][]historyElement)
	for _, op := range history {
		if len(clientOps[op.ClientId]) < 200 {
			clientOps[op.ClientId] = append(clientOps[op.ClientId], op)
		}
	}

	nodes := make(map[int]*dagNode)
	nodeByIndex := make(map[int]map[int]*dagNode)

	for cid, ops := range clientOps {
		nodeByIndex[cid] = make(map[int]*dagNode)
		for i, op := range ops {
			start := op.OriginalStart
			if start == "" {
				start = fmt.Sprintf("%v", op.Start)
			}
			end := op.OriginalEnd
			if end == "" {
				end = fmt.Sprintf("%v", op.End)
			}
			n := &dagNode{
				Id:       op.Id,
				ClientId: op.ClientId,
				Index:    i,
				Desc:     op.Description,
				Start:    start,
				End:      end,
			}
			nodes[op.Id] = n
			nodeByIndex[cid][i] = n
		}
	}

	var edges []dagEdge
	adj := make(map[int][]int)
	inDegree := make(map[int]int)
	for id := range nodes {
		inDegree[id] = 0
	}

	for cid, ops := range clientOps {
		for i, op := range ops {
			opId := op.Id
			if i > 0 {
				prevOp := nodeByIndex[cid][i-1]
				edges = append(edges, dagEdge{From: prevOp.Id, To: opId, Type: "intra"})
				adj[prevOp.Id] = append(adj[prevOp.Id], opId)
				inDegree[opId]++
			}
			for depCid, count := range op.StartDeps {
				if count > 0 {
					depIndex := count - 1
					if depNode, ok := nodeByIndex[depCid][depIndex]; ok {
						if depNode.Id != opId {
							edges = append(edges, dagEdge{From: depNode.Id, To: opId, Type: "inter"})
							adj[depNode.Id] = append(adj[depNode.Id], opId)
							inDegree[opId]++
						}
					}
				}
			}
		}
	}

	var queue []int
	for id, deg := range inDegree {
		if deg == 0 {
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
			inDegree[v]--
			if inDegree[v] == 0 {
				queue = append(queue, v)
			}
		}
	}

	if len(topo) != len(nodes) {
		return fmt.Errorf("cycle found, cannot generate DAG")
	}

	for id, level := range levels {
		nodes[id].Level = level
	}

	redundant := make(map[int]map[int]bool)
	for id := range nodes {
		redundant[id] = make(map[int]bool)
	}

	for u := range nodes {
		for _, v := range adj[u] {
			visited := make(map[int]bool)
			var q []int
			q = append(q, v)
			visited[v] = true
			for len(q) > 0 {
				curr := q[0]
				q = q[1:]
				for _, next := range adj[curr] {
					if !visited[next] {
						visited[next] = true
						q = append(q, next)
					}
				}
			}
			for w := range visited {
				if w != v {
					redundant[u][w] = true
				}
			}
		}
	}

	type HtmlNode struct {
		Id    int    `json:"id"`
		Label string `json:"label"`
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

	var htmlNodes []HtmlNode
	var htmlEdges []HtmlEdge
	const X_SPACING = 200
	const Y_SPACING = 150

	for _, n := range nodes {
		label := strings.ReplaceAll(n.Desc, " ", "\n")
		htmlNodes = append(htmlNodes, HtmlNode{
			Id:    n.Id,
			Label: label,
			Group: n.ClientId,
			X:     n.ClientId * X_SPACING,
			Y:     n.Level * Y_SPACING,
		})
	}

	for _, e := range edges {
		if e.Type == "intra" {
			htmlEdges = append(htmlEdges, HtmlEdge{
				From:   e.From,
				To:     e.To,
				Arrows: "to",
			})
		} else if e.Type == "inter" {
			if !redundant[e.From][e.To] {
				htmlEdges = append(htmlEdges, HtmlEdge{
					From:   e.From,
					To:     e.To,
					Arrows: "to",
				})
			}
		}
	}

	var longestSerialization []linearizationStep
	if len(data.Partitions) > 0 {
		for _, lin := range data.Partitions[0].PartialLinearizations {
			if len(lin) > len(longestSerialization) {
				longestSerialization = lin
			}
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

	nodesJson, _ := json.Marshal(htmlNodes)
	edgesJson, _ := json.Marshal(htmlEdges)

	templateB, err := visualizationFS.ReadFile("visualization/dag.html")
	if err != nil {
		return err
	}
	htmlTemplate := string(templateB)

	htmlContent := strings.Replace(htmlTemplate, "%s", string(nodesJson), 1)
	htmlContent = strings.Replace(htmlContent, "%s", string(edgesJson), 1)
	_, err = fmt.Fprint(output, htmlContent)
	return err
}
