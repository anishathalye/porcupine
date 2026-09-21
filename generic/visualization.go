package generic

import (
	"io"

	"github.com/anishathalye/porcupine"
)

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
func Visualize[S, I, O any](model Model[S, I, O], info porcupine.LinearizationInfo, output io.Writer) error {
	return porcupine.Visualize(model.ToModel(), info, output)
}

// VisualizePath is a wrapper around [Visualize] to write the visualization to
// a file path.
func VisualizePath[S, I, O any](model Model[S, I, O], info porcupine.LinearizationInfo, path string) error {
	return porcupine.VisualizePath(model.ToModel(), info, path)
}
