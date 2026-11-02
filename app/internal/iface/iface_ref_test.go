package iface

import "fmt"

// TRLC-LINKS: REQ-SDS-129
func (a Access) String() string {
	switch a {
	case AccR:
		return "R"
	case AccW:
		return "W"
	case AccRW:
		return "RW"
	}
	return fmt.Sprintf("Access(%d)", uint8(a))
}

// Get extracts the field from word.
// TRLC-LINKS: REQ-SDS-129
func (f Field) Get(word uint16) uint16 { return (word & f.Mask) >> f.Shift }

// Put returns word with the field replaced by v (v is masked to the field width).
// TRLC-LINKS: REQ-SDS-129
func (f Field) Put(word, v uint16) uint16 { return (word &^ f.Mask) | ((v << f.Shift) & f.Mask) }

// ByName returns the register called name.
// TRLC-LINKS: REQ-SDS-129
func ByName(name string) (Register, bool) {
	for i := range registers {
		if registers[i].Name == name {
			return registers[i], true
		}
	}
	return Register{}, false
}

// Decimated returns the tier with its tick scaled by the pick decimator
// (dual-E1 records at DECIM = d have samples d x 5 ns apart).
// TRLC-LINKS: REQ-SDS-129
func (t Tier) Decimated(d uint32) Tier {
	if d == 0 {
		d = 1
	}
	t.TickNs *= float64(d)
	return t
}

// Word packs two samples into a BURST word.
// TRLC-LINKS: REQ-SDS-129
func Word(ch1, ch2 uint8) uint16 { return uint16(ch1)<<8 | uint16(ch2) }

// Split unpacks a BURST word.
// TRLC-LINKS: REQ-SDS-129
func Split(w uint16) (ch1, ch2 uint8) { return uint8(w >> 8), uint8(w) }

// F is the number of phase bins.
// TRLC-LINKS: REQ-SDS-129
func (p StackParams) F() uint { return 1 << p.PhaseBins }

// Words is the readout length: CH1 bins (recA) then CH2 bins (recB), Rows*F rows
// of ROW_COLS words each.
// TRLC-LINKS: REQ-SDS-129
func (p StackParams) Words() int { return 2 * int(p.Rows) * int(p.F()) * RowCols }

// Validate checks the geometry against the fabric's limits.
// TRLC-LINKS: REQ-SDS-129
func (p StackParams) Validate() error {
	if p.PhaseBins > 3 {
		return fmt.Errorf("wordfmt: PHASE_BINS %d > 3", p.PhaseBins)
	}
	if p.Shift > 3 {
		return fmt.Errorf("wordfmt: SHIFT %d > 3", p.Shift)
	}
	if p.Rows == 0 || p.Rows*p.F() > Rows/2 {
		return fmt.Errorf("wordfmt: %d rows x F %d exceeds the %d accumulator rows per channel", p.Rows, p.F(), Rows/2)
	}
	return nil
}
