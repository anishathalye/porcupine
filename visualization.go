package porcupine

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

type historyElement struct {
	ClientId    int
	Start       int64
	End         int64
	Description string
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

type visualizationData = []partitionVisualizationData

func computeVisualizationData(model Model, info linearizationInfo) visualizationData {
	model = fillDefault(model)
	data := make(visualizationData, len(info.history))
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
		data[partition] = partitionVisualizationData{
			History:               history,
			PartialLinearizations: linearizations,
			Largest:               largestIndex,
		}
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
// To get the linearizationInfo that this function requires, you can use
// [CheckOperationsVerbose] / [CheckEventsVerbose].
//
// This function writes the visualization, an HTML file with embedded
// JavaScript and data, to the given output.
func Visualize(model Model, info linearizationInfo, output io.Writer) error {
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
func VisualizePath(model Model, info linearizationInfo, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return Visualize(model, info, f)
}

//go:embed visualization
var visualizationFS embed.FS
