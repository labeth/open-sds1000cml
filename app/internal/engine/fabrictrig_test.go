package engine

// Tests for the in-fabric decode+trigger host driver (fabrictrig.go).
//
// The encoder tests are the authoritative check that the app packs the SAME bit
// layout the RTL decodes (acq.v dec_cfg[*] + dec_trigger.v mode packing). Every
// expected word below is written out by hand from the documented layout, NOT
// re-derived from the encoder, so a bit that drifts in either direction fails.

import (
	"testing"

	"open-sds/app/internal/iface"
)

func TestEncodeFabricSerial_BitLayout(t *testing.T) {
	// CFG common bits: en[0]=1 and trig_en[8]=1 whenever armed => 0x0101 base.
	cases := []struct {
		name    string
		p       SerialParams
		ok      bool
		cfg     uint16
		thr     uint16
		match   uint16
		testgen uint16
		mode    int
		proto   int
		seqlen  int
	}{
		{
			// UART single-byte match 0x55, 8N1 default, C1. mode 0 (byte).
			name:    "uart_byte",
			p:       SerialParams{Proto: serUART, Bytes: []int{0x55}},
			ok:      true,
			cfg:     0x0101, // en|trig_en, proto=0 mode=0 seqlen=0
			thr:     0x8080, // default midscale, both lanes
			match:   0xFF55, // mask=0xFF exact, pattern=0x55
			testgen: 0x0000,
			mode:    fabTrigByte, proto: fabUART, seqlen: 0,
		},
		{
			// UART "any byte": no pattern => mask=0 (legacy comparator fires on any).
			name:    "uart_any",
			p:       SerialParams{Proto: serUART},
			ok:      true,
			cfg:     0x0101,
			thr:     0x8080,
			match:   0x0000,
			testgen: 0x0000,
			mode:    fabTrigByte, proto: fabUART, seqlen: 0,
		},
		{
			// UART parity=even, 7 data bits, source channel C2.
			// bits[5:2]=7 => 0x1C ; parity[7:6]=even(1) => 0x40 ; src[1]=1 => 0x02.
			name:    "uart_7e1_c2",
			p:       SerialParams{Proto: serUART, Bits: 7, Parity: "even", ChA: 1, Bytes: []int{0x41}},
			ok:      true,
			cfg:     0x0101 | 0x1C | 0x40 | 0x02, // 0x015F
			thr:     0x8080,
			match:   0xFF41,
			testgen: 0x0000,
			mode:    fabTrigByte, proto: fabUART, seqlen: 0,
		},
		{
			// UART sequence {AA,BB} (N=2): the CORRECTED packing from the mission.
			// mode=2 => 0x2000 ; seqlen_cfg=N-1=1 => [15:14]=01 => 0x4000.
			name:    "uart_seq2",
			p:       SerialParams{Proto: serUART, Bytes: []int{0xAA, 0xBB}},
			ok:      true,
			cfg:     0x0101 | 0x2000 | 0x4000, // 0x6101
			thr:     0x8080,
			match:   0x00AA, // seq0 -> MATCH[7:0]; seq3 unused => [15:8]=0
			testgen: 0x00BB, // seq1 -> TESTGEN[7:0]; seq2 unused => [15:8]=0
			mode:    fabTrigSeq, proto: fabUART, seqlen: 1,
		},
		{
			// UART sequence {AA,BB,CC} (N=3).
			name:    "uart_seq3",
			p:       SerialParams{Proto: serUART, Bytes: []int{0xAA, 0xBB, 0xCC}},
			ok:      true,
			cfg:     0x0101 | 0x2000 | 0x8000, // seqlen_cfg=2 => [15:14]=10 => 0x8000
			thr:     0x8080,
			match:   0x00AA, // seq0
			testgen: 0xCCBB, // seq1=BB[7:0], seq2=CC[15:8]
			mode:    fabTrigSeq, proto: fabUART, seqlen: 2,
		},
		{
			// UART sequence {AA,BB,CC,DD} (N=4): all four seq slots used.
			name:    "uart_seq4",
			p:       SerialParams{Proto: serUART, Bytes: []int{0xAA, 0xBB, 0xCC, 0xDD}},
			ok:      true,
			cfg:     0x0101 | 0x2000 | 0xC000, // seqlen_cfg=3 => [15:14]=11 => 0xC000
			thr:     0x8080,
			match:   0xDDAA, // seq0=AA[7:0], seq3=DD[15:8]
			testgen: 0xCCBB, // seq1=BB[7:0], seq2=CC[15:8]
			mode:    fabTrigSeq, proto: fabUART, seqlen: 3,
		},
		{
			// A >4-byte pattern is capped at 4 by the fabric sequence depth.
			name:    "uart_seq_cap4",
			p:       SerialParams{Proto: serUART, Bytes: []int{0xAA, 0xBB, 0xCC, 0xDD, 0xEE}},
			ok:      true,
			cfg:     0x0101 | 0x2000 | 0xC000,
			thr:     0x8080,
			match:   0xDDAA,
			testgen: 0xCCBB,
			mode:    fabTrigSeq, proto: fabUART, seqlen: 3,
		},
		{
			// SPI CPOL/CPHA/MSB=1, swapped channel, single byte 0x0F. mode 0.
			// CPOL[2]=0x04 CPHA[3]=0x08 MSB[4]=0x10 src/swap[1]=0x02 proto=2 => 0x0800.
			name:    "spi_mode3_msb",
			p:       SerialParams{Proto: serSPI, CPOL: true, CPHA: true, MSB: true, ChA: 1, Bytes: []int{0x0F}},
			ok:      true,
			cfg:     0x0101 | 0x0800 | 0x04 | 0x08 | 0x10 | 0x02, // 0x091F
			thr:     0x8080,
			match:   0xFF0F,
			testgen: 0x0000,
			mode:    fabTrigByte, proto: fabSPI, seqlen: 0,
		},
		{
			// I2C ADDRESS trigger (mode 3): addr 0x50 write, no data bytes.
			// proto=1 => 0x0400 ; mode=3 => 0x3000.
			// addr symbol = {addr7,rw}; addr_mask=0xFE|0x01(RW!=any)=0xFF,
			// addr_field=(0x50<<1)|0=0xA0 => MATCH=0xFFA0.
			name:    "i2c_addr_write",
			p:       SerialParams{Proto: serI2C, Addr: 0x50, RW: 0},
			ok:      true,
			cfg:     0x0101 | 0x0400 | 0x3000, // 0x3501
			thr:     0x8080,
			match:   0xFFA0,
			testgen: 0x0000,
			mode:    fabTrigAddr, proto: fabI2C, seqlen: 0,
		},
		{
			// I2C ADDRESS trigger, RW=any: bit0 unmasked => addr_mask=0xFE.
			name:    "i2c_addr_anyrw",
			p:       SerialParams{Proto: serI2C, Addr: 0x50, RW: 2},
			ok:      true,
			cfg:     0x0101 | 0x0400 | 0x3000,
			thr:     0x8080,
			match:   0xFEA0,
			testgen: 0x0000,
			mode:    fabTrigAddr, proto: fabI2C, seqlen: 0,
		},
		{
			// I2C with a data pattern is NOT an address arm: falls to byte mode
			// (single byte) — the fabric addr mode fires on the address symbol only.
			name:    "i2c_data_byte",
			p:       SerialParams{Proto: serI2C, Addr: 0x50, RW: 0, Bytes: []int{0x7E}},
			ok:      true,
			cfg:     0x0101 | 0x0400, // mode 0, proto=I2C
			thr:     0x8080,
			match:   0xFF7E,
			testgen: 0x0000,
			mode:    fabTrigByte, proto: fabI2C, seqlen: 0,
		},
		{
			// ERROR trigger (mode 1): err_mask packed into MATCH[15:8].
			name:    "uart_err",
			p:       SerialParams{Proto: serUART, ErrTrig: true, ErrMask: 0x03},
			ok:      true,
			cfg:     0x0101 | 0x1000, // mode=1 => [13:12]=01 => 0x1000
			thr:     0x8080,
			match:   0x0300, // err_mask = MATCH[15:8]
			testgen: 0x0000,
			mode:    fabTrigErr, proto: fabUART, seqlen: 0,
		},
		{
			// ERROR trigger with empty mask defaults to "any error flag" (0xFF).
			name:    "uart_err_default_mask",
			p:       SerialParams{Proto: serUART, ErrTrig: true, ErrMask: 0},
			ok:      true,
			cfg:     0x0101 | 0x1000,
			thr:     0x8080,
			match:   0xFF00,
			testgen: 0x0000,
			mode:    fabTrigErr, proto: fabUART, seqlen: 0,
		},
		{
			// Explicit threshold overrides the midscale default, both lanes.
			name:    "uart_havethr",
			p:       SerialParams{Proto: serUART, HaveThr: true, Threshold: 100, Bytes: []int{0x00}},
			ok:      true,
			cfg:     0x0101,
			thr:     0x6464, // 100 both lanes
			match:   0xFF00,
			testgen: 0x0000,
			mode:    fabTrigByte, proto: fabUART, seqlen: 0,
		},
		{
			// Non-fabric protocol => ok=false (caller keeps the software path).
			name: "manchester_unsupported",
			p:    SerialParams{Proto: serManchester, Bytes: []int{0x4D}},
			ok:   false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := encodeFabricSerial(c.p)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !c.ok {
				return
			}
			if got.CFG != c.cfg {
				t.Errorf("CFG = %#06x, want %#06x", got.CFG, c.cfg)
			}
			if got.THR != c.thr {
				t.Errorf("THR = %#06x, want %#06x", got.THR, c.thr)
			}
			if got.MATCH != c.match {
				t.Errorf("MATCH = %#06x, want %#06x", got.MATCH, c.match)
			}
			if got.TESTGEN != c.testgen {
				t.Errorf("TESTGEN = %#06x, want %#06x", got.TESTGEN, c.testgen)
			}
			if got.Mode != c.mode {
				t.Errorf("Mode = %d, want %d", got.Mode, c.mode)
			}
			if got.Proto != c.proto {
				t.Errorf("Proto = %d, want %d", got.Proto, c.proto)
			}
			if got.SeqLen != c.seqlen {
				t.Errorf("SeqLen = %d, want %d", got.SeqLen, c.seqlen)
			}
			// Cross-check the CFG sub-fields against the resolved values, so the
			// documented [11:10]/[13:12]/[15:14] positions are pinned independently.
			if p := int(got.CFG>>cfgProtoSh) & 0x3; p != c.proto {
				t.Errorf("CFG proto field = %d, want %d", p, c.proto)
			}
			if m := int(got.CFG>>cfgModeSh) & 0x3; m != c.mode {
				t.Errorf("CFG mode field = %d, want %d", m, c.mode)
			}
			if s := int(got.CFG>>cfgSeqSh) & 0x3; s != c.seqlen {
				t.Errorf("CFG seqlen field = %d, want %d", s, c.seqlen)
			}
			if got.CFG&cfgEn == 0 || got.CFG&cfgTrigEn == 0 {
				t.Errorf("CFG = %#06x: en+trig_en must both be set when armed", got.CFG)
			}
			if got.CFG&cfgTgEn != 0 {
				t.Errorf("CFG = %#06x: tg_en must be CLEAR (no synthetic inject on a real arm)", got.CFG)
			}
		})
	}
}

func TestFabricSPB(t *testing.T) {
	// 1 GSa/s capture, 115200 baud => spb = 1e9/115200 = 8680.55.. samples/bit.
	// Q16.8: round(8680.55 * 256) = 2222222 = 0x21E88E => lo=0xE88E hi=0x21.
	lo, hi, ok := fabricSPB(115200, 1e-9)
	if !ok {
		t.Fatal("ok=false for a valid baud/interval")
	}
	if lo != 0xE88E || hi != 0x21 {
		t.Errorf("SPB = {hi %#04x, lo %#06x}, want {0x21, 0xe88e}", hi, lo)
	}
	if _, _, ok := fabricSPB(0, 1e-9); ok {
		t.Error("baud=0 (auto) must report ok=false — the fabric has no auto-baud")
	}
	if _, _, ok := fabricSPB(115200, 0); ok {
		t.Error("interval<=0 must report ok=false")
	}
}

// recBus records WriteSpare calls and scripts the result-side reads. Implements
// the fabricBus surface (WriteSpare + Read). wr is defined in engine_test.go.
type recBus struct {
	writes  []wr
	status  uint16
	matched uint16
	byteq   []uint16
	bytepos int
}

func (r *recBus) WriteSpare(sel, val uint16) error {
	r.writes = append(r.writes, wr{iface.CS1, sel, val})
	return nil
}

func (r *recBus) Read(plane iface.Plane, sel uint16) (uint16, error) {
	switch sel {
	case selDecSTATUS:
		return r.status, nil
	case selDecMATCHED:
		return r.matched, nil
	case selDecBYTE:
		if r.bytepos < len(r.byteq) {
			v := r.byteq[r.bytepos]
			r.bytepos++
			return v, nil
		}
		return 0, nil
	}
	return 0, nil
}

func TestFabricArmSequence(t *testing.T) {
	rb := &recBus{}
	ft := NewFabricTrig(rb)
	regs, ok := encodeFabricSerial(SerialParams{Proto: serUART, Bytes: []int{0x55}})
	if !ok {
		t.Fatal("encode failed")
	}
	if err := ft.Arm(regs, 0x1234, 0x0056); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	// CFG=0 FIRST (inert + clears sticky/overflow), then THR/SPB/MATCH/TESTGEN,
	// then the live CFG LAST so en+trig_en assert on a fully-loaded config.
	want := []wr{
		{iface.CS1, selDecCFG, 0x0000},
		{iface.CS1, selDecTHR, regs.THR},
		{iface.CS1, selDecSPBLo, 0x1234},
		{iface.CS1, selDecSPBHi, 0x0056},
		{iface.CS1, selDecMATCH, regs.MATCH},
		{iface.CS1, selDecTESTGEN, regs.TESTGEN},
		{iface.CS1, selDecCFG, regs.CFG},
	}
	wantWrites(t, rb.writes, want)

	rb.writes = nil
	if err := ft.Disarm(); err != nil {
		t.Fatalf("Disarm: %v", err)
	}
	wantWrites(t, rb.writes, []wr{{iface.CS1, selDecCFG, 0x0000}})
}

func TestFabricPoll(t *testing.T) {
	// STATUS: ovf[15]=1, matched[14]=1, busy[13]=1, fill=5.
	// MATCHED (0x6c) is data-only in the RTL — byte in [7:0], [9:8] are 0.
	rb := &recBus{status: 0x8000 | 0x4000 | 0x2000 | 5, matched: 0x41}
	ft := NewFabricTrig(rb)
	s, err := ft.Poll()
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !s.Overflow || !s.Matched || !s.Busy {
		t.Errorf("status flags = %+v, want ovf/matched/busy all set", s)
	}
	if s.Fill != 5 {
		t.Errorf("Fill = %d, want 5", s.Fill)
	}
	if s.MatchedByte != 0x41 {
		t.Errorf("MatchedByte = %#02x, want 0x41", s.MatchedByte)
	}
}

func TestFabricDrainBytes(t *testing.T) {
	// fill=3 => pop exactly 3; the 2nd byte carries a parity-error flag.
	rb := &recBus{
		status: 3,
		byteq:  []uint16{0x0048, 0x0100 | 0x69, 0x0021}, // 'H', 'i'(perr), '!'
	}
	ft := NewFabricTrig(rb)
	dst := make([]DecodedByte, 8)
	n, err := ft.DrainBytes(dst)
	if err != nil {
		t.Fatalf("DrainBytes: %v", err)
	}
	if n != 3 {
		t.Fatalf("drained %d, want 3", n)
	}
	if dst[0].Data != 'H' || dst[0].ParityErr {
		t.Errorf("dst[0] = %+v, want clean 'H'", dst[0])
	}
	if dst[1].Data != 'i' || !dst[1].ParityErr {
		t.Errorf("dst[1] = %+v, want 'i' with parity error", dst[1])
	}
	if dst[2].Data != '!' {
		t.Errorf("dst[2] = %+v, want '!'", dst[2])
	}
	// Draining stops at fill: the FIFO was popped exactly 3 times.
	if rb.bytepos != 3 {
		t.Errorf("popped %d times, want 3 (fill)", rb.bytepos)
	}
}

// TestServiceFabricDecode_Integration drives the engine glue against the package
// fakeBus: enabling the fabric flag + arming the trigger must program the decode
// registers exactly once and latch fabArmed; clearing it must disarm once.
func TestServiceFabricDecode_Integration(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)

	// Off by default: servicing does nothing, software path intact.
	e.serviceFabricDecode()
	if e.fabArmed {
		t.Fatal("fabArmed with fabric flag off")
	}

	// Arm the fabric UART sequence trigger.
	e.SetSerialParams(SerialParams{Proto: serUART, Baud: 115200, Bytes: []int{0xAA, 0xBB}, Fabric: true})
	e.SetSerialMode(SerialTrigger)
	fb.clearWrites()
	e.serviceFabricDecode()
	if !e.fabArmed {
		t.Fatal("fabArmed false after arming a fabric UART trigger")
	}
	regs, _ := encodeFabricSerial(SerialParams{Proto: serUART, Baud: 115200, Bytes: []int{0xAA, 0xBB}})
	spbLo, spbHi, _ := fabricSPB(115200, e.band.CaptureIntervalNs()*1e-9)
	want := []wr{
		{iface.CS1, selDecCFG, 0x0000},
		{iface.CS1, selDecTHR, regs.THR},
		{iface.CS1, selDecSPBLo, spbLo},
		{iface.CS1, selDecSPBHi, spbHi},
		{iface.CS1, selDecMATCH, regs.MATCH},
		{iface.CS1, selDecTESTGEN, regs.TESTGEN},
		{iface.CS1, selDecCFG, regs.CFG},
	}
	wantWrites(t, fb.snapWrites(), want)

	// Idempotent: an unchanged config must NOT re-arm (a bare re-arm would wipe
	// the sticky match). No new writes on the second service.
	fb.clearWrites()
	e.serviceFabricDecode()
	if got := fb.snapWrites(); len(got) != 0 {
		t.Fatalf("re-armed on unchanged config: %d writes", len(got))
	}

	// Disarm via the flag: exactly one CFG=0, and fabArmed clears.
	e.SetSerialParams(SerialParams{Proto: serUART, Baud: 115200, Bytes: []int{0xAA, 0xBB}, Fabric: false})
	fb.clearWrites()
	e.serviceFabricDecode()
	if e.fabArmed {
		t.Fatal("fabArmed still set after clearing the fabric flag")
	}
	wantWrites(t, fb.snapWrites(), []wr{{iface.CS1, selDecCFG, 0x0000}})
}

// TestServiceFabricDecode_UnsupportedProto: a non-fabric protocol with the flag
// on must NOT arm — the engine falls back to the software path.
func TestServiceFabricDecode_UnsupportedProto(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetSerialParams(SerialParams{Proto: serManchester, Bytes: []int{0x4D}, Fabric: true})
	e.SetSerialMode(SerialTrigger)
	fb.clearWrites()
	e.serviceFabricDecode()
	if e.fabArmed {
		t.Fatal("fabArmed set for a non-fabric-decodable protocol")
	}
	if got := fb.snapWrites(); len(got) != 0 {
		t.Fatalf("wrote decode registers for an unsupported protocol: %d writes", len(got))
	}
}

// TestServiceFabricDecode_UARTNoBaud: fabric UART needs an explicit baud (no
// auto-baud in the fabric); with baud=0 the engine must fall back to software
// (not arm) even though the protocol itself is fabric-decodable.
func TestServiceFabricDecode_UARTNoBaud(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetSerialParams(SerialParams{Proto: serUART, Baud: 0, Bytes: []int{0x55}, Fabric: true})
	e.SetSerialMode(SerialTrigger)
	fb.clearWrites()
	e.serviceFabricDecode()
	if e.fabArmed {
		t.Fatal("fabArmed set for fabric UART with auto-baud (baud=0)")
	}
	if got := fb.snapWrites(); len(got) != 0 {
		t.Fatalf("armed fabric UART without a baud: %d writes", len(got))
	}
}
