package porcupine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

type intState int

func (i intState) Clone() intState {
	return i
}

func (i intState) Equals(otherState intState) bool {
	return i == otherState
}

func (i intState) String() string {
	return fmt.Sprintf("%d", i)
}

type intSliceState []int

func (i intSliceState) Clone() intSliceState {
	ix := make([]int, 0, len(i))
	for _, i := range i {
		ix = append(ix, i)
	}
	return ix
}

func (i intSliceState) Equals(otherState intSliceState) bool {
	return reflect.DeepEqual(i, otherState)
}

func (i intSliceState) String() string {
	return ""
}

type mapState map[string]string

func (m mapState) Clone() mapState {
	m2 := make(map[string]string)
	for k, v := range m {
		m2[k] = v
	}
	return m2
}

func (m mapState) Equals(otherState mapState) bool {
	return reflect.DeepEqual(m, otherState)
}

func (m mapState) String() string {
	return ""
}

type stringState string

func (i stringState) Clone() stringState {
	return i
}

func (i stringState) Equals(otherState stringState) bool {
	return i == otherState
}

func (i stringState) String() string {
	return string(i)
}

type registerInput struct {
	op    bool // false = put, true = get
	value int
}

// a sequential specification of a register
var registerModel = Model[intState, registerInput, int]{
	Init: func() intState { return intState(0) },
	// step function: takes a state, input, and output, and returns whether it
	// was a legal operation, along with a new state
	Step: func(state intState, regInput registerInput, output int) (bool, intState) {
		if regInput.op == false {
			return true, intState(regInput.value) // always ok to execute a put
		} else {
			readCorrectValue := output == int(state)
			return readCorrectValue, state // state is unchanged
		}
	},
	DescribeOperation: func(inp registerInput, output int) string {
		switch inp.op {
		case true:
			return fmt.Sprintf("get() -> '%d'", output)
		case false:
			return fmt.Sprintf("put('%d')", inp.value)
		}
		return "<invalid>" // unreachable
	},
}

func TestRegisterModel(t *testing.T) {
	// examples taken from http://nil.csail.mit.edu/6.824/2017/quizzes/q2-17-ans.pdf
	// section VII

	ops := []Operation[registerInput, int]{
		{0, registerInput{false, 100}, 0, 0, 100},
		{1, registerInput{true, 0}, 25, 100, 75},
		{2, registerInput{true, 0}, 30, 0, 60},
	}
	res := CheckOperations(registerModel, ops)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	// same example as above, but with Event
	events := []Event[registerInput, int]{
		{0, CallEvent, registerInput{false, 100}, 0},
		{1, CallEvent, registerInput{true, 0}, 1},
		{2, CallEvent, registerInput{true, 0}, 2},
		{2, ReturnEvent, 0, 2},
		{1, ReturnEvent, 100, 1},
		{0, ReturnEvent, 0, 0},
	}
	res = CheckEvents(registerModel, events)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	ops = []Operation[registerInput, int]{
		{0, registerInput{false, 200}, 0, 0, 100},
		{1, registerInput{true, 0}, 10, 200, 30},
		{2, registerInput{true, 0}, 40, 0, 90},
	}
	res = CheckOperations(registerModel, ops)
	if res != false {
		t.Fatal("expected operations to not be linearizable")
	}

	// same example as above, but with Event
	events = []Event[registerInput, int]{
		{0, CallEvent, registerInput{false, 200}, 0},
		{1, CallEvent, registerInput{true, 0}, 1},
		{1, ReturnEvent, 200, 1},
		{2, CallEvent, registerInput{true, 0}, 2},
		{2, ReturnEvent, 0, 2},
		{0, ReturnEvent, 0, 0},
	}
	res = CheckEvents(registerModel, events)
	if res != false {
		t.Fatal("expected operations to not be linearizable")
	}
}

func TestZeroDuration(t *testing.T) {
	ops := []Operation[registerInput, int]{
		{0, registerInput{false, 100}, 0, 0, 100},
		{1, registerInput{true, 0}, 25, 100, 75},
		{2, registerInput{true, 0}, 30, 0, 30},
		{3, registerInput{true, 0}, 30, 0, 30},
	}
	res, info := CheckOperationsVerbose(registerModel, ops, 0)
	if res != Ok {
		t.Fatal("expected operations to be linearizable")
	}

	visualizeTempFile(t, registerModel, info)

	ops = []Operation[registerInput, int]{
		{0, registerInput{false, 200}, 0, 0, 100},
		{1, registerInput{true, 0}, 10, 200, 10},
		{2, registerInput{true, 0}, 10, 200, 10},
		{3, registerInput{true, 0}, 40, 0, 90},
	}
	res, _ = CheckOperationsVerbose(registerModel, ops, 0)
	if res != Illegal {
		t.Fatal("expected operations to not be linearizable")
	}
}

type etcdInput struct {
	op   uint8 // 0 => read, 1 => write, 2 => cas
	arg1 int   // used for write, or for CAS from argument
	arg2 int   // used for CAS to argument
}

type etcdOutput struct {
	ok      bool // used for CAS
	exists  bool // used for read
	value   int  // used for read
	unknown bool // used when operation times out
}

var etcdModel = Model[intState, etcdInput, etcdOutput]{
	Init: func() intState { return intState(-1000000) }, // -1000000 corresponds with nil
	Step: func(state intState, inp etcdInput, out etcdOutput) (bool, intState) {
		st := int(state)
		if inp.op == 0 {
			// read
			ok := (out.exists == false && st == -1000000) || (out.exists == true && st == out.value) || out.unknown
			return ok, state
		} else if inp.op == 1 {
			// write
			return true, intState(inp.arg1)
		} else {
			// cas
			ok := (inp.arg1 == st && out.ok) || (inp.arg1 != st && !out.ok) || out.unknown
			result := st
			if inp.arg1 == st {
				result = inp.arg2
			}
			return ok, intState(result)
		}
	},
	DescribeOperation: func(inp etcdInput, out etcdOutput) string {
		switch inp.op {
		case 0:
			var read string
			if out.exists {
				read = fmt.Sprintf("%d", out.value)
			} else {
				read = "null"
			}
			return fmt.Sprintf("read() -> %s", read)
		case 1:
			return fmt.Sprintf("write(%d)", inp.arg1)
		case 2:
			var ret string
			if out.unknown {
				ret = "unknown"
			} else if out.ok {
				ret = "ok"
			} else {
				ret = "fail"
			}
			return fmt.Sprintf("cas(%d, %d) -> %s", inp.arg1, inp.arg2, ret)

		default:
			return "<invalid>"
		}
	},
}

func parseJepsenLog(filename string) []Event[etcdInput, etcdOutput] {
	file, err := os.Open(filename)
	if err != nil {
		panic("can't open file")
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	invokeRead, _ := regexp.Compile(`^INFO\s+jepsen\.util\s+-\s+(\d+)\s+:invoke\s+:read\s+nil$`)
	invokeWrite, _ := regexp.Compile(`^INFO\s+jepsen\.util\s+-\s+(\d+)\s+:invoke\s+:write\s+(\d+)$`)
	invokeCas, _ := regexp.Compile(`^INFO\s+jepsen\.util\s+-\s+(\d+)\s+:invoke\s+:cas\s+\[(\d+)\s+(\d+)\]$`)
	returnRead, _ := regexp.Compile(`^INFO\s+jepsen\.util\s+-\s+(\d+)\s+:ok\s+:read\s+(nil|\d+)$`)
	returnWrite, _ := regexp.Compile(`^INFO\s+jepsen\.util\s+-\s+(\d+)\s+:ok\s+:write\s+(\d+)$`)
	returnCas, _ := regexp.Compile(`^INFO\s+jepsen\.util\s+-\s+(\d+)\s+:(ok|fail)\s+:cas\s+\[(\d+)\s+(\d+)\]$`)
	timeoutRead, _ := regexp.Compile(`^INFO\s+jepsen\.util\s+-\s+(\d+)\s+:fail\s+:read\s+:timed-out$`)

	var events []Event[etcdInput, etcdOutput] = nil

	id := 0
	procIdMap := make(map[int]int)
	for {
		lineBytes, isPrefix, err := reader.ReadLine()
		if err == io.EOF {
			break
		} else if err != nil {
			panic("error while reading file: " + err.Error())
		}
		if isPrefix {
			panic("can't handle isPrefix")
		}
		line := string(lineBytes)

		switch {
		case invokeRead.MatchString(line):
			args := invokeRead.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			events = append(events, Event[etcdInput, etcdOutput]{proc, CallEvent, etcdInput{op: 0}, id})
			procIdMap[proc] = id
			id++
		case invokeWrite.MatchString(line):
			args := invokeWrite.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			value, _ := strconv.Atoi(args[2])
			events = append(events, Event[etcdInput, etcdOutput]{proc, CallEvent, etcdInput{op: 1, arg1: value}, id})
			procIdMap[proc] = id
			id++
		case invokeCas.MatchString(line):
			args := invokeCas.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			from, _ := strconv.Atoi(args[2])
			to, _ := strconv.Atoi(args[3])
			events = append(events, Event[etcdInput, etcdOutput]{proc, CallEvent, etcdInput{op: 2, arg1: from, arg2: to}, id})
			procIdMap[proc] = id
			id++
		case returnRead.MatchString(line):
			args := returnRead.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			exists := false
			value := 0
			if args[2] != "nil" {
				exists = true
				value, _ = strconv.Atoi(args[2])
			}
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			events = append(events, Event[etcdInput, etcdOutput]{proc, ReturnEvent, etcdOutput{exists: exists, value: value}, matchId})
		case returnWrite.MatchString(line):
			args := returnWrite.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			events = append(events, Event[etcdInput, etcdOutput]{proc, ReturnEvent, etcdOutput{}, matchId})
		case returnCas.MatchString(line):
			args := returnCas.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			events = append(events, Event[etcdInput, etcdOutput]{proc, ReturnEvent, etcdOutput{ok: args[2] == "ok"}, matchId})
		case timeoutRead.MatchString(line):
			// timing out a read and then continuing operations is fine
			// we could just delete the read from the events, but we do this the lazy way
			args := timeoutRead.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			// okay to put the return here in the history
			events = append(events, Event[etcdInput, etcdOutput]{proc, ReturnEvent, etcdOutput{unknown: true}, matchId})
		}
	}

	for proc, matchId := range procIdMap {
		events = append(events, Event[etcdInput, etcdOutput]{proc, ReturnEvent, etcdOutput{unknown: true}, matchId})
	}

	return events
}

func checkJepsen(t *testing.T, logNum int, correct bool) {
	events := parseJepsenLog(fmt.Sprintf("test_data/jepsen/etcd_%03d.log", logNum))
	res := CheckEvents(etcdModel, events)
	if res != correct {
		t.Fatalf("expected output %t, got output %t", correct, res)
	}
}

func TestEtcdJepsen000(t *testing.T) {
	checkJepsen(t, 0, false)
}

func TestEtcdJepsen001(t *testing.T) {
	checkJepsen(t, 1, false)
}

func TestEtcdJepsen002(t *testing.T) {
	checkJepsen(t, 2, true)
}

func TestEtcdJepsen003(t *testing.T) {
	checkJepsen(t, 3, false)
}

func TestEtcdJepsen004(t *testing.T) {
	checkJepsen(t, 4, false)
}

func TestEtcdJepsen005(t *testing.T) {
	checkJepsen(t, 5, true)
}

func TestEtcdJepsen006(t *testing.T) {
	checkJepsen(t, 6, false)
}

func TestEtcdJepsen007(t *testing.T) {
	checkJepsen(t, 7, true)
}

func TestEtcdJepsen008(t *testing.T) {
	checkJepsen(t, 8, false)
}

func TestEtcdJepsen009(t *testing.T) {
	checkJepsen(t, 9, false)
}

func TestEtcdJepsen010(t *testing.T) {
	checkJepsen(t, 10, false)
}

func TestEtcdJepsen011(t *testing.T) {
	checkJepsen(t, 11, false)
}

func TestEtcdJepsen012(t *testing.T) {
	checkJepsen(t, 12, false)
}

func TestEtcdJepsen013(t *testing.T) {
	checkJepsen(t, 13, false)
}

func TestEtcdJepsen014(t *testing.T) {
	checkJepsen(t, 14, false)
}

func TestEtcdJepsen015(t *testing.T) {
	checkJepsen(t, 15, false)
}

func TestEtcdJepsen016(t *testing.T) {
	checkJepsen(t, 16, false)
}

func TestEtcdJepsen017(t *testing.T) {
	checkJepsen(t, 17, false)
}

func TestEtcdJepsen018(t *testing.T) {
	checkJepsen(t, 18, true)
}

func TestEtcdJepsen019(t *testing.T) {
	checkJepsen(t, 19, false)
}

func TestEtcdJepsen020(t *testing.T) {
	checkJepsen(t, 20, false)
}

func TestEtcdJepsen021(t *testing.T) {
	checkJepsen(t, 21, false)
}

func TestEtcdJepsen022(t *testing.T) {
	checkJepsen(t, 22, false)
}

func TestEtcdJepsen023(t *testing.T) {
	checkJepsen(t, 23, false)
}

func TestEtcdJepsen024(t *testing.T) {
	checkJepsen(t, 24, false)
}

func TestEtcdJepsen025(t *testing.T) {
	checkJepsen(t, 25, true)
}

func TestEtcdJepsen026(t *testing.T) {
	checkJepsen(t, 26, false)
}

func TestEtcdJepsen027(t *testing.T) {
	checkJepsen(t, 27, false)
}

func TestEtcdJepsen028(t *testing.T) {
	checkJepsen(t, 28, false)
}

func TestEtcdJepsen029(t *testing.T) {
	checkJepsen(t, 29, false)
}

func TestEtcdJepsen030(t *testing.T) {
	checkJepsen(t, 30, false)
}

func TestEtcdJepsen031(t *testing.T) {
	checkJepsen(t, 31, true)
}

func TestEtcdJepsen032(t *testing.T) {
	checkJepsen(t, 32, false)
}

func TestEtcdJepsen033(t *testing.T) {
	checkJepsen(t, 33, false)
}

func TestEtcdJepsen034(t *testing.T) {
	checkJepsen(t, 34, false)
}

func TestEtcdJepsen035(t *testing.T) {
	checkJepsen(t, 35, false)
}

func TestEtcdJepsen036(t *testing.T) {
	checkJepsen(t, 36, false)
}

func TestEtcdJepsen037(t *testing.T) {
	checkJepsen(t, 37, false)
}

func TestEtcdJepsen038(t *testing.T) {
	checkJepsen(t, 38, true)
}

func TestEtcdJepsen039(t *testing.T) {
	checkJepsen(t, 39, false)
}

func TestEtcdJepsen040(t *testing.T) {
	checkJepsen(t, 40, false)
}

func TestEtcdJepsen041(t *testing.T) {
	checkJepsen(t, 41, false)
}

func TestEtcdJepsen042(t *testing.T) {
	checkJepsen(t, 42, false)
}

func TestEtcdJepsen043(t *testing.T) {
	checkJepsen(t, 43, false)
}

func TestEtcdJepsen044(t *testing.T) {
	checkJepsen(t, 44, false)
}

func TestEtcdJepsen045(t *testing.T) {
	checkJepsen(t, 45, true)
}

func TestEtcdJepsen046(t *testing.T) {
	checkJepsen(t, 46, false)
}

func TestEtcdJepsen047(t *testing.T) {
	checkJepsen(t, 47, false)
}

func TestEtcdJepsen048(t *testing.T) {
	checkJepsen(t, 48, true)
}

func TestEtcdJepsen049(t *testing.T) {
	checkJepsen(t, 49, true)
}

func TestEtcdJepsen050(t *testing.T) {
	checkJepsen(t, 50, false)
}

func TestEtcdJepsen051(t *testing.T) {
	checkJepsen(t, 51, true)
}

func TestEtcdJepsen052(t *testing.T) {
	checkJepsen(t, 52, false)
}

func TestEtcdJepsen053(t *testing.T) {
	checkJepsen(t, 53, true)
}

func TestEtcdJepsen054(t *testing.T) {
	checkJepsen(t, 54, false)
}

func TestEtcdJepsen055(t *testing.T) {
	checkJepsen(t, 55, false)
}

func TestEtcdJepsen056(t *testing.T) {
	checkJepsen(t, 56, true)
}

func TestEtcdJepsen057(t *testing.T) {
	checkJepsen(t, 57, false)
}

func TestEtcdJepsen058(t *testing.T) {
	checkJepsen(t, 58, false)
}

func TestEtcdJepsen059(t *testing.T) {
	checkJepsen(t, 59, false)
}

func TestEtcdJepsen060(t *testing.T) {
	checkJepsen(t, 60, false)
}

func TestEtcdJepsen061(t *testing.T) {
	checkJepsen(t, 61, false)
}

func TestEtcdJepsen062(t *testing.T) {
	checkJepsen(t, 62, false)
}

func TestEtcdJepsen063(t *testing.T) {
	checkJepsen(t, 63, false)
}

func TestEtcdJepsen064(t *testing.T) {
	checkJepsen(t, 64, false)
}

func TestEtcdJepsen065(t *testing.T) {
	checkJepsen(t, 65, false)
}

func TestEtcdJepsen066(t *testing.T) {
	checkJepsen(t, 66, false)
}

func TestEtcdJepsen067(t *testing.T) {
	checkJepsen(t, 67, true)
}

func TestEtcdJepsen068(t *testing.T) {
	checkJepsen(t, 68, false)
}

func TestEtcdJepsen069(t *testing.T) {
	checkJepsen(t, 69, false)
}

func TestEtcdJepsen070(t *testing.T) {
	checkJepsen(t, 70, false)
}

func TestEtcdJepsen071(t *testing.T) {
	checkJepsen(t, 71, false)
}

func TestEtcdJepsen072(t *testing.T) {
	checkJepsen(t, 72, false)
}

func TestEtcdJepsen073(t *testing.T) {
	checkJepsen(t, 73, false)
}

func TestEtcdJepsen074(t *testing.T) {
	checkJepsen(t, 74, false)
}

func TestEtcdJepsen075(t *testing.T) {
	checkJepsen(t, 75, true)
}

func TestEtcdJepsen076(t *testing.T) {
	checkJepsen(t, 76, true)
}

func TestEtcdJepsen077(t *testing.T) {
	checkJepsen(t, 77, false)
}

func TestEtcdJepsen078(t *testing.T) {
	checkJepsen(t, 78, false)
}

func TestEtcdJepsen079(t *testing.T) {
	checkJepsen(t, 79, false)
}

func TestEtcdJepsen080(t *testing.T) {
	checkJepsen(t, 80, true)
}

func TestEtcdJepsen081(t *testing.T) {
	checkJepsen(t, 81, false)
}

func TestEtcdJepsen082(t *testing.T) {
	checkJepsen(t, 82, false)
}

func TestEtcdJepsen083(t *testing.T) {
	checkJepsen(t, 83, false)
}

func TestEtcdJepsen084(t *testing.T) {
	checkJepsen(t, 84, false)
}

func TestEtcdJepsen085(t *testing.T) {
	checkJepsen(t, 85, false)
}

func TestEtcdJepsen086(t *testing.T) {
	checkJepsen(t, 86, false)
}

func TestEtcdJepsen087(t *testing.T) {
	checkJepsen(t, 87, true)
}

func TestEtcdJepsen088(t *testing.T) {
	checkJepsen(t, 88, false)
}

func TestEtcdJepsen089(t *testing.T) {
	checkJepsen(t, 89, false)
}

func TestEtcdJepsen090(t *testing.T) {
	checkJepsen(t, 90, false)
}

func TestEtcdJepsen091(t *testing.T) {
	checkJepsen(t, 91, false)
}

func TestEtcdJepsen092(t *testing.T) {
	checkJepsen(t, 92, true)
}

func TestEtcdJepsen093(t *testing.T) {
	checkJepsen(t, 93, false)
}

func TestEtcdJepsen094(t *testing.T) {
	checkJepsen(t, 94, false)
}

// etcd cluster failed to start up in test 95

func TestEtcdJepsen096(t *testing.T) {
	checkJepsen(t, 96, false)
}

func TestEtcdJepsen097(t *testing.T) {
	checkJepsen(t, 97, false)
}

func TestEtcdJepsen098(t *testing.T) {
	checkJepsen(t, 98, true)
}

func TestEtcdJepsen099(t *testing.T) {
	checkJepsen(t, 99, false)
}

func TestEtcdJepsen100(t *testing.T) {
	checkJepsen(t, 100, true)
}

func TestEtcdJepsen101(t *testing.T) {
	checkJepsen(t, 101, true)
}

func TestEtcdJepsen102(t *testing.T) {
	checkJepsen(t, 102, true)
}

func benchJepsen(b *testing.B, logNum int, correct bool) {
	events := parseJepsenLog(fmt.Sprintf("test_data/jepsen/etcd_%03d.log", logNum))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := CheckEvents(etcdModel, events)
		if res != correct {
			b.Fatalf("expected output %t, got output %t", correct, res)
		}
	}
}

func BenchmarkEtcdJepsen000(b *testing.B) {
	benchJepsen(b, 0, false)
}

func BenchmarkEtcdJepsen001(b *testing.B) {
	benchJepsen(b, 1, false)
}

func BenchmarkEtcdJepsen002(b *testing.B) {
	benchJepsen(b, 2, true)
}

func BenchmarkEtcdJepsen003(b *testing.B) {
	benchJepsen(b, 3, false)
}

func BenchmarkEtcdJepsen004(b *testing.B) {
	benchJepsen(b, 4, false)
}

func BenchmarkEtcdJepsen005(b *testing.B) {
	benchJepsen(b, 5, true)
}

func BenchmarkEtcdJepsen006(b *testing.B) {
	benchJepsen(b, 6, false)
}

func BenchmarkEtcdJepsen007(b *testing.B) {
	benchJepsen(b, 7, true)
}

func BenchmarkEtcdJepsen008(b *testing.B) {
	benchJepsen(b, 8, false)
}

func BenchmarkEtcdJepsen009(b *testing.B) {
	benchJepsen(b, 9, false)
}

func BenchmarkEtcdJepsen010(b *testing.B) {
	benchJepsen(b, 10, false)
}

func BenchmarkEtcdJepsen011(b *testing.B) {
	benchJepsen(b, 11, false)
}

func BenchmarkEtcdJepsen012(b *testing.B) {
	benchJepsen(b, 12, false)
}

func BenchmarkEtcdJepsen013(b *testing.B) {
	benchJepsen(b, 13, false)
}

func BenchmarkEtcdJepsen014(b *testing.B) {
	benchJepsen(b, 14, false)
}

func BenchmarkEtcdJepsen015(b *testing.B) {
	benchJepsen(b, 15, false)
}

func BenchmarkEtcdJepsen016(b *testing.B) {
	benchJepsen(b, 16, false)
}

func BenchmarkEtcdJepsen017(b *testing.B) {
	benchJepsen(b, 17, false)
}

func BenchmarkEtcdJepsen018(b *testing.B) {
	benchJepsen(b, 18, true)
}

func BenchmarkEtcdJepsen019(b *testing.B) {
	benchJepsen(b, 19, false)
}

func BenchmarkEtcdJepsen020(b *testing.B) {
	benchJepsen(b, 20, false)
}

func BenchmarkEtcdJepsen021(b *testing.B) {
	benchJepsen(b, 21, false)
}

func BenchmarkEtcdJepsen022(b *testing.B) {
	benchJepsen(b, 22, false)
}

func BenchmarkEtcdJepsen023(b *testing.B) {
	benchJepsen(b, 23, false)
}

func BenchmarkEtcdJepsen024(b *testing.B) {
	benchJepsen(b, 24, false)
}

func BenchmarkEtcdJepsen025(b *testing.B) {
	benchJepsen(b, 25, true)
}

func BenchmarkEtcdJepsen026(b *testing.B) {
	benchJepsen(b, 26, false)
}

func BenchmarkEtcdJepsen027(b *testing.B) {
	benchJepsen(b, 27, false)
}

func BenchmarkEtcdJepsen028(b *testing.B) {
	benchJepsen(b, 28, false)
}

func BenchmarkEtcdJepsen029(b *testing.B) {
	benchJepsen(b, 29, false)
}

func BenchmarkEtcdJepsen030(b *testing.B) {
	benchJepsen(b, 30, false)
}

func BenchmarkEtcdJepsen031(b *testing.B) {
	benchJepsen(b, 31, true)
}

func BenchmarkEtcdJepsen032(b *testing.B) {
	benchJepsen(b, 32, false)
}

func BenchmarkEtcdJepsen033(b *testing.B) {
	benchJepsen(b, 33, false)
}

func BenchmarkEtcdJepsen034(b *testing.B) {
	benchJepsen(b, 34, false)
}

func BenchmarkEtcdJepsen035(b *testing.B) {
	benchJepsen(b, 35, false)
}

func BenchmarkEtcdJepsen036(b *testing.B) {
	benchJepsen(b, 36, false)
}

func BenchmarkEtcdJepsen037(b *testing.B) {
	benchJepsen(b, 37, false)
}

func BenchmarkEtcdJepsen038(b *testing.B) {
	benchJepsen(b, 38, true)
}

func BenchmarkEtcdJepsen039(b *testing.B) {
	benchJepsen(b, 39, false)
}

func BenchmarkEtcdJepsen040(b *testing.B) {
	benchJepsen(b, 40, false)
}

func BenchmarkEtcdJepsen041(b *testing.B) {
	benchJepsen(b, 41, false)
}

func BenchmarkEtcdJepsen042(b *testing.B) {
	benchJepsen(b, 42, false)
}

func BenchmarkEtcdJepsen043(b *testing.B) {
	benchJepsen(b, 43, false)
}

func BenchmarkEtcdJepsen044(b *testing.B) {
	benchJepsen(b, 44, false)
}

func BenchmarkEtcdJepsen045(b *testing.B) {
	benchJepsen(b, 45, true)
}

func BenchmarkEtcdJepsen046(b *testing.B) {
	benchJepsen(b, 46, false)
}

func BenchmarkEtcdJepsen047(b *testing.B) {
	benchJepsen(b, 47, false)
}

func BenchmarkEtcdJepsen048(b *testing.B) {
	benchJepsen(b, 48, true)
}

func BenchmarkEtcdJepsen049(b *testing.B) {
	benchJepsen(b, 49, true)
}

func BenchmarkEtcdJepsen050(b *testing.B) {
	benchJepsen(b, 50, false)
}

func BenchmarkEtcdJepsen051(b *testing.B) {
	benchJepsen(b, 51, true)
}

func BenchmarkEtcdJepsen052(b *testing.B) {
	benchJepsen(b, 52, false)
}

func BenchmarkEtcdJepsen053(b *testing.B) {
	benchJepsen(b, 53, true)
}

func BenchmarkEtcdJepsen054(b *testing.B) {
	benchJepsen(b, 54, false)
}

func BenchmarkEtcdJepsen055(b *testing.B) {
	benchJepsen(b, 55, false)
}

func BenchmarkEtcdJepsen056(b *testing.B) {
	benchJepsen(b, 56, true)
}

func BenchmarkEtcdJepsen057(b *testing.B) {
	benchJepsen(b, 57, false)
}

func BenchmarkEtcdJepsen058(b *testing.B) {
	benchJepsen(b, 58, false)
}

func BenchmarkEtcdJepsen059(b *testing.B) {
	benchJepsen(b, 59, false)
}

func BenchmarkEtcdJepsen060(b *testing.B) {
	benchJepsen(b, 60, false)
}

func BenchmarkEtcdJepsen061(b *testing.B) {
	benchJepsen(b, 61, false)
}

func BenchmarkEtcdJepsen062(b *testing.B) {
	benchJepsen(b, 62, false)
}

func BenchmarkEtcdJepsen063(b *testing.B) {
	benchJepsen(b, 63, false)
}

func BenchmarkEtcdJepsen064(b *testing.B) {
	benchJepsen(b, 64, false)
}

func BenchmarkEtcdJepsen065(b *testing.B) {
	benchJepsen(b, 65, false)
}

func BenchmarkEtcdJepsen066(b *testing.B) {
	benchJepsen(b, 66, false)
}

func BenchmarkEtcdJepsen067(b *testing.B) {
	benchJepsen(b, 67, true)
}

func BenchmarkEtcdJepsen068(b *testing.B) {
	benchJepsen(b, 68, false)
}

func BenchmarkEtcdJepsen069(b *testing.B) {
	benchJepsen(b, 69, false)
}

func BenchmarkEtcdJepsen070(b *testing.B) {
	benchJepsen(b, 70, false)
}

func BenchmarkEtcdJepsen071(b *testing.B) {
	benchJepsen(b, 71, false)
}

func BenchmarkEtcdJepsen072(b *testing.B) {
	benchJepsen(b, 72, false)
}

func BenchmarkEtcdJepsen073(b *testing.B) {
	benchJepsen(b, 73, false)
}

func BenchmarkEtcdJepsen074(b *testing.B) {
	benchJepsen(b, 74, false)
}

func BenchmarkEtcdJepsen075(b *testing.B) {
	benchJepsen(b, 75, true)
}

func BenchmarkEtcdJepsen076(b *testing.B) {
	benchJepsen(b, 76, true)
}

func BenchmarkEtcdJepsen077(b *testing.B) {
	benchJepsen(b, 77, false)
}

func BenchmarkEtcdJepsen078(b *testing.B) {
	benchJepsen(b, 78, false)
}

func BenchmarkEtcdJepsen079(b *testing.B) {
	benchJepsen(b, 79, false)
}

func BenchmarkEtcdJepsen080(b *testing.B) {
	benchJepsen(b, 80, true)
}

func BenchmarkEtcdJepsen081(b *testing.B) {
	benchJepsen(b, 81, false)
}

func BenchmarkEtcdJepsen082(b *testing.B) {
	benchJepsen(b, 82, false)
}

func BenchmarkEtcdJepsen083(b *testing.B) {
	benchJepsen(b, 83, false)
}

func BenchmarkEtcdJepsen084(b *testing.B) {
	benchJepsen(b, 84, false)
}

func BenchmarkEtcdJepsen085(b *testing.B) {
	benchJepsen(b, 85, false)
}

func BenchmarkEtcdJepsen086(b *testing.B) {
	benchJepsen(b, 86, false)
}

func BenchmarkEtcdJepsen087(b *testing.B) {
	benchJepsen(b, 87, true)
}

func BenchmarkEtcdJepsen088(b *testing.B) {
	benchJepsen(b, 88, false)
}

func BenchmarkEtcdJepsen089(b *testing.B) {
	benchJepsen(b, 89, false)
}

func BenchmarkEtcdJepsen090(b *testing.B) {
	benchJepsen(b, 90, false)
}

func BenchmarkEtcdJepsen091(b *testing.B) {
	benchJepsen(b, 91, false)
}

func BenchmarkEtcdJepsen092(b *testing.B) {
	benchJepsen(b, 92, true)
}

func BenchmarkEtcdJepsen093(b *testing.B) {
	benchJepsen(b, 93, false)
}

func BenchmarkEtcdJepsen094(b *testing.B) {
	benchJepsen(b, 94, false)
}

// etcd cluster failed to start up in test 95

func BenchmarkEtcdJepsen096(b *testing.B) {
	benchJepsen(b, 96, false)
}

func BenchmarkEtcdJepsen097(b *testing.B) {
	benchJepsen(b, 97, false)
}

func BenchmarkEtcdJepsen098(b *testing.B) {
	benchJepsen(b, 98, true)
}

func BenchmarkEtcdJepsen099(b *testing.B) {
	benchJepsen(b, 99, false)
}

func BenchmarkEtcdJepsen100(b *testing.B) {
	benchJepsen(b, 100, true)
}

func BenchmarkEtcdJepsen101(b *testing.B) {
	benchJepsen(b, 101, true)
}

func BenchmarkEtcdJepsen102(b *testing.B) {
	benchJepsen(b, 102, true)
}

type kvInput struct {
	op    uint8 // 0 => get, 1 => put, 2 => append
	key   string
	value string
}

type kvOutput struct {
	value string
}

var kvModel = Model[stringState, kvInput, kvOutput]{
	Partition: func(history []Operation[kvInput, kvOutput]) [][]Operation[kvInput, kvOutput] {
		m := make(map[string][]Operation[kvInput, kvOutput])
		for _, v := range history {
			key := v.Input.key
			m[key] = append(m[key], v)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ret := make([][]Operation[kvInput, kvOutput], 0, len(keys))
		for _, k := range keys {
			ret = append(ret, m[k])
		}
		return ret
	},
	PartitionEvent: func(history []Event[kvInput, kvOutput]) [][]Event[kvInput, kvOutput] {
		m := make(map[string][]Event[kvInput, kvOutput])
		match := make(map[int]string) // id -> key
		for _, v := range history {
			if v.Kind == CallEvent {
				key := v.Value.(kvInput).key
				m[key] = append(m[key], v)
				match[v.Id] = key
			} else {
				key := match[v.Id]
				m[key] = append(m[key], v)
			}
		}
		var ret [][]Event[kvInput, kvOutput]
		for _, v := range m {
			ret = append(ret, v)
		}
		return ret
	},
	Init: func() stringState { return "" },
	Step: func(state stringState, inp kvInput, out kvOutput) (bool, stringState) {
		st := string(state)
		if inp.op == 0 {
			// get
			return out.value == st, state
		} else if inp.op == 1 {
			// put
			return true, stringState(inp.value)
		} else {
			// append
			return true, stringState(st + inp.value)
		}
	},
	DescribeOperation: func(inp kvInput, out kvOutput) string {
		switch inp.op {
		case 0:
			return fmt.Sprintf("get('%s') -> '%s'", inp.key, out.value)
		case 1:
			return fmt.Sprintf("put('%s', '%s')", inp.key, inp.value)
		case 2:
			return fmt.Sprintf("append('%s', '%s')", inp.key, inp.value)
		default:
			return "<invalid>"
		}
	},
}

// uses a map[string]string to represent the state, and doesn't do partitioning
//
// this is a silly way to do things (it's way slower!) but good for
// demonstration, testing, and benchmark purposes
var kvNoPartitionModel = Model[mapState, kvInput, kvOutput]{
	Init: func() mapState { return mapState{} },
	Step: func(st mapState, inp kvInput, out kvOutput) (bool, mapState) {
		if inp.op == 0 {
			// get
			return out.value == st[inp.key], st
		} else if inp.op == 1 {
			// put
			st[inp.key] = inp.value
			return true, st
		} else {
			// append
			st[inp.key] = st[inp.key] + inp.value
			return true, st
		}
	},
}

func parseKvLog(filename string) []Event[kvInput, kvOutput] {
	file, err := os.Open(filename)
	if err != nil {
		panic("can't open file")
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	invokeGet, _ := regexp.Compile(`{:process (\d+), :type :invoke, :f :get, :key "(.*)", :value nil}`)
	invokePut, _ := regexp.Compile(`{:process (\d+), :type :invoke, :f :put, :key "(.*)", :value "(.*)"}`)
	invokeAppend, _ := regexp.Compile(`{:process (\d+), :type :invoke, :f :append, :key "(.*)", :value "(.*)"}`)
	returnGet, _ := regexp.Compile(`{:process (\d+), :type :ok, :f :get, :key ".*", :value "(.*)"}`)
	returnPut, _ := regexp.Compile(`{:process (\d+), :type :ok, :f :put, :key ".*", :value ".*"}`)
	returnAppend, _ := regexp.Compile(`{:process (\d+), :type :ok, :f :append, :key ".*", :value ".*"}`)

	var events []Event[kvInput, kvOutput] = nil

	id := 0
	procIdMap := make(map[int]int)
	for {
		lineBytes, isPrefix, err := reader.ReadLine()
		if err == io.EOF {
			break
		} else if err != nil {
			panic("error while reading file: " + err.Error())
		}
		if isPrefix {
			panic("can't handle isPrefix")
		}
		line := string(lineBytes)

		switch {
		case invokeGet.MatchString(line):
			args := invokeGet.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			events = append(events, Event[kvInput, kvOutput]{proc, CallEvent, kvInput{op: 0, key: args[2]}, id})
			procIdMap[proc] = id
			id++
		case invokePut.MatchString(line):
			args := invokePut.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			events = append(events, Event[kvInput, kvOutput]{proc, CallEvent, kvInput{op: 1, key: args[2], value: args[3]}, id})
			procIdMap[proc] = id
			id++
		case invokeAppend.MatchString(line):
			args := invokeAppend.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			events = append(events, Event[kvInput, kvOutput]{proc, CallEvent, kvInput{op: 2, key: args[2], value: args[3]}, id})
			procIdMap[proc] = id
			id++
		case returnGet.MatchString(line):
			args := returnGet.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			events = append(events, Event[kvInput, kvOutput]{proc, ReturnEvent, kvOutput{args[2]}, matchId})
		case returnPut.MatchString(line):
			args := returnPut.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			events = append(events, Event[kvInput, kvOutput]{proc, ReturnEvent, kvOutput{}, matchId})
		case returnAppend.MatchString(line):
			args := returnAppend.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			events = append(events, Event[kvInput, kvOutput]{proc, ReturnEvent, kvOutput{}, matchId})
		}
	}

	for proc, matchId := range procIdMap {
		events = append(events, Event[kvInput, kvOutput]{proc, ReturnEvent, kvOutput{}, matchId})
	}

	return events
}

func checkKvPartition(t *testing.T, logName string, correct bool) {
	events := parseKvLog(fmt.Sprintf("test_data/kv/%s.txt", logName))
	model := kvModel
	res := CheckEvents(model, events)
	if res != correct {
		t.Fatalf("expected output %t, got output %t", correct, res)
	}
}

func checkKvNoPartition(t *testing.T, logName string, correct bool) {
	events := parseKvLog(fmt.Sprintf("test_data/kv/%s.txt", logName))
	model := kvNoPartitionModel
	res := CheckEvents(model, events)
	if res != correct {
		t.Fatalf("expected output %t, got output %t", correct, res)
	}
}

func TestKv1ClientOk(t *testing.T) {
	checkKvPartition(t, "c01-ok", true)
}

func TestKv1ClientBad(t *testing.T) {
	checkKvPartition(t, "c01-bad", false)
}

func TestKv10ClientsOk(t *testing.T) {
	checkKvPartition(t, "c10-ok", true)
}

func TestKv10ClientsBad(t *testing.T) {
	checkKvPartition(t, "c10-bad", false)
}

func TestKv50ClientsOk(t *testing.T) {
	checkKvPartition(t, "c50-ok", true)
}

func TestKv50ClientsBad(t *testing.T) {
	checkKvPartition(t, "c50-bad", false)
}

func TestKvNoPartition1ClientOk(t *testing.T) {
	checkKvNoPartition(t, "c01-ok", true)
}

func TestKvNoPartition1ClientBad(t *testing.T) {
	checkKvNoPartition(t, "c01-bad", false)
}

// takes about 90 seconds to run
func TestKvNoPartition10ClientsOk(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testing in short mode")
	}
	checkKvNoPartition(t, "c10-ok", true)
}

// takes about 60 seconds to run
func TestKvNoPartition10ClientsBad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testing in short mode")
	}
	checkKvNoPartition(t, "c10-bad", false)
}

func benchKv(b *testing.B, logName string, correct bool) {
	events := parseKvLog(fmt.Sprintf("test_data/kv/%s.txt", logName))
	model := kvModel
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := CheckEvents(model, events)
		if res != correct {
			b.Fatalf("expected output %t, got output %t", correct, res)
		}
	}
}

func benchKvNoPartition(b *testing.B, logName string, correct bool) {
	events := parseKvLog(fmt.Sprintf("test_data/kv/%s.txt", logName))
	model := kvNoPartitionModel
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := CheckEvents(model, events)
		if res != correct {
			b.Fatalf("expected output %t, got output %t", correct, res)
		}
	}
}

func BenchmarkKv1ClientOk(b *testing.B) {
	benchKv(b, "c01-ok", true)
}

func BenchmarkKv1ClientBad(b *testing.B) {
	benchKv(b, "c01-bad", false)
}

func BenchmarkKv10ClientsOk(b *testing.B) {
	benchKv(b, "c10-ok", true)
}

func BenchmarkKv10ClientsBad(b *testing.B) {
	benchKv(b, "c10-bad", false)
}

func BenchmarkKv50ClientsOk(b *testing.B) {
	benchKv(b, "c50-ok", true)
}

func BenchmarkKv50ClientsBad(b *testing.B) {
	benchKv(b, "c50-bad", false)
}

func BenchmarkKvNoPartition1ClientOk(b *testing.B) {
	benchKvNoPartition(b, "c01-ok", true)
}

func BenchmarkKvNoPartition1ClientBad(b *testing.B) {
	benchKvNoPartition(b, "c01-bad", false)
}

// takes about 90 seconds to run
func BenchmarkKvNoPartition10ClientsOk(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}
	benchKvNoPartition(b, "c10-ok", true)
}

// takes about 60 seconds to run
func BenchmarkKvNoPartition10ClientsBad(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}
	benchKvNoPartition(b, "c10-bad", false)
}

func TestSetModel(t *testing.T) {

	// Set Model is from Jepsen/Knossos Set.
	// A set supports add and read operations, and we must ensure that
	// each read can't read duplicated or unknown values from the set

	// inputs
	type setInput struct {
		op    bool // false = read, true = write
		value int
	}

	// outputs
	type setOutput struct {
		values  []int // read
		unknown bool  // read
	}

	setModel := Model[intSliceState, setInput, setOutput]{
		Init: func() intSliceState { return []int{} },
		Step: func(state intSliceState, inp setInput, out setOutput) (bool, intSliceState) {
			st := []int(state)

			if inp.op == true {
				// always returns true for write
				index := sort.SearchInts(st, inp.value)
				if index >= len(st) || st[index] != inp.value {
					// value not in the set
					st = append(st, inp.value)
					sort.Ints(st)
				}
				return true, st
			}

			sort.Ints(out.values)
			return out.unknown || reflect.DeepEqual(st, out.values), out.values
		},
	}

	events := []Event[setInput, setOutput]{
		{0, CallEvent, setInput{true, 100}, 0},
		{1, CallEvent, setInput{true, 0}, 1},
		{2, CallEvent, setInput{false, 0}, 2},
		{2, ReturnEvent, setOutput{[]int{100}, false}, 2},
		{1, ReturnEvent, setOutput{}, 1},
		{0, ReturnEvent, setOutput{}, 0},
	}
	res := CheckEvents(setModel, events)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	events = []Event[setInput, setOutput]{
		{0, CallEvent, setInput{true, 100}, 0},
		{1, CallEvent, setInput{true, 110}, 1},
		{2, CallEvent, setInput{false, 0}, 2},
		{2, ReturnEvent, setOutput{[]int{100, 110}, false}, 2},
		{1, ReturnEvent, setOutput{}, 1},
		{0, ReturnEvent, setOutput{}, 0},
	}
	res = CheckEvents(setModel, events)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	events = []Event[setInput, setOutput]{
		{0, CallEvent, setInput{true, 100}, 0},
		{1, CallEvent, setInput{true, 110}, 1},
		{2, CallEvent, setInput{false, 0}, 2},
		{2, ReturnEvent, setOutput{[]int{}, true}, 2},
		{1, ReturnEvent, setOutput{}, 1},
		{0, ReturnEvent, setOutput{}, 0},
	}
	res = CheckEvents(setModel, events)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	events = []Event[setInput, setOutput]{
		{0, CallEvent, setInput{true, 100}, 0},
		{1, CallEvent, setInput{true, 110}, 1},
		{2, CallEvent, setInput{false, 0}, 2},
		{2, ReturnEvent, setOutput{[]int{100, 100, 110}, false}, 2},
		{1, ReturnEvent, setOutput{}, 1},
		{0, ReturnEvent, setOutput{}, 0},
	}
	res = CheckEvents(setModel, events)
	if res == true {
		t.Fatal("expected operations not to be linearizable")
	}
}
