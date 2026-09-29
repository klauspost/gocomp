package corpus

import "encoding/binary"

// Rand fills dst with PCG output using the same generator and seed as the
// original compress.go, so the incompressible corpus is byte-identical on every
// machine without a download.
func Rand(dst []byte) {
	const mult = 6364136223846793005
	const seed = 0xdeadbeef

	inc := uint64(seed<<1) | 1
	state := uint64(0)
	state = state*mult + (inc | 1)
	state += seed
	state = state*mult + (inc | 1)

	next := func() uint32 {
		old := state
		state = state*mult + (inc | 1)
		xorshifted := uint32(((old >> 18) ^ old) >> 27)
		rot := uint32(old >> 59)
		return (xorshifted >> rot) | (xorshifted << ((-rot) & 31))
	}

	w := dst
	for ; len(w) >= 4; w = w[4:] {
		binary.LittleEndian.PutUint32(w, next())
	}
	if len(w) > 0 {
		var tail [4]byte
		binary.LittleEndian.PutUint32(tail[:], next())
		copy(w, tail[:])
	}
}
