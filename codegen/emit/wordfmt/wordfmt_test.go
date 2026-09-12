package wordfmt

import (
	"math"
	"testing"
)

func ramp(n int, start uint8) []uint8 {
	s := make([]uint8, n)
	for i := range s {
		s[i] = start + uint8(i)
	}
	return s
}

func TestTier(t *testing.T) {
	if TierIL5x100.PairOfWord(6) != 2 || TierIL5x200.PairOfWord(6) != 1 || TierDualE1.PairOfWord(6) != 0 {
		t.Error("PairOfWord")
	}
	if c := TierIL5x100.ColumnsOfPair(1); len(c) != 1 || c[0] != 3 {
		t.Errorf("E2 fills column 3 at IL5-100, got %v", c)
	}
	if c := TierDualE1.ColumnsOfPair(0); len(c) != RowCols {
		t.Errorf("E1 fills every column in dual-E1, got %v", c)
	}
	if c := TierDualE1.ColumnsOfPair(3); c != nil {
		t.Errorf("E4 fills nothing in dual-E1, got %v", c)
	}
	if d := TierDualE1.Decimated(4); d.TickNs != 20 || TierDualE1.Decimated(0).TickNs != 5 {
		t.Error("Decimated")
	}
	// the IL5-100 column order is E1,E3,E5,E2,E4 (06-TIERS §1.2.2) and every pair appears once
	seen := map[uint8]bool{}
	for _, p := range TierIL5x100.Cols {
		seen[p] = true
	}
	if len(seen) != 5 || TierIL5x100.Cols != [RowCols]uint8{0, 2, 4, 1, 3} {
		t.Errorf("IL5-100 column order %v", TierIL5x100.Cols)
	}
}

func TestWordSplit(t *testing.T) {
	if Word(0x12, 0x34) != 0x1234 {
		t.Error("Word")
	}
	a, b := Split(0xabcd)
	if a != 0xab || b != 0xcd {
		t.Error("Split")
	}
	c1, c2 := SplitWords([]uint16{0x0102, 0x0304})
	if c1[1] != 3 || c2[0] != 2 {
		t.Error("SplitWords")
	}
}

func TestModelRecord(t *testing.T) {
	ch1, ch2 := ramp(6, 10), ramp(7, 100)
	s, err := ModelRecord(TierIL5x100, RunChmodeDual, ch1, ch2)
	if err != nil || len(s.Words) != 6 || s.Words[2] != 0x0c66 || s.TimeNs(3) != 6 || s.StepNs != 2 {
		t.Fatalf("dual: %v %v", s, err)
	}
	s, err = ModelRecord(TierDualE1.Decimated(2), RunChmodeCh1, ch1, ch2)
	if err != nil || len(s.Words) != 3 || s.Words[0] != 0x0a0b || s.Words[2] != 0x0e0f || s.TimeNs(1) != 20 {
		t.Fatalf("CH1: %v %v", s, err)
	}
	s, err = ModelRecord(TierIL5x200, RunChmodeCh2, ch1, ch2)
	if err != nil || len(s.Words) != 3 || s.Words[1] != 0x6667 || s.StepNs != 2 {
		t.Fatalf("CH2: %v %v", s, err)
	}
	if _, err = ModelRecord(TierIL5x200, 3, ch1, ch2); err == nil {
		t.Error("CHMODE 3 accepted")
	}
}

func TestTsrc(t *testing.T) {
	cases := []struct {
		mode uint16
		k    uint32
		want uint16
	}{
		{IlCtrlTsrcRamp, 0, 0}, {IlCtrlTsrcRamp, 0x1ffff, 0xffff}, {IlCtrlTsrcRamp, 20479, 20479},
		{IlCtrlTsrcColtag, 0, 0x0000}, {IlCtrlTsrcColtag, 4, 0x0400}, {IlCtrlTsrcColtag, 5, 0x0001},
		{IlCtrlTsrcColtag, 5*255 + 3, 0x03ff}, {IlCtrlTsrcColtag, 5 * 256, 0x0000},
		{IlCtrlTsrcGlitch, 0, 0xffff}, {IlCtrlTsrcGlitch, 1, 0}, {IlCtrlTsrcGlitch, 511, 0}, {IlCtrlTsrcGlitch, 1024, 0xffff},
	}
	for _, c := range cases {
		got, err := TsrcWord(c.mode, c.k)
		if err != nil || got != c.want {
			t.Errorf("TsrcWord(%d, %d) = 0x%04x %v, want 0x%04x", c.mode, c.k, got, err, c.want)
		}
	}
	if _, err := TsrcWord(IlCtrlTsrcAdc, 0); err == nil {
		t.Error("ADC is not a pattern")
	}
	for _, mode := range []uint16{IlCtrlTsrcRamp, IlCtrlTsrcColtag, IlCtrlTsrcGlitch} {
		for _, k0 := range []uint32{0, 1, 7, 511, 512, 1279, 1280, 65535, 65536, 100000} {
			s, err := ModelTsrc(TierDualE1, mode, k0, 20478)
			if err != nil {
				t.Fatal(err)
			}
			// the sequence is self-consistent under TsrcCheck
			k, bad, err := TsrcCheck(mode, s.Words)
			if err != nil || len(bad) != 0 {
				t.Fatalf("mode %d k0 %d: check k=%d bad=%v err=%v", mode, k0, k, bad, err)
			}
			// one corrupted word is found, nothing else
			w := append([]uint16(nil), s.Words...)
			w[1234] ^= 0x0100
			_, bad, _ = TsrcCheck(mode, w)
			if len(bad) != 1 || bad[0] != 1234 {
				t.Errorf("mode %d k0 %d: corrupted word not isolated: %v", mode, k0, bad)
			}
			// a duplicated word (the R1 failure signature) shifts everything after it
			w = append(append([]uint16(nil), s.Words[:100]...), s.Words[99:]...)
			_, bad, _ = TsrcCheck(mode, w)
			if mode != IlCtrlTsrcGlitch && len(bad) < len(w)/2 {
				t.Errorf("mode %d: duplicated word not detected (%d bad)", mode, len(bad))
			}
		}
	}
	// recovered origins
	s, _ := ModelTsrc(TierDualE1, IlCtrlTsrcRamp, 40000, 10)
	if k, _, _ := TsrcCheck(IlCtrlTsrcRamp, s.Words); k != 40000 {
		t.Errorf("ramp origin %d", k)
	}
	s, _ = ModelTsrc(TierDualE1, IlCtrlTsrcColtag, 1280+7, 10)
	if k, _, _ := TsrcCheck(IlCtrlTsrcColtag, s.Words); k != 7 {
		t.Errorf("coltag origin %d (mod 1280)", k)
	}
	s, _ = ModelTsrc(TierDualE1, IlCtrlTsrcGlitch, 512*3+500, 1000)
	if k, _, _ := TsrcCheck(IlCtrlTsrcGlitch, s.Words); k != 500 {
		t.Errorf("glitch origin %d (mod 512)", k)
	}
	if _, _, err := TsrcCheck(IlCtrlTsrcColtag, []uint16{0x0500}); err == nil {
		t.Error("column 5 accepted as a tag")
	}
	if _, _, err := TsrcCheck(IlCtrlTsrcGlitch, make([]uint16, 512)); err == nil {
		t.Error("512 zero words accepted as a glitch source")
	}
	if k, bad, err := TsrcCheck(IlCtrlTsrcGlitch, make([]uint16, 100)); err != nil || len(bad) != 0 || k != 1 {
		t.Errorf("short zero window: k=%d bad=%v err=%v", k, bad, err)
	}
	if _, _, err := TsrcCheck(IlCtrlTsrcRamp, nil); err == nil {
		t.Error("empty accepted")
	}
	if _, err := ModelTsrc(TierDualE1, IlCtrlTsrcAdc, 0, 3); err == nil {
		t.Error("ADC modelled")
	}
}

func TestModelDecim(t *testing.T) {
	ch1, ch2 := ramp(11, 0), ramp(11, 50)
	s, err := ModelDecim(TierDualE1, 4, ch1, ch2)
	if err != nil || len(s.Words) != 2 || s.Words[1] != 0x0436 || s.StepNs != 20 || s.TimeNs(1) != 20 {
		t.Fatalf("%v %v", s, err)
	}
	if _, err := ModelDecim(TierDualE1, 0, ch1, ch2); err == nil {
		t.Error("D=0 accepted")
	}
}

func TestModelPeak(t *testing.T) {
	ch1 := []uint8{5, 9, 1, 7, 200, 0, 3, 3, 8}
	ch2 := []uint8{50, 40, 60, 55, 10, 250, 30, 30, 99}
	s, err := ModelPeak(TierIL5x100, 4, ch1, ch2)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{Word(1, 40), Word(9, 60), Word(0, 10), Word(200, 250)}
	if len(s.Words) != 4 {
		t.Fatalf("%d words", len(s.Words))
	}
	for i := range want {
		if s.Words[i] != want[i] {
			t.Errorf("word %d = 0x%04x, want 0x%04x", i, s.Words[i], want[i])
		}
	}
	if s.Group != 2 || s.TimeNs(1) != 0 || s.TimeNs(2) != 8 || s.TimeNs(3) != 8 {
		t.Errorf("timing: group %d, %v %v", s.Group, s.TimeNs(2), s.TimeNs(3))
	}
	// the glitch source through PEAK: min 0 / max 255 in the block with the glitch, 0/0 elsewhere
	g, _ := ModelTsrc(TierIL5x200, IlCtrlTsrcGlitch, 0, 2048)
	c1, c2 := SplitWords(g.Words)
	s, _ = ModelPeak(TierIL5x200, 256, c1, c2)
	if len(s.Words) != 16 || s.Words[0] != 0 || s.Words[1] != 0xffff || s.Words[2] != 0 || s.Words[3] != 0 || s.Words[5] != 0xffff {
		t.Errorf("glitch through PEAK: %04x", s.Words)
	}
}

func TestBoxcarParams(t *testing.T) {
	for _, d := range []uint32{2, 3, 5, 10, 16, 100, 200, 255, 256, 257, 300, 512, 1000, 4096, 65535, 65536} {
		recip, pre, err := BoxcarParams(d)
		if err != nil {
			t.Fatal(err)
		}
		div := d >> pre
		if div < 2 || div > 256 {
			t.Errorf("D=%d: D>>PRE = %d outside [2,256]", d, div)
		}
		if uint32(recip) != uint32(math.Round(math.Ldexp(1, 16+int(pre))/float64(d))) || recip < 256 {
			t.Errorf("D=%d: RECIP %d", d, recip)
		}
		if d <= 256 || d&(d-1) == 0 {
			// where D >> PRE is exact the design's own formula gives the same constant
			if uint32(recip) != uint32(math.Round(65536/float64(div))) {
				t.Errorf("D=%d: RECIP %d differs from round(2^16/(D>>PRE))", d, recip)
			}
		}
		// full-scale mean: within the 16-bit reciprocal's rounding (0.5/RECIP) plus the sum truncation
		sum := d * 255
		mean := float64(MeanQ88(sum, recip, pre)) / 256
		bound := 0.5/float64(recip) + float64(uint32(1)<<pre)/float64(sum) + 1.0/(256*255)
		if rel := math.Abs(mean-255) / 255; rel > bound {
			t.Errorf("D=%d: mean %.5f rel err %.2e > %.2e", d, mean, rel, bound)
		}
		// mid-scale: same bound
		mean = float64(MeanQ88(d*100, recip, pre)) / 256
		if rel := math.Abs(mean-100) / 100; rel > 0.5/float64(recip)+float64(uint32(1)<<pre)/float64(d*100)+1.0/(256*100) {
			t.Errorf("D=%d: mean %.5f of 100 rel err %.2e", d, mean, rel)
		}
		if lg := math.Ceil(math.Log2(float64(d))); pre != uint16(math.Max(0, lg-8)) {
			t.Errorf("D=%d: PRE %d", d, pre)
		}
	}
	for _, d := range []uint32{0, 1, 65537} {
		if _, _, err := BoxcarParams(d); err == nil {
			t.Errorf("D=%d accepted", d)
		}
	}
	// the worked example of the design: D=10, sum 1000 -> 100.004 in Q8.8
	if q := MeanQ88(1000, 6554, 0); q != 0x6401 {
		t.Errorf("MeanQ88 = 0x%04x", q)
	}
	if Round8(0x6401) != 100 || Round8(0x647f) != 100 || Round8(0x6480) != 101 || Round8(0xff80) != 255 || Round8(0xffff) != 255 {
		t.Error("Round8")
	}
}

func TestModelBoxcar(t *testing.T) {
	// D=8, constant blocks: exact means
	ch1 := append(append(make([]uint8, 0, 24), fill(8, 100)...), append(fill(8, 7), fill(8, 255)...)...)
	ch2 := append(append(make([]uint8, 0, 24), fill(8, 0)...), append(fill(8, 200), fill(8, 1)...)...)
	s16, err := ModelBoxcar16(TierDualE1.Decimated(8), 8, ch1, ch2)
	if err != nil || len(s16.Words) != 6 {
		t.Fatalf("%v %v", s16, err)
	}
	want := []uint16{100 << 8, 0, 7 << 8, 200 << 8, 255 << 8, 1 << 8}
	for i := range want {
		if s16.Words[i] != want[i] {
			t.Errorf("BOXCAR16 word %d = 0x%04x, want 0x%04x", i, s16.Words[i], want[i])
		}
	}
	if s16.Group != 2 || s16.StepNs != 8*40 {
		t.Errorf("BOXCAR16 timing %+v", s16)
	}
	s8, err := ModelBoxcar8(TierIL5x100, 8, ch1, ch2)
	if err != nil || len(s8.Words) != 3 || s8.Words[0] != 0x6400 || s8.Words[1] != 0x07c8 || s8.Words[2] != 0xff01 {
		t.Fatalf("BOXCAR8 %04x %v", s8.Words, err)
	}
	// a non-integer mean rounds: {1,2} -> 1.5 -> 2; {0,1} -> 0.5 -> 1 (Q8.8 0x0080 rounds up)
	s8, _ = ModelBoxcar8(TierIL5x100, 2, []uint8{1, 2}, []uint8{0, 1})
	if s8.Words[0] != 0x0201 {
		t.Errorf("rounding: 0x%04x", s8.Words[0])
	}
	// the ramp source through BOXCAR8 at D=2: consecutive words k, k+1 -> CH1 = high bytes equal, CH2 mean of k, k+1
	r, _ := ModelTsrc(TierDualE1, IlCtrlTsrcRamp, 0, 16)
	c1, c2 := SplitWords(r.Words)
	s8, _ = ModelBoxcar8(TierDualE1, 2, c1, c2)
	if s8.Words[0] != Word(0, 1) || s8.Words[7] != Word(0, 15) {
		t.Errorf("ramp through BOXCAR8: %04x", s8.Words)
	}
	if _, err := ModelBoxcar8(TierIL5x100, 1, c1, c2); err == nil {
		t.Error("D=1 accepted")
	}
}

func fill(n int, v uint8) []uint8 {
	s := make([]uint8, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func TestStackParams(t *testing.T) {
	p := StackParams{PhaseBins: 3, Shift: 2, Rows: 256}
	if p.F() != 8 || p.Words() != 2*256*8*5 || p.Validate() != nil {
		t.Errorf("%+v: F %d words %d err %v", p, p.F(), p.Words(), p.Validate())
	}
	for _, bad := range []StackParams{{PhaseBins: 4, Rows: 1}, {Shift: 4, Rows: 1}, {Rows: 0}, {PhaseBins: 3, Rows: 257}, {Rows: 2049}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if (StackParams{Rows: 2048}).Validate() != nil || (StackParams{PhaseBins: 1, Rows: 1024}).Validate() != nil {
		t.Error("the full accumulator refused")
	}
	if PhaseBin(0xffff, 3) != 7 || PhaseBin(0x8000, 1) != 1 || PhaseBin(0x7fff, 1) != 0 || PhaseBin(0xffff, 0) != 0 || PhaseBin(0x4000, 2) != 1 {
		t.Error("PhaseBin")
	}
	// readout time: F=2, word i -> a = i/5 = row*2+phase
	p = StackParams{PhaseBins: 1, Rows: 4}
	tt := TierIL5x100 // tick 2 ns
	// i=47 wraps into the CH2 block: 47-40 = 7 -> a=1 (row 0, phase 1), c=2 -> 2*2 + 1 = 5
	if p.TimeNs(tt, 0) != 0 || p.TimeNs(tt, 1) != 2 || p.TimeNs(tt, 5) != 1 || p.TimeNs(tt, 10) != 10 || p.TimeNs(tt, 5*8+7) != 5 {
		t.Errorf("TimeNs: %v %v %v %v %v", p.TimeNs(tt, 0), p.TimeNs(tt, 1), p.TimeNs(tt, 5), p.TimeNs(tt, 10), p.TimeNs(tt, 5*8+7))
	}
}

func TestModelStack(t *testing.T) {
	p := StackParams{PhaseBins: 1, Shift: 0, Rows: 3}
	n := int(p.Rows) * RowCols
	recs := []StackRecord{
		{Phase: 0, CH1: fill(n, 10), CH2: fill(n, 1)},
		{Phase: 1, CH1: ramp(n, 0), CH2: fill(n, 2)},
		{Phase: 0, CH1: fill(n, 5), CH2: fill(n, 3)},
	}
	s, cnt, err := ModelStack(TierIL5x100, p, recs)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != [8]uint16{2, 1} {
		t.Errorf("PHASE_CNT %v", cnt)
	}
	if len(s.Words) != p.Words() || s.Stack == nil {
		t.Fatalf("%d words", len(s.Words))
	}
	half := p.Words() / 2
	for i := 0; i < half; i++ {
		a, c := i/RowCols, i%RowCols
		row, phase := a/2, a%2
		var want1, want2 uint16
		if phase == 0 {
			want1, want2 = 15, 4
		} else {
			want1, want2 = uint16(row*RowCols+c), 2
		}
		if s.Words[i] != want1 || s.Words[half+i] != want2 {
			t.Errorf("word %d: CH1 %d CH2 %d, want %d %d", i, s.Words[i], s.Words[half+i], want1, want2)
		}
	}
	// word 6: a = 1 (row 0, phase 1), c = 1 -> (0*5+1)*2 + 1*2/2 = 3
	if s.TimeNs(6) != 3 {
		t.Errorf("TimeNs(6) = %v", s.TimeNs(6))
	}
	// SHIFT and the 18-bit wrap: 1024 records of 255 = 261120 < 2^18, no wrap; SHIFT 2 -> 65280
	p = StackParams{Shift: 2, Rows: 1}
	full := make([]StackRecord, StackNMax)
	for i := range full {
		full[i] = StackRecord{CH1: fill(RowCols, 255), CH2: fill(RowCols, 255)}
	}
	s, cnt, err = ModelStack(TierIL5x200, p, full)
	if err != nil || s.Words[0] != 261120>>2 || s.Words[9] != 261120>>2 || cnt[0] != 1024 {
		t.Errorf("full batch: %v %v %v", s.Words, cnt, err)
	}
	p.Shift = 0
	s, _, _ = ModelStack(TierIL5x200, p, full)
	if s.Words[0] != uint16(261120&0xffff) {
		t.Errorf("SHIFT 0 truncates to the low 16 bits: %d", s.Words[0])
	}
	// errors
	if _, _, err = ModelStack(TierIL5x200, p, append(full, full[0])); err == nil {
		t.Error("N > STACK_N_MAX accepted")
	}
	if _, _, err = ModelStack(TierIL5x200, StackParams{Rows: 1}, []StackRecord{{Phase: 1, CH1: fill(5, 0), CH2: fill(5, 0)}}); err == nil {
		t.Error("phase >= F accepted")
	}
	if _, _, err = ModelStack(TierIL5x200, StackParams{Rows: 2}, []StackRecord{{CH1: fill(5, 0), CH2: fill(10, 0)}}); err == nil {
		t.Error("short window accepted")
	}
	if _, _, err = ModelStack(TierIL5x200, StackParams{Rows: 0}, nil); err == nil {
		t.Error("bad params accepted")
	}
	// an empty batch (HALT before any trigger) drains zeros
	s, cnt, err = ModelStack(TierIL5x200, StackParams{Rows: 2}, nil)
	if err != nil || len(s.Words) != 20 || s.Words[19] != 0 || cnt != [8]uint16{} {
		t.Errorf("empty batch: %v", err)
	}
}

func TestSequenceTime(t *testing.T) {
	s := Sequence{Tier: TierIL5x100, Words: make([]uint16, 4), StepNs: 2}
	if s.TimeNs(3) != 6 { // Group 0 counts as 1
		t.Error("Group 0")
	}
}
