package generic

import (
	"os"
	"testing"

	"github.com/anishathalye/porcupine"
)

func visualizeTempFile[S, I, O any](t *testing.T, model Model[S, I, O], info porcupine.LinearizationInfo) {
	file, err := os.CreateTemp("", "*.html")
	if err != nil {
		t.Fatalf("failed to create temp file")
	}
	err = Visualize(model, info, file)
	if err != nil {
		t.Fatalf("visualization failed")
	}
	t.Logf("wrote visualization to %s", file.Name())
}
