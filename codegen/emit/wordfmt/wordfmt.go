// Package wordfmt holds the Go models of every drained word format of the
// default image (06-TIERS §1.1, §1.4, §1.5, §2): what the fabric must emit,
// word for word, for a given input — the reference rung R2b compares drained
// words against, and the contract the fabric's RTL is written to.
//
// THIS FILE IS SPLICED VERBATIM INTO THE GENERATED app/internal/iface/iface.go
// by codegen/emit (after the package clause and the import line), so it may
// import "fmt" only and must not declare a name the bindings declare
// (Field, Register, DiagEntry, Opcode, Access, Sel*, Op*, Diag*, geometry and
// enum constants). Its unit tests live next to it and run in codegen; the
// constants it uses (RowCols, IlCtrlTsrc*, ...) come from geom.go here and
// from the schema in iface.go, kept equal by a codegen test.
//
// Conventions shared by every model:
//   - a sample is an 8-bit code; a word is {CH1, CH2} = CH1<<8 | CH2 (BURST);
//   - words are in time order: word w of a record is time index w in every
//     mode (row-major, column-minor over the ROW_COLS columns of a row);
//   - the tier (Tier) fixes the time base and which pair produced a column —
//     it never changes the word content, which is why every model takes it and
//     hands it back in the Sequence, so a consumer can ask when a word was
//     sampled and by which converter pair;
//   - the models are exact integer arithmetic, the fabric's arithmetic.
package wordfmt

import "fmt"

// Tier is an interleave tier: the sample spacing and the pair that fills each
// column of a row (06-TIERS §1.1 table; column order E1,E3,E5,E2,E4 at IL5-100,
// §1.2.2).
type Tier struct {
	Name   string
	TickNs float64        // sample spacing of one channel
	Cols   [RowCols]uint8 // Cols[c] = pair (0 = E1 .. 4 = E5) that samples column c
}

var (
	// TierDualE1 is the v2.1 datapath: pair E1 fills every column, 5 ns x DECIM
	// (use Decimated(D) for the programmed DECIM).
	TierDualE1 = Tier{Name: "dual-E1", TickNs: 5, Cols: [RowCols]uint8{0, 0, 0, 0, 0}}
	// TierIL5x100 is the shipping tier: 100 MHz encode, 2 ns lattice, 500 MSa/s.
	TierIL5x100 = Tier{Name: "IL5-100", TickNs: 2, Cols: [RowCols]uint8{0, 2, 4, 1, 3}}
	// TierIL5x200 is the over-clock tier: 200 MHz encode, 1 ns lattice, 1 GSa/s.
	TierIL5x200 = Tier{Name: "IL5-200", TickNs: 1, Cols: [RowCols]uint8{0, 1, 2, 3, 4}}
)

// Decimated returns the tier with its tick scaled by the pick decimator
// (dual-E1 records at DECIM = d have samples d x 5 ns apart).
func (t Tier) Decimated(d uint32) Tier {
	if d == 0 {
		d = 1
	}
	t.TickNs *= float64(d)
	return t
}

// PairOfWord returns the pair (0 = E1) that sampled record word w.
func (t Tier) PairOfWord(w int) uint8 { return t.Cols[w%RowCols] }

// ColumnsOfPair returns the columns a pair fills (one at IL5, all five for E1
// in dual-E1, none for E2..E5 in dual-E1) — the freeze-signature check of rung
// R6 looks at exactly these.
func (t Tier) ColumnsOfPair(pair uint8) []int {
	var cols []int
	for c, p := range t.Cols {
		if p == pair {
			cols = append(cols, c)
		}
	}
	return cols
}

// Word packs two samples into a BURST word.
func Word(ch1, ch2 uint8) uint16 { return uint16(ch1)<<8 | uint16(ch2) }

// Split unpacks a BURST word.
func Split(w uint16) (ch1, ch2 uint8) { return uint8(w >> 8), uint8(w) }

// SplitWords unpacks a word sequence into its two sample streams.
func SplitWords(ws []uint16) (ch1, ch2 []uint8) {
	ch1, ch2 = make([]uint8, len(ws)), make([]uint8, len(ws))
	for i, w := range ws {
		ch1[i], ch2[i] = Split(w)
	}
	return ch1, ch2
}

// Sequence is a modelled drain: the words the fabric emits in pop order and
// their time base. Group consecutive words share one time step (1 for
// RECORD / DECIM / BOXCAR8, 2 for PEAK / BOXCAR16 whose two words describe the
// same D samples); StepNs is the spacing of steps. Stack sequences are
// addressed (row, phase, column) and use StackParams.TimeNs instead.
type Sequence struct {
	Tier   Tier
	Words  []uint16
	Group  int
	StepNs float64
	Stack  *StackParams // set for ModelStack output
}

// TimeNs returns the sample time of word i relative to word 0.
func (s Sequence) TimeNs(i int) float64 {
	if s.Stack != nil {
		return s.Stack.TimeNs(s.Tier, i)
	}
	g := s.Group
	if g < 1 {
		g = 1
	}
	return float64(i/g) * s.StepNs
}

// ---- RECORD -------------------------------------------------------------------

// ModelRecord is the RAW record: dual mode packs {CH1[i], CH2[i]} per word;
// single-channel mode packs two consecutive samples of the one channel per
// word, the earlier one in the high byte (BURST.CH1). Trailing samples that do
// not fill a word are dropped.
func ModelRecord(t Tier, chmode uint16, ch1, ch2 []uint8) (Sequence, error) {
	var ws []uint16
	step := t.TickNs
	switch chmode {
	case RunChmodeDual:
		n := min(len(ch1), len(ch2))
		ws = make([]uint16, n)
		for i := 0; i < n; i++ {
			ws[i] = Word(ch1[i], ch2[i])
		}
	case RunChmodeCh1, RunChmodeCh2:
		s := ch1
		if chmode == RunChmodeCh2 {
			s = ch2
		}
		ws = make([]uint16, len(s)/2)
		for i := range ws {
			ws[i] = Word(s[2*i], s[2*i+1])
		}
		step = 2 * t.TickNs
	default:
		return Sequence{}, fmt.Errorf("wordfmt: CHMODE %d is not a channel mode", chmode)
	}
	return Sequence{Tier: t, Words: ws, Group: 1, StepNs: step}, nil
}

// ---- TSRC test sources (IL_CTRL.TSRC) ----------------------------------------------

// TsrcGlitchPeriod is the spacing of the GLITCH source's non-zero word.
const TsrcGlitchPeriod = 512

// TsrcWord is the word the test source substitutes for the writer's k-th word
// since GO (k wraps with the ring in stream mode; the pattern index is the
// writer's word count, not the record address):
//
//	RAMP    k & 0xffff                         (+1 per word)
//	COLTAG  {k mod ROW_COLS, (k / ROW_COLS) & 0xff}  (column in the high byte, row in the low)
//	GLITCH  0xffff when k mod 512 == 0, else 0x0000
func TsrcWord(mode uint16, k uint32) (uint16, error) {
	switch mode {
	case IlCtrlTsrcRamp:
		return uint16(k), nil
	case IlCtrlTsrcColtag:
		return uint16(k%RowCols)<<8 | uint16((k/RowCols)&0xff), nil
	case IlCtrlTsrcGlitch:
		if k%TsrcGlitchPeriod == 0 {
			return 0xffff, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("wordfmt: TSRC %d is not a pattern source", mode)
}

// ModelTsrc is the RECORD drain of a test source: n words starting at pattern
// index k0 (the writer's word index of the first drained word). Word content is
// the same in every CHMODE — the substitution happens on the 16-bit record
// word — which is what rung R2b relies on.
func ModelTsrc(t Tier, mode uint16, k0 uint32, n int) (Sequence, error) {
	ws := make([]uint16, n)
	for i := range ws {
		w, err := TsrcWord(mode, k0+uint32(i))
		if err != nil {
			return Sequence{}, err
		}
		ws[i] = w
	}
	return Sequence{Tier: t, Words: ws, Group: 1, StepNs: t.TickNs}, nil
}

// TsrcCheck recovers the pattern index of the first word of a drained test
// source and lists the indices that do not follow the pattern from there. k0
// is exact for RAMP (16-bit wrap), modulo ROW_COLS*256 for COLTAG and modulo
// TsrcGlitchPeriod for GLITCH. A GLITCH drain shorter than one period without
// a glitch word is consistent (k0 = 1 is returned); a longer one without any
// is an error.
func TsrcCheck(mode uint16, words []uint16) (k0 uint32, bad []int, err error) {
	if len(words) == 0 {
		return 0, nil, fmt.Errorf("wordfmt: no words")
	}
	switch mode {
	case IlCtrlTsrcRamp:
		k0 = uint32(words[0])
	case IlCtrlTsrcColtag:
		col, row := Split(words[0])
		if col >= RowCols {
			return 0, nil, fmt.Errorf("wordfmt: first word 0x%04x is not a column tag", words[0])
		}
		k0 = uint32(row)*RowCols + uint32(col)
	case IlCtrlTsrcGlitch:
		k0 = 1
		found := false
		for i, w := range words {
			if w == 0xffff {
				k0, found = uint32((TsrcGlitchPeriod-i%TsrcGlitchPeriod)%TsrcGlitchPeriod), true
				break
			}
		}
		if !found && len(words) >= TsrcGlitchPeriod {
			return 0, nil, fmt.Errorf("wordfmt: no glitch word in %d words", len(words))
		}
	default:
		return 0, nil, fmt.Errorf("wordfmt: TSRC %d is not a pattern source", mode)
	}
	for i, w := range words {
		want, _ := TsrcWord(mode, k0+uint32(i))
		if w != want {
			bad = append(bad, i)
		}
	}
	return k0, bad, nil
}

// ---- REDUCE (IL_CTRL.REDUCE_MODE, D = DECIM) ---------------------------------------

func checkBlocks(d uint32, ch1, ch2 []uint8) (n int, err error) {
	if d == 0 {
		return 0, fmt.Errorf("wordfmt: D = 0")
	}
	n = min(len(ch1), len(ch2)) / int(d)
	return n, nil
}

// ModelDecim is REDUCE_MODE 1: the pick decimator, {CH1[kD], CH2[kD]}.
func ModelDecim(t Tier, d uint32, ch1, ch2 []uint8) (Sequence, error) {
	n, err := checkBlocks(d, ch1, ch2)
	if err != nil {
		return Sequence{}, err
	}
	ws := make([]uint16, n)
	for k := range ws {
		ws[k] = Word(ch1[uint32(k)*d], ch2[uint32(k)*d])
	}
	return Sequence{Tier: t, Words: ws, Group: 1, StepNs: float64(d) * t.TickNs}, nil
}

// ModelPeak is REDUCE_MODE 2: per D samples the word {min1, min2} then the
// word {max1, max2}. A partial trailing block emits nothing.
func ModelPeak(t Tier, d uint32, ch1, ch2 []uint8) (Sequence, error) {
	n, err := checkBlocks(d, ch1, ch2)
	if err != nil {
		return Sequence{}, err
	}
	ws := make([]uint16, 0, 2*n)
	for k := 0; k < n; k++ {
		lo, hi := uint32(k)*d, uint32(k+1)*d
		min1, min2, max1, max2 := uint8(255), uint8(255), uint8(0), uint8(0)
		for i := lo; i < hi; i++ {
			min1, max1 = min(min1, ch1[i]), max(max1, ch1[i])
			min2, max2 = min(min2, ch2[i]), max(max2, ch2[i])
		}
		ws = append(ws, Word(min1, min2), Word(max1, max2))
	}
	return Sequence{Tier: t, Words: ws, Group: 2, StepNs: float64(d) * t.TickNs}, nil
}

// BoxcarMaxD is the largest block length the 24-bit sum of the reduce unit holds
// (65536 x 255 < 2^24).
const BoxcarMaxD = 65536

// BoxcarParams returns the DEC_RECIP / DEC_PRE words for a block length D
// (2..BoxcarMaxD): PRE = max(0, ceil(log2 D) - 8) so the pre-shifted sum fits
// the 18-bit multiplier input, and RECIP = round(2^(16+PRE) / D), which lies
// in [256, 32768] and therefore fits 16 bits. 06-TIERS §1.5 writes the
// reciprocal as round(2^16 / (D >> PRE)) — the same number whenever D >> PRE
// is exact (every D <= 256, every power of two), but for other D above 256
// that truncation of D costs up to 0.8 % (D = 257 is scaled as 256); this
// constant keeps the same fabric arithmetic and removes that term. What
// remains is the 16-bit reciprocal's own rounding (<= 0.5 / RECIP, i.e.
// <= 2^-9 relative where D >> PRE approaches 256) plus the truncation of the
// pre-shifted sum — not the 2^-15 the design quotes.
func BoxcarParams(d uint32) (recip, pre uint16, err error) {
	if d < 2 || d > BoxcarMaxD {
		return 0, 0, fmt.Errorf("wordfmt: boxcar D = %d outside 2..%d", d, BoxcarMaxD)
	}
	var lg uint16
	for (uint32(1) << lg) < d {
		lg++
	}
	if lg > 8 {
		pre = lg - 8
	}
	num := uint64(1) << (16 + pre)
	recip = uint16((num + uint64(d)/2) / uint64(d))
	return recip, pre, nil
}

// MeanQ88 is the reduce unit's scaling: ((sum >> pre) * recip) >> 8, the mean
// in Q8.8 (the BOXCAR16 word).
func MeanQ88(sum uint32, recip, pre uint16) uint16 {
	return uint16(((uint64(sum) >> pre) * uint64(recip)) >> 8)
}

// Round8 rounds a Q8.8 mean to the 8-bit BOXCAR8 sample, saturating at 255.
func Round8(q88 uint16) uint8 {
	r := (uint32(q88) + 0x80) >> 8
	if r > 255 {
		r = 255
	}
	return uint8(r)
}

func boxcarSums(d uint32, ch1, ch2 []uint8, fn func(sum1, sum2 uint32)) error {
	n, err := checkBlocks(d, ch1, ch2)
	if err != nil {
		return err
	}
	for k := 0; k < n; k++ {
		var s1, s2 uint32
		for i := uint32(k) * d; i < uint32(k+1)*d; i++ {
			s1 += uint32(ch1[i])
			s2 += uint32(ch2[i])
		}
		fn(s1, s2)
	}
	return nil
}

// ModelBoxcar8 is REDUCE_MODE 3: per D samples one word {Round8(mean1), Round8(mean2)}.
func ModelBoxcar8(t Tier, d uint32, ch1, ch2 []uint8) (Sequence, error) {
	recip, pre, err := BoxcarParams(d)
	if err != nil {
		return Sequence{}, err
	}
	var ws []uint16
	err = boxcarSums(d, ch1, ch2, func(s1, s2 uint32) {
		ws = append(ws, Word(Round8(MeanQ88(s1, recip, pre)), Round8(MeanQ88(s2, recip, pre))))
	})
	return Sequence{Tier: t, Words: ws, Group: 1, StepNs: float64(d) * t.TickNs}, err
}

// ModelBoxcar16 is REDUCE_MODE 4: per D samples the word mean1 (Q8.8) then the
// word mean2 (Q8.8).
func ModelBoxcar16(t Tier, d uint32, ch1, ch2 []uint8) (Sequence, error) {
	recip, pre, err := BoxcarParams(d)
	if err != nil {
		return Sequence{}, err
	}
	var ws []uint16
	err = boxcarSums(d, ch1, ch2, func(s1, s2 uint32) {
		ws = append(ws, MeanQ88(s1, recip, pre), MeanQ88(s2, recip, pre))
	})
	return Sequence{Tier: t, Words: ws, Group: 2, StepNs: float64(d) * t.TickNs}, err
}

// ---- STACK (RUN.STACK, 06-TIERS §1.4) --------------------------------------------------

// StackParams is the batch geometry: STACK_CTRL.PHASE_BINS (log2 F), STACK_CTRL.SHIFT
// and STACK_LEN (rows per window).
type StackParams struct {
	PhaseBins uint8 // log2 F, F in {1, 2, 4, 8}
	Shift     uint8 // readout shift, 0..3
	Rows      uint  // window rows (STACK_LEN)
}

// F is the number of phase bins.
func (p StackParams) F() uint { return 1 << p.PhaseBins }

// Words is the readout length: CH1 bins (recA) then CH2 bins (recB), Rows*F rows
// of ROW_COLS words each.
func (p StackParams) Words() int { return 2 * int(p.Rows) * int(p.F()) * RowCols }

// Validate checks the geometry against the fabric's limits.
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

// TimeNs is the sample time of readout word i (within either channel block):
// address a = i / ROW_COLS = row*F + phase, column c = i mod ROW_COLS, time =
// (row*ROW_COLS + c) * tick + phase * tick / F.
func (p StackParams) TimeNs(t Tier, i int) float64 {
	half := p.Words() / 2
	if half > 0 {
		i %= half
	}
	a, c := i/RowCols, i%RowCols
	f := int(p.F())
	row, phase := a/f, a%f
	return float64(row*RowCols+c)*t.TickNs + float64(phase)*t.TickNs/float64(f)
}

// PhaseBin is the phase bin of a record: the top PHASE_BINS bits of its Q16
// trigger fraction (TRIGPOS_LO.FRAC).
func PhaseBin(frac uint16, phaseBins uint8) uint8 {
	if phaseBins == 0 {
		return 0
	}
	return uint8(frac >> (16 - phaseBins))
}

// StackRecord is one accepted record of a batch: its phase bin and the window
// the delay line delivered, Rows*ROW_COLS samples per channel in time order.
type StackRecord struct {
	Phase    uint8
	CH1, CH2 []uint8
}

// ModelStack is the batch readout: every record is added into the ACC_BITS-wide
// bins at address row*F + Phase (wrapping like the hardware, which cannot
// happen for N <= STACK_N_MAX), then drained as recA (CH1 bins) followed by
// recB (CH2 bins), row-major, column-minor, each word (sum >> SHIFT)[15:0].
// phaseCnt is the PHASE_CNT window. HALT ends a batch early: pass the records
// accepted so far.
func ModelStack(t Tier, p StackParams, recs []StackRecord) (seq Sequence, phaseCnt [8]uint16, err error) {
	if err = p.Validate(); err != nil {
		return Sequence{}, phaseCnt, err
	}
	if len(recs) > StackNMax {
		return Sequence{}, phaseCnt, fmt.Errorf("wordfmt: %d records > STACK_N_MAX %d", len(recs), StackNMax)
	}
	f := p.F()
	cells := int(p.Rows) * int(f) * RowCols
	accA, accB := make([]uint32, cells), make([]uint32, cells)
	const wrap = uint32(1)<<AccBits - 1
	for k, r := range recs {
		if uint(r.Phase) >= f {
			return Sequence{}, phaseCnt, fmt.Errorf("wordfmt: record %d phase %d >= F %d", k, r.Phase, f)
		}
		if len(r.CH1) != int(p.Rows)*RowCols || len(r.CH2) != int(p.Rows)*RowCols {
			return Sequence{}, phaseCnt, fmt.Errorf("wordfmt: record %d has %d/%d samples, want %d per channel", k, len(r.CH1), len(r.CH2), int(p.Rows)*RowCols)
		}
		phaseCnt[r.Phase]++
		for row := 0; row < int(p.Rows); row++ {
			base := (row*int(f) + int(r.Phase)) * RowCols
			for c := 0; c < RowCols; c++ {
				accA[base+c] = (accA[base+c] + uint32(r.CH1[row*RowCols+c])) & wrap
				accB[base+c] = (accB[base+c] + uint32(r.CH2[row*RowCols+c])) & wrap
			}
		}
	}
	ws := make([]uint16, 0, 2*cells)
	for _, acc := range [][]uint32{accA, accB} {
		for _, s := range acc {
			ws = append(ws, uint16(s>>p.Shift))
		}
	}
	pp := p
	return Sequence{Tier: t, Words: ws, Group: 1, StepNs: t.TickNs, Stack: &pp}, phaseCnt, nil
}
