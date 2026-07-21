package porcupine

import (
	"errors"
	"fmt"
)

// OrderKind represents the kind of an ordering constraint
type OrderKind int

const (
	// Indicates there is no information about the relative order of operations a and b.
	DontKnow OrderKind = iota
	// Indicates that operation a should be strictly ordered before operation b.
	HardBefore OrderKind = -2
	// Indicates that operation a should likely be ordered before operation b.
	SoftBefore OrderKind = -1
	// Indicates that operation a and b are concurrent (real-time overlapping),
	// i.e. neither HardBefore nor HardAfter.
	Concurrent OrderKind = 3
	// Indicates that operation a should likely be ordered after operation b.
	SoftAfter OrderKind = 1
	// Indicates that operation a should be strictly ordered after operation b.
	HardAfter OrderKind = 2
)

// Oracle encodes an ordering constraint by comparing pairs of operations.
type Oracle func(a *Operation, b *Operation) (OrderKind, error)

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

func (c *Consistency) Check(a *clientOperation, b *clientOperation) (OrderKind, error) {
	ok := DontKnow
	for _, o := range c.Oracles {
		order, err := o(&a.Op, &b.Op)
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
			if ok == DontKnow || ok == Concurrent {
				ok = SoftAfter
			}
		case SoftBefore:
			if ok == DontKnow || ok == Concurrent {
				ok = SoftBefore
			}
		case Concurrent:
			if ok == DontKnow {
				ok = Concurrent
			}
		case DontKnow:
			// nothing to do
		}
	}
	return ok, nil
}

var GeneralLikely Oracle = func(a *Operation, b *Operation) (OrderKind, error) {
	if a.Call < b.Call {
		return SoftBefore, nil
	}
	if a.Call > b.Call {
		return SoftAfter, nil
	}
	return DontKnow, nil
}

var RealTime Oracle = func(a *Operation, b *Operation) (OrderKind, error) {
	if a.Return < b.Call {
		return HardBefore, nil
	}
	if a.Call > b.Return {
		return HardAfter, nil
	}
	return Concurrent, nil
}

var LinearizabilityOracles = []Oracle{RealTime, GeneralLikely}

var RealTimeWrites Oracle = func(a *Operation, b *Operation) (OrderKind, error) {
	if a.OpKind == Write && b.OpKind == Write {
		return RealTime(a, b)
	}
	// When one is a read and the other is a write, we don't have enough
	// information without preprocessing, so return DontKnow.
	if (a.OpKind != Write && b.OpKind == Write) || (a.OpKind == Write && b.OpKind != Write) {
		return DontKnow, nil
	}
	// read vs read: no constraint from RealTimeWrites
	return DontKnow, nil
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
