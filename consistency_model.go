package porcupine

import "errors"

// OrderKind represents the kind of precedence relationship between two operations
type OrderKind int

const (
	// Indicates that event a should be strictly ordered before event b.
	HardBefore OrderKind = -2
	// Indicates that event a should likely be ordered before event b.
	SoftBefore OrderKind = -1
	// Indicates that there is no information about the relative order of operations a and b.
	Unconstrained OrderKind = 0
	// Indicates that event a should likely be ordered after event b.
	SoftAfter OrderKind = 1
	// Indicates that event a should be strictly ordered after event b.
	HardAfter OrderKind = 2
)

type Oracle func(a *Operation, b *Operation) (OrderKind, error)

type Consistency struct {
	// e.g., linearizability; etcd revision number, etc
	Oracles []Oracle
	// e.g. RVal
	Valid func(interface{}, interface{}, interface{}, func(interface{}, interface{}, interface{}) (bool, interface{})) (bool, interface{})
}

func (c *Consistency) Check(a *Operation, b *Operation) (OrderKind, error) {
	ok := Unconstrained
	for _, o := range c.Oracles {
		order, err := o(a, b)
		if err != nil {
			return ok, err
		}
		switch order {
		case HardAfter:
			if ok == HardBefore {
				return ok, errors.New("conflicting order constraints")
			}
			ok = HardAfter
		case HardBefore:
			if ok == HardAfter {
				return ok, errors.New("conflicting order constraints")
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

func GeneralLikely(a *Operation, b *Operation) (OrderKind, error) {
	if a.Call < b.Call {
		return SoftBefore, nil
	}
	if a.Call > b.Call {
		return SoftAfter, nil
	}
	return Unconstrained, nil
}

func RealTime(a *Operation, b *Operation) (OrderKind, error) {
	if a.Return < b.Call {
		return HardBefore, nil
	}
	if a.Call > b.Return {
		return HardAfter, nil
	}
	return Unconstrained, nil
}

var LinearizabilityOracles = []Oracle{RealTime, GeneralLikely}

func RealTimeWrites(a *Operation, b *Operation) (OrderKind, error) {
	if a.OpKind == Write && b.OpKind == Write {
		return RealTime(a, b)
	}
	return Unconstrained, nil
}

var OrderedSequentialConsistencyOracles = []Oracle{RealTimeWrites, GeneralLikely}

func RVal(state interface{}, input interface{}, output interface{}, step func(interface{}, interface{}, interface{}) (bool, interface{})) (bool, interface{}) {
	ok, newState := step(state, input, output)
	if !ok {
		return false, nil
	}
	if input == output {
		return true, newState
	}
	return false, nil
}

var Linearizability = Consistency{
	Oracles: LinearizabilityOracles,
	Valid:   RVal,
}

var OrderedSequentialConsistency = Consistency{
	Oracles: OrderedSequentialConsistencyOracles,
	Valid:   RVal,
}
