package porcupine

import (
	"errors"
	"fmt"
)

// OrderKind represents the kind of an ordering constraint
type OrderKind int

const (
	// Indicates that operation a should be strictly ordered before operation b.
	HardBefore OrderKind = -2
	// Indicates that operation a should likely be ordered before operation b.
	SoftBefore OrderKind = -1
	// Indicates that there is no information about the relative order of operations a and b.
	Unconstrained OrderKind = 0
	// Indicates that operation a should likely be ordered after operation b.
	SoftAfter OrderKind = 1
	// Indicates that operation a should be strictly ordered after operation b.
	HardAfter OrderKind = 2
)

// Oracle encodes ordering constraints by comparing pairs of operations.
type Oracle struct {
	Params interface{}
	// Preprocess adds additional info to clientOperations before comparison.
	Preprocess func(op *Operation, state interface{}) interface{}
	// Compare returns the ordering relationship between two operations.
	Compare func(a *Operation, b *Operation) (OrderKind, error)
}

// Validity defines valid serializations for a model.
type Validity struct {
	Init func(model Model) interface{}
	Step func(state interface{}, input interface{}, output interface{}, model Model) (bool, interface{})
}

// Consistency encodes a consistency property.
type Consistency struct {
	// e.g., linearizability; etcd revision number, etc
	Oracles []Oracle
	// e.g. RVal
	Valid Validity
}

func (c *Consistency) Check(a *ClientOperation, b *ClientOperation) (OrderKind, error) {
	ok := Unconstrained
	for _, o := range c.Oracles {
		order, err := o.Compare(&a.Op, &b.Op)
		if err != nil {
			return ok, err
		}
		switch order {
		case HardAfter:
			if ok == HardBefore {
				return ok, errors.New("conflicting order constraints:\n" + fmt.Sprintf("%+v", a) + "\n" + fmt.Sprintf("%+v", b))
			}
			ok = HardAfter
		case HardBefore:
			if ok == HardAfter {
				return ok, errors.New("conflicting order constraints:\n" + fmt.Sprintf("%+v", a) + "\n" + fmt.Sprintf("%+v", b))
			}
			ok = HardBefore
		case SoftAfter:
			if ok == Unconstrained {
				ok = SoftAfter
			}
		case SoftBefore:
			if ok == Unconstrained {
				ok = SoftBefore
			}
		case Unconstrained:
			// nothing to do
		}
	}
	return ok, nil
}

func (c *Consistency) Preprocess(history OperationHistory) {
	for _, oracle := range c.Oracles {
		if oracle.Preprocess == nil {
			continue
		}
		for i := range history {
			var state interface{}
			for j := range history[i] {
				state = oracle.Preprocess(&history[i][j].Op, state)
			}
		}
	}
}

var GeneralLikely = Oracle{
	Compare: func(a *Operation, b *Operation) (OrderKind, error) {
		if a.Call < b.Call {
			return SoftBefore, nil
		}
		if a.Call > b.Call {
			return SoftAfter, nil
		}
		return Unconstrained, nil
	},
}

var RealTime = Oracle{
	Compare: func(a *Operation, b *Operation) (OrderKind, error) {
		if a.Return < b.Call {
			return HardBefore, nil
		}
		if a.Call > b.Return {
			return HardAfter, nil
		}
		return Unconstrained, nil
	},
}

var LinearizabilityOracles = []Oracle{RealTime, GeneralLikely}

var RealTimeWrites = func() Oracle {
	m := make(map[*Operation]int64)
	return Oracle{
		Params: m,
		Preprocess: func(op *Operation, state interface{}) interface{} {
			type ppState struct {
				openReads []*Operation
			}
			s, _ := state.(*ppState)
			if s == nil {
				s = &ppState{}
			}
			if op.OpKind == Write {
				// with each open read, store this write's end time.
				for _, r := range s.openReads {
					m[r] = op.Return
				}
				s.openReads = nil
			} else {
				s.openReads = append(s.openReads, op)
			}
			return s
		},
		Compare: func(a *Operation, b *Operation) (OrderKind, error) {
			if a.OpKind == Write && b.OpKind == Write {
				return RealTime.Compare(a, b)
			}
			var read, write *Operation
			readIsA := false
			if a.OpKind != Write && b.OpKind == Write {
				read, write = a, b
				readIsA = true
			} else if a.OpKind == Write && b.OpKind != Write {
				read, write = b, a
				readIsA = false
			} else {
				return Unconstrained, nil
			}
			nextWrite, ok := m[read]
			if !ok {
				return Unconstrained, nil
			}
			if write.Call > nextWrite {
				if readIsA {
					return HardBefore, nil
				}
				return HardAfter, nil
			}
			return Unconstrained, nil
		},
	}
}()

var OrderedSequentialConsistencyOracles = []Oracle{RealTimeWrites, GeneralLikely}

var RVal = Validity{
	Init: func(model Model) interface{} {
		return model.Init()
	},
	Step: func(state interface{}, input interface{}, output interface{}, model Model) (bool, interface{}) {
		return model.Step(state, input, output)
	},
}

var Linearizability = Consistency{
	Oracles: LinearizabilityOracles,
	Valid:   RVal,
}

var OrderedSequentialConsistency = Consistency{
	Oracles: OrderedSequentialConsistencyOracles,
	Valid:   RVal,
}
