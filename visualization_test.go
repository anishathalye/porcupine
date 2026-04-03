package porcupine

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func visualizeTempFile(t *testing.T, model Model, info LinearizationInfo) {
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

func TestVisualizationMultipleLengths(t *testing.T) {
	ops := []Operation{
		{ClientId: 0, Input: kvInput{op: 0, key: "x"}, Call: 0, Output: kvOutput{"w"}, Return: 100},
		{ClientId: 1, Input: kvInput{op: 1, key: "x", value: "y"}, Call: 5, Output: kvOutput{}, Return: 10},
		{ClientId: 2, Input: kvInput{op: 1, key: "x", value: "z"}, Call: 0, Output: kvOutput{}, Return: 10},
		{ClientId: 1, Input: kvInput{op: 0, key: "x"}, Call: 20, Output: kvOutput{"y"}, Return: 30},
		{ClientId: 1, Input: kvInput{op: 1, key: "x", value: "w"}, Call: 35, Output: kvOutput{}, Return: 45},
		{ClientId: 5, Input: kvInput{op: 0, key: "x"}, Call: 25, Output: kvOutput{"z"}, Return: 35},
		{ClientId: 3, Input: kvInput{op: 0, key: "x"}, Call: 30, Output: kvOutput{"y"}, Return: 40},
		{ClientId: 4, Input: kvInput{op: 0, key: "y"}, Call: 50, Output: kvOutput{"a"}, Return: 90},
		{ClientId: 2, Input: kvInput{op: 1, key: "y", value: "a"}, Call: 55, Output: kvOutput{}, Return: 85},
	}
	res, info := CheckOperationsVerbose(kvModel, ops, 0)
	if res != Illegal {
		t.Fatalf("expected output %v, got output %v", Illegal, res)
	}
	data := computeVisualizationData(kvModel, info)
	expected := []partitionVisualizationData{{
		History: []historyElement{
			{ClientId: 0, Start: 0, OriginalStart: "0", End: 1300, OriginalEnd: "100", Description: "get('x') -> 'w'"},
			{ClientId: 1, Start: 100, OriginalStart: "5", End: 200, OriginalEnd: "10", Description: "put('x', 'y')"},
			{ClientId: 2, Start: 0, OriginalStart: "0", End: 200, OriginalEnd: "10", Description: "put('x', 'z')"},
			{ClientId: 1, Start: 300, OriginalStart: "20", End: 500, OriginalEnd: "30", Description: "get('x') -> 'y'"},
			{ClientId: 1, Start: 600, OriginalStart: "35", End: 800, OriginalEnd: "45", Description: "put('x', 'w')"},
			{ClientId: 5, Start: 400, OriginalStart: "25", End: 600, OriginalEnd: "35", Description: "get('x') -> 'z'"},
			{ClientId: 3, Start: 500, OriginalStart: "30", End: 700, OriginalEnd: "40", Description: "get('x') -> 'y'"},
		},
		PartialLinearizations: []partialLinearization{
			{{2, "z"}, {1, "y"}, {3, "y"}, {6, "y"}, {4, "w"}, {0, "w"}},
			{{1, "y"}, {2, "z"}, {5, "z"}},
		},
		Largest: map[int]int{0: 0, 1: 0, 2: 0, 3: 0, 4: 0, 5: 1, 6: 0},
	}, {
		History: []historyElement{
			{ClientId: 4, Start: 900, OriginalStart: "50", End: 1200, OriginalEnd: "90", Description: "get('y') -> 'a'"},
			{ClientId: 2, Start: 1000, OriginalStart: "55", End: 1100, OriginalEnd: "85", Description: "put('y', 'a')"},
		},
		PartialLinearizations: []partialLinearization{
			{{1, "a"}, {0, "a"}},
		},
		Largest: map[int]int{0: 0, 1: 0},
	}}
	if !reflect.DeepEqual(expected, data.Partitions) {
		t.Fatalf("expected data to be \n%v\n, was \n%v", expected, data)
	}
	visualizeTempFile(t, kvModel, info)
}

func TestRegisterModelReadme(t *testing.T) {
	// basically the code from the README

	events := []Event{
		// C0: Write(100)
		{Kind: CallEvent, Value: registerInput{false, 100}, Id: 0, ClientId: 0},
		// C1: Read()
		{Kind: CallEvent, Value: registerInput{true, 0}, Id: 1, ClientId: 1},
		// C2: Read()
		{Kind: CallEvent, Value: registerInput{true, 0}, Id: 2, ClientId: 2},
		// C2: Completed Read -> 0
		{Kind: ReturnEvent, Value: 0, Id: 2, ClientId: 2},
		// C1: Completed Read -> 100
		{Kind: ReturnEvent, Value: 100, Id: 1, ClientId: 1},
		// C0: Completed Write
		{Kind: ReturnEvent, Value: 0, Id: 0, ClientId: 0},
	}

	res, info := CheckEventsVerbose(registerModel, events, 0)
	// returns true

	if res != Ok {
		t.Fatal("expected operations to be linearizable")
	}

	visualizeTempFile(t, registerModel, info)

	events = []Event{
		// C0: Write(200)
		{Kind: CallEvent, Value: registerInput{false, 200}, Id: 0, ClientId: 0},
		// C1: Read()
		{Kind: CallEvent, Value: registerInput{true, 0}, Id: 1, ClientId: 1},
		// C1: Completed Read -> 200
		{Kind: ReturnEvent, Value: 200, Id: 1, ClientId: 1},
		// C2: Read()
		{Kind: CallEvent, Value: registerInput{true, 0}, Id: 2, ClientId: 2},
		// C2: Completed Read -> 0
		{Kind: ReturnEvent, Value: 0, Id: 2, ClientId: 2},
		// C0: Completed Write
		{Kind: ReturnEvent, Value: 0, Id: 0, ClientId: 0},
	}

	res, info = CheckEventsVerbose(registerModel, events, 0)
	// returns false

	if res != Illegal {
		t.Fatal("expected operations not to be linearizable")
	}

	visualizeTempFile(t, registerModel, info)
}

func TestVisualizationLarge(t *testing.T) {
	events := parseJepsenLog("test_data/jepsen/etcd_070.log")
	res, info := CheckEventsVerbose(etcdModel, events, 0)
	if res != Illegal {
		t.Fatal("expected operations not to be linearizable")
	}

	visualizeTempFile(t, etcdModel, info)
}

func TestVisualizationAnnotations(t *testing.T) {
	// base set of operations same as TestVisualizationMultipleLengths
	ops := []Operation{
		{ClientId: 0, Input: kvInput{op: 0, key: "x"}, Call: 0, Output: kvOutput{"w"}, Return: 100},
		{ClientId: 1, Input: kvInput{op: 1, key: "x", value: "y"}, Call: 5, Output: kvOutput{}, Return: 10},
		{ClientId: 2, Input: kvInput{op: 1, key: "x", value: "z"}, Call: 0, Output: kvOutput{}, Return: 10},
		{ClientId: 1, Input: kvInput{op: 0, key: "x"}, Call: 20, Output: kvOutput{"y"}, Return: 30},
		{ClientId: 1, Input: kvInput{op: 1, key: "x", value: "w"}, Call: 35, Output: kvOutput{}, Return: 45},
		{ClientId: 5, Input: kvInput{op: 0, key: "x"}, Call: 25, Output: kvOutput{"z"}, Return: 35},
		{ClientId: 3, Input: kvInput{op: 0, key: "x"}, Call: 30, Output: kvOutput{"y"}, Return: 40},
		{ClientId: 4, Input: kvInput{op: 0, key: "y"}, Call: 50, Output: kvOutput{"a"}, Return: 90},
		{ClientId: 2, Input: kvInput{op: 1, key: "y", value: "a"}, Call: 55, Output: kvOutput{}, Return: 85},
	}
	res, info := CheckOperationsVerbose(kvModel, ops, 0)
	annotations := []Annotation{
		// let's say that there was a "failed get" by client 4 early on
		{ClientId: 4, Start: 10, End: 31, Description: "get('y') timeout", BackgroundColor: "#ff9191"},
		// and a failed get by client 5 later
		{ClientId: 5, Start: 80, Description: "get('x') timeout", BackgroundColor: "#ff9191"},
		// and some tagged annotations
		{Tag: "Server 1", Start: 30, Description: "leader", Details: "became leader in term 3 with 2 votes"},
		{Tag: "Server 3", Start: 10, Description: "duplicate", Details: "saw duplicate operation put('x', 'y')"},
		{Tag: "Server 2", Start: 80, Description: "restart"},
		{Tag: "Server 3", Start: 0, Description: "leader", Details: "became leader in term 1 with 3 votes"},
		// and some "test framework" annotations
		{Tag: "Test Framework", Start: 20, End: 35, Description: "partition [3] [1 2]", BackgroundColor: "#efaefc"},
		{Tag: "Test Framework", Start: 40, End: 100, Description: "partition [2] [1 3]", BackgroundColor: "#efaefc"},
	}
	info.AddAnnotations(annotations)
	if res != Illegal {
		t.Fatalf("expected output %v, got output %v", Illegal, res)
	}
	// we don't check much else here, this has to be visually inspected
	visualizeTempFile(t, kvModel, info)
}

func TestVisualizePointInTimeAnnotationsEnd(t *testing.T) {
	ops := []Operation{
		{ClientId: 0, Input: kvInput{op: 0, key: "x"}, Call: 0, Output: kvOutput{"w"}, Return: 100},
		{ClientId: 1, Input: kvInput{op: 1, key: "x", value: "y"}, Call: 50, Output: kvOutput{}, Return: 60},
	}
	res, info := CheckOperationsVerbose(kvModel, ops, 0)
	if res != Illegal {
		t.Fatalf("expected output %v, got output %v", Illegal, res)
	}
	annotations := []Annotation{
		{Tag: "Server 1", Start: 30, Description: "leader change", Details: "became leader"},
		{Tag: "Server 2", Start: 50, Description: "heartbeat"},
		// point-in-time annotation at the end
		{Tag: "Server 1", Start: 100, Description: "shutdown"},
		{Tag: "Test Framework", Start: 20, End: 40, Description: "network stable", BackgroundColor: "#efaefc"},
	}
	info.AddAnnotations(annotations)
	visualizeTempFile(t, kvModel, info)
}

func TestVisualizeMatchingStartEnd(t *testing.T) {
	ops := []Operation{
		{ClientId: 0, Input: kvInput{op: 0, key: "x"}, Call: 0, Output: kvOutput{"w"}, Return: 50},
		{ClientId: 1, Input: kvInput{op: 1, key: "x", value: "y"}, Call: 50, Output: kvOutput{}, Return: 80},
	}
	res, info := CheckOperationsVerbose(kvModel, ops, 0)
	if res != Illegal {
		t.Fatalf("expected output %v, got output %v", Illegal, res)
	}
	annotations := []Annotation{
		{Tag: "Test Framework", Start: 0, End: 20, Description: "partition"},
		{Tag: "Test Framework", Start: 20, End: 20, Description: "point in time 1"},
		{Tag: "Test Framework", Start: 20, End: 40, Description: "network stable"},
	}
	info.AddAnnotations(annotations)
	visualizeTempFile(t, kvModel, info)
}

func TestVisualizeAnnotationsNoEvents(t *testing.T) {
	var info LinearizationInfo
	annotations := []Annotation{
		{Tag: "$ Test Info", Start: 1739938076171778000, End: 1739938076171778000, Description: "TestPersist33C (3 servers)"},
		{Tag: "$ Checker", Start: 1739938076171786000, End: 1739938086186709000, Description: "agreement of 101 failed"},
		{Tag: "$ Test Info", Start: 1739938086187103000, End: 1739938086187104000, Description: "test failed"},
	}
	info.AddAnnotations(annotations)
	visualizeTempFile(t, kvModel, info)
}

func TestVisualizationCallAndReturnTime(t *testing.T) {
	tests := []struct {
		name           string
		ops            []Operation
		expectedRes    CheckResult
		expectedStarts []string
		expectedEnds   []string
	}{
		{
			name: "LinearizableContent",
			ops: []Operation{
				{ClientId: 0, Input: kvInput{op: 1, key: "x", value: "a"}, Call: 0, Output: kvOutput{}, Return: 100},
			},
			expectedRes:    Ok, // CheckOperationsVerbose returns Ok for linearizable
			expectedStarts: []string{`"OriginalStart":"0"`},
			expectedEnds:   []string{`"OriginalEnd":"100"`},
		},
		{
			name: "NotLinearizedSingleIllegal",
			ops: []Operation{
				{ClientId: 0, Input: kvInput{op: 1, key: "x", value: "a"}, Call: 0, Output: kvOutput{}, Return: 100},
				{ClientId: 1, Input: kvInput{op: 0, key: "x"}, Call: 10, Output: kvOutput{"b"}, Return: 110}, // not linearizable
			},
			expectedRes:    Illegal,
			expectedStarts: []string{`"OriginalStart":"0"`, `"OriginalStart":"10"`},
			expectedEnds:   []string{`"OriginalEnd":"100"`, `"OriginalEnd":"110"`},
		},
		{
			name: "NotPartiallyLinearized",
			ops: []Operation{
				{ClientId: 0, Input: kvInput{op: 1, key: "x", value: "a"}, Call: 0, Output: kvOutput{}, Return: 100},
				{ClientId: 1, Input: kvInput{op: 0, key: "x"}, Call: 10, Output: kvOutput{"b"}, Return: 110}, // not linearizable
				{ClientId: 2, Input: kvInput{op: 0, key: "x"}, Call: 120, Output: kvOutput{"a"}, Return: 130},
			},
			expectedRes:    Illegal,
			expectedStarts: []string{`"OriginalStart":"0"`, `"OriginalStart":"10"`, `"OriginalStart":"120"`},
			expectedEnds:   []string{`"OriginalEnd":"100"`, `"OriginalEnd":"110"`, `"OriginalEnd":"130"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, info := CheckOperationsVerbose(kvModel, tt.ops, 0)
			if res != tt.expectedRes {
				t.Fatalf("expected result %v, got %v", tt.expectedRes, res)
			}

			file, err := os.CreateTemp("", "porcupine_test_*.html")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			defer os.Remove(file.Name())
			defer file.Close()

			if err := Visualize(kvModel, info, file); err != nil {
				t.Fatalf("Visualize failed: %v", err)
			}

			content, err := os.ReadFile(file.Name())
			if err != nil {
				t.Fatalf("failed to read generated HTML: %v", err)
			}

			if len(tt.expectedStarts) != len(tt.expectedEnds) {
				t.Fatalf("mismatch between expectedStarts and expectedEnds length")
			}

			for i := range tt.expectedStarts {
				if !strings.Contains(string(content), tt.expectedStarts[i]) {
					t.Errorf("expected HTML to contain %s", tt.expectedStarts[i])
				}
				if !strings.Contains(string(content), tt.expectedEnds[i]) {
					t.Errorf("expected HTML to contain %s", tt.expectedEnds[i])
				}
			}
		})
	}
}

func TestVisualizationStringMetadata(t *testing.T) {
	model := kvModel
	model.DescribeOperationMetadata = func(info interface{}) string {
		return fmt.Sprintf("custom: %v", info)
	}

	ops := []Operation{
		{ClientId: 0, Input: kvInput{op: 0, key: "x"}, Call: 0, Output: kvOutput{"w"}, Return: 100, Metadata: "meta1"},
	}
	_, info := CheckOperationsVerbose(model, ops, 0)

	// Part 1: Test data structure directly
	data := computeVisualizationData(model, info)
	if len(data.Partitions) != 1 || len(data.Partitions[0].History) != 1 {
		t.Fatalf("unexpected partition structure")
	}
	if data.Partitions[0].History[0].Metadata != "custom: meta1" {
		t.Errorf("expected Metadata='custom: meta1', got '%s'", data.Partitions[0].History[0].Metadata)
	}

	// Part 2: Test HTML output
	file, err := os.CreateTemp("", "porcupine_test_custom_metadata_*.html")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	if err := Visualize(model, info, file); err != nil {
		t.Fatalf("Visualize failed: %v", err)
	}

	content, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("failed to read generated HTML: %v", err)
	}

	re := regexp.MustCompile(`const data = (\{.*\})`)
	matches := re.FindSubmatch(content)
	if len(matches) < 2 {
		t.Fatal("failed to extract JSON data from HTML")
	}

	var htmlData visualizationData
	if err := json.Unmarshal(matches[1], &htmlData); err != nil {
		t.Fatalf("failed to unmarshal JSON from HTML: %v", err)
	}

	if len(htmlData.Partitions) != 1 || len(htmlData.Partitions[0].History) != 1 {
		t.Fatalf("unexpected HTML data structure")
	}
	if htmlData.Partitions[0].History[0].Metadata != "custom: meta1" {
		t.Errorf("HTML: expected Metadata='custom: meta1', got '%s'", htmlData.Partitions[0].History[0].Metadata)
	}
}

type customMetadata struct {
	ID   int
	Info string
}

func TestVisualizationStructMetadata(t *testing.T) {
	ops := []Operation{
		{ClientId: 0, Input: kvInput{op: 0, key: "x"}, Call: 0, Output: kvOutput{"w"}, Return: 100, Metadata: customMetadata{1, "meta1"}},
		{ClientId: 1, Input: kvInput{op: 1, key: "x", value: "y"}, Call: 5, Output: kvOutput{}, Return: 10, Metadata: customMetadata{2, "meta2"}},
	}

	// Define a model that handles custom metadata
	model := kvModel
	model.DescribeOperationMetadata = func(info interface{}) string {
		if m, ok := info.(customMetadata); ok {
			return fmt.Sprintf("ID:%d, Info:%s", m.ID, m.Info)
		}
		return fmt.Sprintf("%v", info)
	}

	_, info := CheckOperationsVerbose(model, ops, 0)

	// Part 1: Test data structure directly
	data := computeVisualizationData(model, info)
	if len(data.Partitions) != 1 || len(data.Partitions[0].History) != 2 {
		t.Fatalf("unexpected partition structure")
	}
	history := data.Partitions[0].History
	if history[0].Metadata != "ID:1, Info:meta1" {
		t.Errorf("expected history[0].Metadata='ID:1, Info:meta1', got '%s'", history[0].Metadata)
	}
	if history[1].Metadata != "ID:2, Info:meta2" {
		t.Errorf("expected history[1].Metadata='ID:2, Info:meta2', got '%s'", history[1].Metadata)
	}

	// Part 2: Test HTML output
	file, err := os.CreateTemp("", "porcupine_test_custom_metadata_*.html")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	if err := Visualize(model, info, file); err != nil {
		t.Fatalf("Visualize failed: %v", err)
	}

	content, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("failed to read generated HTML: %v", err)
	}

	re := regexp.MustCompile(`const data = (\{.*\})`)
	matches := re.FindSubmatch(content)
	if len(matches) < 2 {
		t.Fatal("failed to extract JSON data from HTML")
	}

	var htmlData visualizationData
	if err := json.Unmarshal(matches[1], &htmlData); err != nil {
		t.Fatalf("failed to unmarshal JSON from HTML: %v", err)
	}

	if len(htmlData.Partitions) != 1 || len(htmlData.Partitions[0].History) != 2 {
		t.Fatalf("unexpected HTML data structure")
	}
	htmlHistory := htmlData.Partitions[0].History
	if htmlHistory[0].Metadata != "ID:1, Info:meta1" {
		t.Errorf("HTML: expected history[0].Metadata='ID:1, Info:meta1', got '%s'", htmlHistory[0].Metadata)
	}
	if htmlHistory[1].Metadata != "ID:2, Info:meta2" {
		t.Errorf("HTML: expected history[1].Metadata='ID:2, Info:meta2', got '%s'", htmlHistory[1].Metadata)
	}
}

func TestVisualizationMetadataAlwaysVisible(t *testing.T) {
	ops := []Operation{
		{ClientId: 0, Input: kvInput{op: 1, key: "x", value: "val"}, Call: 0, Output: kvOutput{}, Return: 100, Metadata: "meta_linearizable"},
		{ClientId: 1, Input: kvInput{op: 0, key: "x"}, Call: 5, Output: kvOutput{"invalid"}, Return: 10, Metadata: "meta_not_linearizable"},
	}

	_, info := CheckOperationsVerbose(kvModel, ops, 0)

	// Part 1: Test data structure directly
	data := computeVisualizationData(kvModel, info)
	if len(data.Partitions) != 1 || len(data.Partitions[0].History) != 2 {
		t.Fatalf("unexpected partition structure")
	}
	history := data.Partitions[0].History
	// Both linearizable and non-linearizable operations should have metadata
	if history[0].Metadata != "meta_linearizable" {
		t.Errorf("expected history[0].Metadata='meta_linearizable', got '%s'", history[0].Metadata)
	}
	if history[1].Metadata != "meta_not_linearizable" {
		t.Errorf("expected history[1].Metadata='meta_not_linearizable', got '%s'", history[1].Metadata)
	}

	// Part 2: Test HTML output
	file, err := os.CreateTemp("", "porcupine_test_metadata_visible_*.html")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	if err := Visualize(kvModel, info, file); err != nil {
		t.Fatalf("Visualize failed: %v", err)
	}

	content, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("failed to read generated HTML: %v", err)
	}

	re := regexp.MustCompile(`const data = (\{.*\})`)
	matches := re.FindSubmatch(content)
	if len(matches) < 2 {
		t.Fatal("failed to extract JSON data from HTML")
	}

	var htmlData visualizationData
	if err := json.Unmarshal(matches[1], &htmlData); err != nil {
		t.Fatalf("failed to unmarshal JSON from HTML: %v", err)
	}

	if len(htmlData.Partitions) != 1 || len(htmlData.Partitions[0].History) != 2 {
		t.Fatalf("unexpected HTML data structure")
	}
	htmlHistory := htmlData.Partitions[0].History
	if htmlHistory[0].Metadata != "meta_linearizable" {
		t.Errorf("HTML: expected history[0].Metadata='meta_linearizable', got '%s'", htmlHistory[0].Metadata)
	}
	if htmlHistory[1].Metadata != "meta_not_linearizable" {
		t.Errorf("HTML: expected history[1].Metadata='meta_not_linearizable', got '%s'", htmlHistory[1].Metadata)
	}
}

func TestVisualizationEventMetadata(t *testing.T) {
	model := registerModel
	model.DescribeOperationMetadata = func(info interface{}) string {
		if info == nil {
			return ""
		}
		return fmt.Sprintf("event-meta: %v", info)
	}

	events := []Event{
		// C0: Write(100) with metadata on both CallEvent and ReturnEvent (Return should take precedence)
		{Kind: CallEvent, Value: registerInput{false, 100}, Id: 0, ClientId: 0, Metadata: "write-call-meta-IGNORED"},
		// C1: Read() with metadata only on CallEvent
		{Kind: CallEvent, Value: registerInput{true, 0}, Id: 1, ClientId: 1, Metadata: "read-call-meta"},
		// C2: Read() with metadata only on ReturnEvent
		{Kind: CallEvent, Value: registerInput{true, 0}, Id: 2, ClientId: 2},
		// C1: Completed Read -> 100 (no metadata on return, should use call metadata)
		{Kind: ReturnEvent, Value: 100, Id: 1, ClientId: 1},
		// C2: Completed Read -> 100 (metadata on ReturnEvent only)
		{Kind: ReturnEvent, Value: 100, Id: 2, ClientId: 2, Metadata: "read-return-meta"},
		// C0: Completed Write (metadata on ReturnEvent should take precedence over CallEvent)
		{Kind: ReturnEvent, Value: 0, Id: 0, ClientId: 0, Metadata: "write-return-meta"},
	}

	res, info := CheckEventsVerbose(model, events, 0)
	if res != Ok {
		t.Fatal("expected operations to be linearizable")
	}

	// Part 1: Test data structure directly
	data := computeVisualizationData(model, info)

	if len(data.Partitions) != 1 {
		t.Fatalf("expected 1 partition, got %d", len(data.Partitions))
	}
	history := data.Partitions[0].History
	if len(history) != 3 {
		t.Fatalf("expected 3 history elements, got %d", len(history))
	}

	// Verify metadata is associated with the correct operations
	// history[0] = operation Id=0 (Write, ClientId=0)
	if history[0].ClientId != 0 {
		t.Errorf("expected history[0].ClientId=0, got %d", history[0].ClientId)
	}
	if history[0].Metadata != "event-meta: write-return-meta" {
		t.Errorf("expected history[0].Metadata='event-meta: write-return-meta', got '%s'", history[0].Metadata)
	}

	// history[1] = operation Id=1 (Read, ClientId=1)
	if history[1].ClientId != 1 {
		t.Errorf("expected history[1].ClientId=1, got %d", history[1].ClientId)
	}
	if history[1].Metadata != "event-meta: read-call-meta" {
		t.Errorf("expected history[1].Metadata='event-meta: read-call-meta', got '%s'", history[1].Metadata)
	}

	// history[2] = operation Id=2 (Read, ClientId=2)
	if history[2].ClientId != 2 {
		t.Errorf("expected history[2].ClientId=2, got %d", history[2].ClientId)
	}
	if history[2].Metadata != "event-meta: read-return-meta" {
		t.Errorf("expected history[2].Metadata='event-meta: read-return-meta', got '%s'", history[2].Metadata)
	}

	// Part 2: Test HTML output (end-to-end verification)
	file, err := os.CreateTemp("", "porcupine_test_event_metadata_*.html")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	if err := Visualize(model, info, file); err != nil {
		t.Fatalf("Visualize failed: %v", err)
	}

	content, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("failed to read generated HTML: %v", err)
	}

	// Extract JSON from HTML: "const data = {...}"
	re := regexp.MustCompile(`const data = (\{.*\})`)
	matches := re.FindSubmatch(content)
	if len(matches) < 2 {
		t.Fatal("failed to extract JSON data from HTML")
	}

	var htmlData visualizationData
	if err := json.Unmarshal(matches[1], &htmlData); err != nil {
		t.Fatalf("failed to unmarshal JSON from HTML: %v", err)
	}

	// Verify HTML contains correctly structured data
	if len(htmlData.Partitions) != 1 || len(htmlData.Partitions[0].History) != 3 {
		t.Fatalf("unexpected HTML data structure")
	}
	htmlHistory := htmlData.Partitions[0].History
	if htmlHistory[0].Metadata != "event-meta: write-return-meta" {
		t.Errorf("HTML: expected history[0].Metadata='event-meta: write-return-meta', got '%s'", htmlHistory[0].Metadata)
	}
	if htmlHistory[1].Metadata != "event-meta: read-call-meta" {
		t.Errorf("HTML: expected history[1].Metadata='event-meta: read-call-meta', got '%s'", htmlHistory[1].Metadata)
	}
	if htmlHistory[2].Metadata != "event-meta: read-return-meta" {
		t.Errorf("HTML: expected history[2].Metadata='event-meta: read-return-meta', got '%s'", htmlHistory[2].Metadata)
	}
}
