// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import "math/bits"

// The qualify image's symbol trigger (fpga/images/qualify/symbol_trigger.v),
// modelled clock for clock on the 125 MHz receiver clock: runs of the run
// line classified through a host table (leader and data codes), bits from
// the runs, a unit sampler or a clock line, and a masked 64-bit window
// compared at a bit count after the frame opened.

// Symbol trigger bit sources (SymbolCfg.Mode).
const (
	SymbolClass   = 0 // the runs' data codes
	SymbolLevel   = 1 // the run line's level each unit
	SymbolEdge    = 2 // whether an edge came in each unit
	SymbolClocked = 3 // line B at the run line's clock edges
)

// Symbol table codes: the leader code (an entry's high nibble) and the data
// code (its low nibble).
const (
	SymLeadNone = 0
	SymLeadSep  = 1
	SymLeadStep = 2 // + step 0..3

	SymDataEnd     = 0
	SymDataSep     = 1
	SymDataBit0    = 2
	SymDataBit1    = 3
	SymDataRestart = 4
)

// SymbolTableSize is the table's entries: {level, e[3:0], m[5:0]}.
const SymbolTableSize = 2048

// SymbolMaxCount is the most prescaled ticks a run counts.
const SymbolMaxCount = 1<<21 - 1

// SymbolLatency is the clocks from the edge or sample that completes a match
// to the match output (the RTL's match register).
const SymbolLatency = 8

// SymbolCfg is the block's configuration as its registers hold it.
type SymbolCfg struct {
	InvA, InvB, AndAB, ClockFall bool
	Mode                         int
	LeadLen                      int // 0..4
	Prescale                     int // 0..7
	UnitQ8                       uint32
	FrameUnits, Period, Phase0   int // 0..63
	K                            int // 0..1023
	Pattern, Mask                uint64
	Table                        [SymbolTableSize]uint8
}

// SymbolIndex is the table index of a run of count prescaled ticks at level.
// TRLC-LINKS: REQ-SDS-013
func SymbolIndex(level bool, count uint32) int {
	if count > SymbolMaxCount {
		count = SymbolMaxCount
	}
	e, sh := 0, 0
	if count >= 64 {
		top := bits.Len32(count) - 1 // the leading one: bit 6 to 20
		e, sh = top-5, top-6
	}
	idx := e<<6 | int(count>>sh&63)
	if level {
		idx |= 1024
	}
	return idx
}

// SymbolCount is the prescaled ticks the block counts for a run of w clocks
// (edges w clocks apart).
// TRLC-LINKS: REQ-SDS-013
func SymbolCount(w int, prescale int) uint32 {
	if w < 1 {
		return 0
	}
	c := uint32((w - 1) >> prescale)
	if c > SymbolMaxCount {
		c = SymbolMaxCount
	}
	return c
}

// SymbolTrigger is the block's state.
type SymbolTrigger struct {
	c SymbolCfg

	r0, d0, rp, primed bool
	pc                 uint32
	cnt                uint32
	v1, l1             bool
	n1                 uint32
	v2, l2             bool
	n2, e2, s2         uint32
	v3                 bool
	addr3              int
	v4                 bool
	q4                 uint8
	v5                 bool
	dEnd, dBit, dVal   bool
	dRestart, lSep     bool
	lStep              [4]bool
	rr                 int64
	act, ef            bool
	fu                 int
	sbV, sb            bool
	dv, dbit           [4]bool
	inFrame            bool
	got                int
	n, ph              int
	sh                 uint64
	hitA, openA        bool
	partOK             bool
	hitB, openB        bool
	match              bool
}

// NewSymbolTrigger starts the block in reset with configuration c.
// TRLC-LINKS: REQ-SDS-013
func NewSymbolTrigger(c SymbolCfg) *SymbolTrigger {
	return &SymbolTrigger{c: c, r0: true, d0: true, rp: true}
}

// Step advances one receiver clock with the sliced lines a and b (before
// inversion) and reset; it returns the match output after the clock.
// TRLC-LINKS: REQ-SDS-013
func (s *SymbolTrigger) Step(a, b, reset bool) bool {
	c := &s.c
	pmask := uint32(1)<<c.Prescale - 1
	half := int64(c.UnitQ8 >> 1)
	unitM256 := int64(c.UnitQ8) - 256
	edgeNow := s.primed && s.r0 != s.rp

	la, lb := a != c.InvA, b != c.InvB
	nr0 := la
	if c.AndAB {
		nr0 = la && lb
	}
	nd0 := lb

	// run lengths
	nprimed, nrp, npc, ncnt := s.primed, s.rp, s.pc, s.cnt
	if reset {
		nprimed, npc, ncnt, nrp = false, 0, 0, s.r0
	} else {
		nprimed, nrp = true, s.r0
		if edgeNow {
			npc, ncnt = 0, 0
		} else {
			npc = (s.pc + 1) & 127
			if s.pc&pmask == pmask && s.cnt != SymbolMaxCount {
				ncnt = s.cnt + 1
			}
		}
	}
	// classification pipeline
	nv1, nn1, nl1 := !reset && edgeNow, s.cnt, s.rp
	ne2, ns2 := uint32(0), uint32(0)
	if s.n1>>6 != 0 {
		top := uint32(0)
		for b := 6; b < 21; b++ {
			if s.n1>>b&1 != 0 {
				top = uint32(b)
			}
		}
		ne2, ns2 = top-5, top-6
	}
	nv2, nn2, nl2 := !reset && s.v1, s.n1, s.l1
	nv3 := !reset && s.v2
	naddr3 := int(s.e2)<<6 | int((s.n2>>s.s2)&63)
	if s.l2 {
		naddr3 |= 1024
	}
	nv4 := !reset && s.v3
	nq4 := c.Table[s.addr3]
	nv5 := !reset && s.v4
	ndEnd, ndBit, ndVal, ndRestart, nlSep := s.q4&15 == 0, s.q4&15 == 2 || s.q4&15 == 3, s.q4&1 != 0, s.q4&15 == 4, s.q4>>4 == 1
	nlStep := [4]bool{s.q4>>4 == 2, s.q4>>4 == 3, s.q4>>4 == 4, s.q4>>4 == 5}

	// frame state (combinational on this clock's v4/q4 and delayed bit)
	bv, bbit := s.dv[3], s.dbit[3]
	fIn, fGot, fOpen, fBitV, fBit := s.inFrame, s.got, false, false, false
	if s.v5 {
		if s.inFrame {
			switch {
			case s.dEnd:
				if c.LeadLen == 0 {
					fOpen = true
				} else {
					fIn, fGot = false, 0
				}
			case s.dBit:
				if c.Mode == SymbolClass {
					fBitV, fBit = true, s.dVal
				}
			case s.dRestart:
				if c.LeadLen <= 1 {
					fOpen = true
				} else {
					fIn, fGot = false, 1
				}
			}
		} else {
			if s.got < c.LeadLen && s.lStep[s.got&3] {
				fGot = s.got + 1
				if s.got+1 == c.LeadLen {
					fOpen = true
				}
			} else if s.lStep[0] {
				fGot = 1
				if c.LeadLen == 1 {
					fOpen = true
				}
			} else if !s.lSep {
				fGot = 0
			}
		}
	}
	if fOpen {
		fIn, fGot = true, 0
	}
	if c.Mode != SymbolClass && bv {
		fBitV, fBit = fIn, bbit
	}
	restartUnits := s.v5 && fOpen

	// unit sampler / clocked bits
	nrr, nact, nef, nfu, nsbV, nsb := s.rr, s.act, s.ef, s.fu, false, s.sb
	if reset {
		nrr, nact, nef, nfu = 0, false, false, 0
	} else if c.Mode == SymbolClocked {
		nsbV = edgeNow && (s.r0 != c.ClockFall)
		nsb = s.d0
	} else {
		rrDue := s.rr>>8 == 0
		if edgeNow {
			nrr, nef = half, true
			if !s.act {
				nact, nfu = true, 0
			}
		} else if s.act {
			if rrDue {
				nsbV = c.Mode != SymbolClass
				if c.Mode == SymbolEdge {
					nsb = s.ef
				} else {
					nsb = s.r0
				}
				nef = false
				nrr = (s.rr + unitM256) & (1<<28 - 1)
				nfu = (s.fu + 1) & 63
				if c.FrameUnits != 0 && (s.fu+1)&63 == c.FrameUnits {
					nact = false
				}
			} else {
				nrr = (s.rr - 256) & (1<<28 - 1)
			}
		}
		if restartUnits {
			nfu, nact = 0, true
		}
	}
	ndv := [4]bool{s.sbV && !reset, s.dv[0], s.dv[1], s.dv[2]}
	ndbit := [4]bool{s.sb, s.dbit[0], s.dbit[1], s.dbit[2]}

	nBase, phBase := s.n, s.ph
	if fOpen {
		nBase, phBase = 0, c.Phase0
	}
	nNext := nBase
	if nBase != 1023 {
		nNext = nBase + 1
	}
	phNext := (phBase + 1) & 63
	if phNext == c.Period {
		phNext = 0
	}
	ninFrame, ngot, nn, nph, nsh := s.inFrame, s.got, s.n, s.ph, s.sh
	nhitA, nopenA := false, false
	if reset {
		ninFrame, ngot, nn, nph, nsh = c.LeadLen == 0, 0, 0, c.Phase0, 0
	} else {
		ninFrame, ngot = fIn, fGot
		if fOpen {
			nn, nph = 0, c.Phase0
		}
		nopenA = fOpen && c.K == 0
		if fBitV {
			nsh = s.sh << 1
			if fBit {
				nsh |= 1
			}
			nn, nhitA = nNext, true
			if c.Period == 0 {
				nph = 0
			} else {
				nph = phNext
			}
		}
	}
	npartOK := (s.sh^c.Pattern)&c.Mask == 0
	cond := s.n == c.K
	if c.Period != 0 {
		cond = s.n >= c.K && s.ph == 0
	}
	nhitB, nopenB := s.hitA && !reset && cond, s.openA && !reset
	nmatch := !reset && ((s.hitB && s.partOK) || s.openB)

	s.r0, s.d0 = nr0, nd0
	s.primed, s.rp, s.pc, s.cnt = nprimed, nrp, npc, ncnt
	s.v1, s.n1, s.l1 = nv1, nn1, nl1
	s.v2, s.n2, s.l2, s.e2, s.s2 = nv2, nn2, nl2, ne2, ns2
	s.v3, s.addr3 = nv3, naddr3
	s.v4, s.q4 = nv4, nq4
	s.v5, s.dEnd, s.dBit, s.dVal, s.dRestart, s.lSep, s.lStep = nv5, ndEnd, ndBit, ndVal, ndRestart, nlSep, nlStep
	s.rr, s.act, s.ef, s.fu, s.sbV, s.sb = nrr, nact, nef, nfu, nsbV, nsb
	s.dv, s.dbit = ndv, ndbit
	s.inFrame, s.got, s.n, s.ph, s.sh = ninFrame, ngot, nn, nph, nsh
	s.hitA, s.openA = nhitA, nopenA
	s.partOK, s.hitB, s.openB, s.match = npartOK, nhitB, nopenB, nmatch
	return nmatch
}

// SymbolMatches runs the block over per-clock line levels after reset clocks
// of reset and returns the clocks (indices into a, b) whose step output a
// match.
// TRLC-LINKS: REQ-SDS-013
func SymbolMatches(c SymbolCfg, a, b []bool, resetClocks int) []int {
	s := NewSymbolTrigger(c)
	var out []int
	for i := range a {
		bb := true
		if i < len(b) {
			bb = b[i]
		}
		if s.Step(a[i], bb, i < resetClocks) {
			out = append(out, i)
		}
	}
	return out
}
