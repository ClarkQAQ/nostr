package hyperloglog

import (
	"encoding/binary"
	"fmt"
)

// invPow2[v] == 2^(-v) for v in [0, 63]. Register values are tiny (<=57), so
// the table makes Count() division-free and allocation-free per register.
var invPow2 [64]float64

func init() {
	for i := range invPow2 {
		invPow2[i] = 1.0 / float64(uint64(1)<<uint(i))
	}
}

// Everything is hardcoded to use precision 8, i.e. 256 registers.
type HyperLogLog struct {
	offset    int
	registers []uint8
}

func New(offset int) *HyperLogLog {
	if offset < 0 || offset > 32-8 {
		panic(fmt.Errorf("invalid offset %d", offset))
	}

	// precision is always 8
	// the number of registers is always 256 (1<<8)
	hll := &HyperLogLog{offset: offset}
	hll.registers = make([]uint8, 256)
	return hll
}

func NewWithRegisters(registers []byte, offset int) *HyperLogLog {
	if offset < 0 || offset > 32-8 {
		panic(fmt.Errorf("invalid offset %d", offset))
	}
	if len(registers) != 256 {
		panic(fmt.Errorf("invalid number of registers %d", len(registers)))
	}
	return &HyperLogLog{registers: registers, offset: offset}
}

func (hll *HyperLogLog) GetRegisters() []byte    { return hll.registers }
func (hll *HyperLogLog) SetRegisters(enc []byte) { hll.registers = enc }
func (hll *HyperLogLog) MergeRegisters(other []byte) {
	for i, v := range other {
		if v > hll.registers[i] {
			hll.registers[i] = v
		}
	}
}

func (hll *HyperLogLog) Clear() {
	for i := range hll.registers {
		hll.registers[i] = 0
	}
}

// RegisterForPubkey returns the register index and value an event pubkey maps
// to under the given offset, without mutating any sketch. offset must be in
// [0, 24] so the 8-byte window stays inside the 32-byte pubkey.
func RegisterForPubkey(pubkey [32]byte, offset int) (idx uint8, val uint8) {
	x := pubkey[offset : offset+8]
	w := binary.BigEndian.Uint64(x)
	return x[0], clz56(w) + 1
}

// Add takes a Nostr event pubkey which will be used as the item "key" (that combined with the offset)
func (hll *HyperLogLog) Add(pubkey [32]byte) {
	idx, val := RegisterForPubkey(pubkey, hll.offset)
	if val > hll.registers[idx] {
		hll.registers[idx] = val
	}
}

func (hll *HyperLogLog) Merge(other *HyperLogLog) {
	for i, v := range other.registers {
		if v > hll.registers[i] {
			hll.registers[i] = v
		}
	}
}

// CountRegisters estimates the cardinality from a raw 256-register sketch,
// using the same estimation logic as HyperLogLog.Count.
func CountRegisters(registers []byte) uint64 {
	if len(registers) != 256 {
		return 0
	}
	v := countZeros(registers)

	if v != 0 {
		lc := linearCounting(256 /* nregisters */, v)

		if lc <= 220 /* threshold */ {
			return uint64(lc)
		}
	}

	est := estimateRegisters(registers)
	if est <= 256 /* nregisters */ *3 {
		if v != 0 {
			return uint64(linearCounting(256 /* nregisters */, v))
		}
	}

	return uint64(est)
}

func (hll *HyperLogLog) Count() uint64 {
	return CountRegisters(hll.registers)
}

func estimateRegisters(registers []byte) float64 {
	sum := 0.0
	for _, val := range registers {
		sum += invPow2[val]
	}

	return 0.7182725932495458 /* alpha for 256 registers */ * 256 /* nregisters */ * 256 /* nregisters */ / sum
}
