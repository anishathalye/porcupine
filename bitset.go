package porcupine

type bitset []uint64

// data layout:
// bits 0-63 are in data[0], the next are in data[1], etc.

func newBitset(bits uint) bitset {
	extra := uint(0)
	if bits%64 != 0 {
		extra = 1
	}
	chunks := bits/64 + extra
	return bitset(make([]uint64, chunks))
}

func (b bitset) clone() bitset {
	dataCopy := make([]uint64, len(b))
	copy(dataCopy, b)
	return bitset(dataCopy)
}

func bitsetIndex(pos uint) (uint, uint) {
	return pos / 64, pos % 64
}

func (b bitset) set(pos uint) bitset {
	major, minor := bitsetIndex(pos)
	b[major] |= (1 << minor)
	return b
}

func (b bitset) clear(pos uint) bitset {
	major, minor := bitsetIndex(pos)
	b[major] &^= (1 << minor)
	return b
}

func (b bitset) get(pos uint) bool {
	major, minor := bitsetIndex(pos)
	return b[major]&(1<<minor) != 0
}
