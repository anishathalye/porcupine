package porcupine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestRegisterModel(t *testing.T) {
	t.Parallel()
	// inputs
	type registerInput struct {
		op    bool // false = read, true = write
		value int
	}
	// output
	type registerOutput int // we don't care about return value for write
	registerModel := Model{
		Partition:      NoPartition,
		PartitionEvent: NoPartitionEvent,
		Init:           func() interface{} { return 0 },
		Step: func(state interface{}, input interface{}, output interface{}) (bool, interface{}) {
			st := state.(int)
			inp := input.(registerInput)
			out := output.(int)
			if inp.op == false {
				// read
				return out == st, state
			} else {
				// write
				return true, inp.value
			}
		},
	}

	// examples taken from http://nil.csail.mit.edu/6.824/2017/quizzes/q2-17-ans.pdf
	// section VII

	ops := []Operation{
		Operation{registerInput{true, 100}, 0, 0, 100},
		Operation{registerInput{false, 0}, 25, 100, 75},
		Operation{registerInput{false, 0}, 30, 0, 60},
	}
	res := CheckOperations(registerModel, ops)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	// same example as above, but with Event
	events := []Event{
		Event{CallEvent, registerInput{true, 100}, 0},
		Event{CallEvent, registerInput{false, 0}, 1},
		Event{CallEvent, registerInput{false, 0}, 2},
		Event{ReturnEvent, 0, 2},
		Event{ReturnEvent, 100, 1},
		Event{ReturnEvent, 0, 0},
	}
	res = CheckEvents(registerModel, events)
	if res != true {
		t.Fatal("expected operations to be linearizable")
	}

	ops = []Operation{
		Operation{registerInput{true, 200}, 0, 0, 100},
		Operation{registerInput{false, 0}, 10, 200, 30},
		Operation{registerInput{false, 0}, 40, 0, 90},
	}
	res = CheckOperations(registerModel, ops)
	if res != false {
		t.Fatal("expected operations to not be linearizable")
	}

	// same example as above, but with Event
	events = []Event{
		Event{CallEvent, registerInput{true, 200}, 0},
		Event{CallEvent, registerInput{false, 0}, 1},
		Event{ReturnEvent, 200, 1},
		Event{CallEvent, registerInput{false, 0}, 2},
		Event{ReturnEvent, 0, 2},
		Event{ReturnEvent, 0, 0},
	}
	res = CheckEvents(registerModel, events)
	if res != false {
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

func getEtcdModel() Model {
	return Model{
		Partition:      NoPartition,
		PartitionEvent: NoPartitionEvent,
		Init:           func() interface{} { return -1000000 }, // -1000000 corresponds with nil
		Step: func(state interface{}, input interface{}, output interface{}) (bool, interface{}) {
			st := state.(int)
			inp := input.(etcdInput)
			out := output.(etcdOutput)
			if inp.op == 0 {
				// read
				ok := (out.exists == false && st == -1000000) || (out.exists == true && st == out.value) || out.unknown
				return ok, state
			} else if inp.op == 1 {
				// write
				return true, inp.arg1
			} else {
				// cas
				ok := (inp.arg1 == st && out.ok) || (inp.arg1 != st && !out.ok) || out.unknown
				result := st
				if inp.arg1 == st {
					result = inp.arg2
				}
				return ok, result
			}
		},
	}
}

func parseJepsenLog(t *testing.T, filename string) []Event {
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal("can't open file")
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

	var events []Event = nil

	id := 0
	procIdMap := make(map[int]int)
	for {
		lineBytes, isPrefix, err := reader.ReadLine()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatal("error while reading file: " + err.Error())
		}
		if isPrefix {
			t.Fatal("can't handle isPrefix")
		}
		line := string(lineBytes)

		switch {
		case invokeRead.MatchString(line):
			args := invokeRead.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			events = append(events, Event{CallEvent, etcdInput{op: 0}, id})
			procIdMap[proc] = id
			id++
		case invokeWrite.MatchString(line):
			args := invokeWrite.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			value, _ := strconv.Atoi(args[2])
			events = append(events, Event{CallEvent, etcdInput{op: 1, arg1: value}, id})
			procIdMap[proc] = id
			id++
		case invokeCas.MatchString(line):
			args := invokeCas.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			from, _ := strconv.Atoi(args[2])
			to, _ := strconv.Atoi(args[3])
			events = append(events, Event{CallEvent, etcdInput{op: 2, arg1: from, arg2: to}, id})
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
			events = append(events, Event{ReturnEvent, etcdOutput{exists: exists, value: value}, matchId})
		case returnWrite.MatchString(line):
			args := returnWrite.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			events = append(events, Event{ReturnEvent, etcdOutput{}, matchId})
		case returnCas.MatchString(line):
			args := returnCas.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			events = append(events, Event{ReturnEvent, etcdOutput{ok: args[2] == "ok"}, matchId})
		case timeoutRead.MatchString(line):
			// timing out a read and then continuing operations is fine
			// we could just delete the read from the events, but we do this the lazy way
			args := timeoutRead.FindStringSubmatch(line)
			proc, _ := strconv.Atoi(args[1])
			matchId := procIdMap[proc]
			delete(procIdMap, proc)
			// okay to put the return here in the history
			events = append(events, Event{ReturnEvent, etcdOutput{unknown: true}, matchId})
		}
	}

	for _, matchId := range procIdMap {
		events = append(events, Event{ReturnEvent, etcdOutput{unknown: true}, matchId})
	}

	return events
}

func checkJepsen(t *testing.T, logNum int, correct bool) {
	t.Parallel()
	etcdModel := getEtcdModel()
	events := parseJepsenLog(t, fmt.Sprintf("test_data/jepsen/etcd_%03d.log", logNum))
	t.Logf("etcd log with %d entries, expecting result %t", len(events), correct)
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
