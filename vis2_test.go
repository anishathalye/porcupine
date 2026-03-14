package porcupine_test

import (
	"fmt"
	"os"
	"testing"

	porc "github.com/anishathalye/porcupine"
)

// The test data was moved to end of this file
// for readability. Search for:
// var eventTestData = []porc.Event{

func TestVis2(t *testing.T) {
	linz := porc.CheckEvents(stringCasModel, eventTestData)
	if !linz {
		writeToDiskNonLinzFuzzEvents(t, eventTestData)
	}
}

/* output on porcupine v1.1.0:


Compilation started at Sat Feb 14 08:38:28

GOEXPERIMENT=synctest go test -v -run Vis2
=== RUN   TestVis2
writing out non-linearizable evs history 'red.nonlinz.TestVis2.034.html'
--- FAIL: TestVis2 (0.00s)
panic: runtime error: index out of range [3] with length 3 [recovered, repanicked]

goroutine 6 [running]:
testing.tRunner.func1.2({0x4dea920, 0xc0000181c8})
	/usr/local/go/src/testing/testing.go:1872 +0x237
testing.tRunner.func1()
	/usr/local/go/src/testing/testing.go:1875 +0x35b
panic({0x4dea920?, 0xc0000181c8?})
	/usr/local/go/src/runtime/panic.go:783 +0x132
github.com/anishathalye/porcupine.computeVisualizationData({0x4dfbc58, 0x4dfbc60, 0x4dfbc70, 0x4dfbc78, 0x4dfbc68, 0x4dfbc80, 0x4dfbbc0, 0x4dfbbb8, {}}, {{0xc0000103d8, ...}, ...})
	/Users/jaten/go/src/github.com/anishathalye/porcupine/visualization.go:153 +0x11f5
github.com/anishathalye/porcupine.Visualize({0x0, 0x0, 0x4dfbc70, 0x4dfbc78, 0x0, 0x4dfbc80, 0x0, 0x0, {}}, {{0xc0000103d8, ...}, ...}, ...)
	/Users/jaten/go/src/github.com/anishathalye/porcupine/visualization.go:241 +0xc5
github.com/anishathalye/porcupine_test.writeToDiskNonLinzFuzzEvents(0xc000003880, {0x4f23120?, 0x7?, 0x0?})
	/Users/jaten/go/src/github.com/anishathalye/porcupine/vis2_test.go:218 +0x505
github.com/anishathalye/porcupine_test.TestVis2(0xc000003880)
	/Users/jaten/go/src/github.com/anishathalye/porcupine/vis2_test.go:18 +0x114
testing.tRunner(0xc000003880, 0x4dfbaf0)
	/usr/local/go/src/testing/testing.go:1934 +0xea
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:1997 +0x465
exit status 2
FAIL	github.com/anishathalye/porcupine	0.033s

Compilation exited abnormally with code 1 at Sat Feb 14 08:38:29
*/

type stringRegisterOp int

const (
	STRING_REGISTER_UNK stringRegisterOp = 0
	STRING_REGISTER_PUT stringRegisterOp = 1
	STRING_REGISTER_GET stringRegisterOp = 2
	STRING_REGISTER_CAS stringRegisterOp = 3
)

func (o stringRegisterOp) String() string {
	switch o {
	case STRING_REGISTER_PUT:
		return "STRING_REGISTER_PUT"
	case STRING_REGISTER_GET:
		return "STRING_REGISTER_GET"
	case STRING_REGISTER_CAS:
		return "STRING_REGISTER_CAS"
	}
	panic(fmt.Sprintf("unknown stringRegisterOp: %v", int(o)))
}

type casInput struct {
	id        int
	op        stringRegisterOp
	oldString string
	newString string
}

type casOutput struct {
	id int

	op        stringRegisterOp
	oldString string
	newString string

	swapped  bool   // used for CAS
	notFound bool   // used for read
	valueCur string // for read/when cas rejects
}

func (o *casOutput) String() string {
	return fmt.Sprintf(`casOutput{
            id: %v,
       swapped: %v,
      notFound: %v,
      valueCur: %q,
}`, o.id, o.swapped, o.notFound,
		o.valueCur)
}

func (ri *casInput) String() string {
	if ri.op == STRING_REGISTER_GET {
		return fmt.Sprintf("casInput{op: %v}", ri.op)
	}
	return fmt.Sprintf(`casInput{op: %v, oldString: %q, newString: %q}`, ri.op, ri.oldString, ri.newString)
}

// a sequential specification of a register, that holds a string
// and can CAS.
var stringCasModel = porc.Model{
	Init: func() interface{} {
		return "<empty>"
	},
	// step function: takes a state, input, and output, and returns whether it
	// was a legal operation, along with a new state. Must be a pure
	// function. Do not modify state, input, or output.
	Step: func(state, input, output interface{}) (legal bool, newState interface{}) {
		st := state.(string)
		inp := input.(casInput)
		out := output.(casOutput)

		switch inp.op {
		case STRING_REGISTER_GET:
			newState = st // state is unchanged by GET

			legal = (out.notFound && st == "<empty>") ||
				(!out.notFound && st == out.valueCur)
			return

		case STRING_REGISTER_PUT:
			legal = true // always ok to execute a put
			newState = inp.newString
			return

		case STRING_REGISTER_CAS:

			if inp.oldString == "" {
				// treat empty string as absent/deleted/anything goes.
				// So this becomes just a PUT:
				legal = true
				newState = inp.newString
				return
			}

			// the default is that the state stays the same.
			newState = st

			if inp.oldString == st && out.swapped {
				legal = true
			} else if inp.oldString != st && !out.swapped {
				legal = true
			} else {
				//fmt.Printf("warning: legal is false in CAS because out.swapped = '%v', inp.oldString = '%v', inp.newString = '%v'; old state = '%v', newState = '%v'; out.valueCur = '%v'\n", out.swapped, inp.oldString, inp.newString, st, newState, out.valueCur)
			}

			if legal {
				if inp.oldString == st {
					newState = inp.newString
				}
			}
			return
		}
		return
	},
	DescribeOperation: func(input, output interface{}) string {
		inp := input.(casInput)
		out := output.(casOutput)

		switch inp.op {
		case STRING_REGISTER_GET:
			var r string
			if out.notFound {
				r = "<not found>"
			} else {
				r = fmt.Sprintf("'%v'", out.valueCur)
			}
			return fmt.Sprintf("get() -> %v", r)
		case STRING_REGISTER_PUT:
			return fmt.Sprintf("put('%v')", inp.newString)

		case STRING_REGISTER_CAS:

			if out.swapped {
				return fmt.Sprintf("CAS(ok: '%v' ->'%v')", inp.oldString, inp.newString)
			}
			return fmt.Sprintf("CAS(rejected:old '%v' != cur '%v')", inp.oldString, out.valueCur)
		}
		panic(fmt.Sprintf("invalid inp.op! '%v'", int(inp.op)))
		return "<invalid>" // unreachable
	},
}

// dump as html to visualize in browser.
func writeToDiskNonLinzFuzzEvents(t *testing.T, evs []porc.Event) {

	res, info := porc.CheckEventsVerbose(stringCasModel, evs, 0)
	if res != porc.Illegal {
		t.Fatalf("expected output %v, got output %v", porc.Illegal, res)
	}
	nm := fmt.Sprintf("red.nonlinz.%v.%03d.html", t.Name(), 0)
	for i := 1; fileExists(nm) && i < 1000; i++ {
		nm = fmt.Sprintf("red.nonlinz.%v.%03d.html", t.Name(), i)
	}
	fmt.Printf("writing out non-linearizable evs history '%v'\n", nm)
	fd, err := os.Create(nm)
	if err != nil {
		panic(err)
	}
	defer fd.Close()

	err = porc.Visualize(stringCasModel, info, fd) // panic: runtime error: index out of range [3] with length 3
	if err != nil {
		t.Fatalf("evs visualization failed: %v", err)
	}
	t.Logf("wrote evs visualization to %s", fd.Name())
}

func fileExists(name string) bool {
	fi, err := os.Stat(name)
	if err != nil {
		return false
	}
	if fi.IsDir() {
		return false
	}
	return true
}

var eventTestData = []porc.Event{
	/* 00: */ porc.Event{
		ClientId: 0,
		Kind:     porc.CallEvent,
		Id:       2,
		Value:    casInput{op: STRING_REGISTER_CAS, oldString: "", newString: "19"},
	},
	/* 01: */ porc.Event{
		ClientId: 0,
		Kind:     porc.ReturnEvent,
		Id:       2,
		Value: casOutput{
			id:       2,
			op:       STRING_REGISTER_CAS,
			valueCur: "19",
			notFound: false,
			swapped:  true,
			// -- from input --
			oldString: "",
			newString: "19",
		},
	},
	/* 02: */ porc.Event{
		ClientId: 1,
		Kind:     porc.CallEvent,
		Id:       5,
		Value:    casInput{op: STRING_REGISTER_GET},
	},
	/* 03: */ porc.Event{
		ClientId: 1,
		Kind:     porc.ReturnEvent,
		Id:       5,
		Value: casOutput{
			id:       5,
			op:       STRING_REGISTER_GET,
			valueCur: "19",
			notFound: false,
		},
	},
	/* 04: */ porc.Event{
		ClientId: 1,
		Kind:     porc.CallEvent,
		Id:       6,
		Value:    casInput{op: STRING_REGISTER_CAS, oldString: "19", newString: "15"},
	},
	/* 05: */ porc.Event{
		ClientId: 1,
		Kind:     porc.CallEvent,
		Id:       7,
		Value:    casInput{op: STRING_REGISTER_CAS, oldString: "19", newString: "15"},
	},
	/* 06: */ porc.Event{
		ClientId: 1,
		Kind:     porc.ReturnEvent,
		Id:       7,
		Value: casOutput{
			id:       7,
			op:       STRING_REGISTER_CAS,
			valueCur: "15",
			notFound: false,
			swapped:  true,
			// -- from input --
			oldString: "19",
			newString: "15",
		},
	},
}
