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
	// Preprocess adds additional info to clientOperations before comparison.
	Preprocess func(op *ClientOperation, state interface{}) interface{}
	// Compare returns the ordering relationship between two operations.
	Compare func(a *ClientOperation, b *ClientOperation) (OrderKind, error)
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
		order, err := o.Compare(a, b)
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
				state = oracle.Preprocess(&history[i][j], state)
			}
		}
	}
}

var GeneralLikely = Oracle{
	Compare: func(a *ClientOperation, b *ClientOperation) (OrderKind, error) {
		if a.Op.Call < b.Op.Call {
			return SoftBefore, nil
		}
		if a.Op.Call > b.Op.Call {
			return SoftAfter, nil
		}
		return Unconstrained, nil
	},
}

var RealTime = Oracle{
	Compare: func(a *ClientOperation, b *ClientOperation) (OrderKind, error) {
		if a.Op.Return < b.Op.Call {
			return HardBefore, nil
		}
		if a.Op.Call > b.Op.Return {
			return HardAfter, nil
		}
		return Unconstrained, nil
	},
}

var LinearizabilityOracles = []Oracle{RealTime, GeneralLikely}

var RealTimeWrites = Oracle{
	Preprocess: func(op *ClientOperation, state interface{}) interface{} {
		type ppState struct {
			openReads []*ClientOperation
		}
		s, _ := state.(*ppState)
		if s == nil {
			s = &ppState{}
		}
		if op.Op.OpKind == Write {
			// with each open read, store this write's end time.
			for _, r := range s.openReads {
				if r.Params == nil {
					r.Params = make(map[string]interface{})
				}
				r.Params["nextWrite"] = op.Op.Return
			}
			s.openReads = nil
		} else {
			s.openReads = append(s.openReads, op)
		}
		return s
	},
	Compare: func(a *ClientOperation, b *ClientOperation) (OrderKind, error) {
		if a.Op.OpKind == Write && b.Op.OpKind == Write {
			return RealTime.Compare(a, b)
		}
		var read, write *ClientOperation
		readIsA := false
		if a.Op.OpKind != Write && b.Op.OpKind == Write {
			read, write = a, b
			readIsA = true
		} else if a.Op.OpKind == Write && b.Op.OpKind != Write {
			read, write = b, a
			readIsA = false
		} else {
			return Unconstrained, nil
		}
		valNW, ok := read.Params["nextWrite"]
		if !ok {
			return Unconstrained, nil
		}
		nextWrite := valNW.(int64)
		if write.Op.Call > nextWrite {
			if readIsA {
				return HardBefore, nil
			}
			return HardAfter, nil
		}
		return Unconstrained, nil
	},
}

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
