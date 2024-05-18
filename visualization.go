package porcupine

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

type LinearizationInfo struct {
	history               [][]entry // for each partition, a list of entries
	partialLinearizations [][][]int // for each partition, a set of histories (list of ids)
	annotations           []annotation
}

type historyElement struct {
	ClientId    int
	Start       int64
	End         int64
	Description string
}

type annotation struct {
	ClientId        int
	Tag             string
	Start           int64
	End             int64
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
		li.annotations = append(li.annotations, annotation{
			ClientId:        elem.ClientId,
			Tag:             elem.Tag,
			Start:           elem.Start,
			End:             end,
			Description:     elem.Description,
			Details:         elem.Details,
			Annotation:      true,
			TextColor:       elem.TextColor,
			BackgroundColor: elem.BackgroundColor,
		})
	}
}

func computeVisualizationData(model Model, info LinearizationInfo) visualizationData {
	model = fillDefault(model)
	partitions := make([]partitionVisualizationData, len(info.history))
	for partition := 0; partition < len(info.history); partition++ {
		// history
		n := len(info.history[partition]) / 2
		history := make([]historyElement, n)
		callValue := make(map[int]interface{})
		returnValue := make(map[int]interface{})
		for _, elem := range info.history[partition] {
			switch elem.kind {
			case callEntry:
				history[elem.id].ClientId = elem.clientId
				history[elem.id].Start = elem.time
				callValue[elem.id] = elem.value
			case returnEntry:
				history[elem.id].End = elem.time
				history[elem.id].Description = model.DescribeOperation(callValue[elem.id], elem.value)
				returnValue[elem.id] = elem.value
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
	annotations := info.annotations
	if annotations == nil {
		annotations = make([]annotation, 0)
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
