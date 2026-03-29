package zookeeper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"
	"olympos.io/encoding/edn"
)

// CheckerOutput encapsulates the result of the linearizability checker
// for JSON output by the test script.
type CheckerOutput struct {
	Valid        bool    `json:"valid"`
	Status       string  `json:"status"` // "ok", "timeout", "fail", "error"
	EventsParsed int     `json:"events_parsed"`
	DurationSec  float64 `json:"duration_sec"`
	VizPath      string  `json:"visualization,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// PrintJSON calculate duration and print JSON
func (out *CheckerOutput) PrintJSON(startTime time.Time) {
	out.DurationSec = time.Since(startTime).Seconds()
	if jsonBytes, err := json.Marshal(out); err == nil {
		fmt.Printf("JSON_OUTPUT: %s\n", string(jsonBytes))
	}
}

// WriteVizFile handles creating the porcupine visualization file
func WriteVizFile(out *CheckerOutput, t *testing.T, ok porcupine.CheckResult, info porcupine.LinearizationInfo, model porcupine.Model, vizDir string, logFile string, suffix string) {
	vizPath := filepath.Join(vizDir, filepath.Base(logFile)+"_"+suffix+".html")
	vizFile, err := os.Create(vizPath)
	if err == nil {
		defer vizFile.Close()
		porcupine.Visualize(model, info, vizFile)
		out.VizPath = vizFile.Name()
		t.Logf("Visualization written to %s", vizFile.Name())
	} else {
		if out.Error != "" {
			out.Error += "; "
		}
		out.Error += fmt.Sprintf("failed to create viz file: %v", err)
		if ok != porcupine.Unknown {
			t.Fatalf("failed to create viz file: %v", err)
		}
	}
}

// ToInt is a helper to safely convert interface{} to int
func ToInt(p interface{}) (int, bool) {
	switch v := p.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// ParseEDNLog abstract base of edn log parsing, delegates entry looping via a callback.
func ParseEDNLog(path string, decodeLoop func(dec *edn.Decoder) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := edn.NewDecoder(bufio.NewReader(f))
	return decodeLoop(dec)
}
