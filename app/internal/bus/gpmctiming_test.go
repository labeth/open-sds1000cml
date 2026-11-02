// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"testing"
)

// TRLC-LINKS: REQ-SDS-131
func TestCS1TimingFieldCodec(t *testing.T) {
	f := FactoryCS1Timing.Fields()
	// fpga-specs 10 §4.3: CONFIG2 0x00141400, CONFIG4 0x10041004, CONFIG5 0x010d141f, CONFIG6 0x060005c1.
	want := TimingFields{
		RdCycle: 31, RdAccess: 13, OEOn: 4, OEOff: 16, CSOn: 0, CSRdOff: 20, Gap: 5, SameCSEn: true,
		WrCycle: 20, WrAccess: 6, WEOn: 4, WEOff: 16, CSWrOff: 20,
	}
	if f != want {
		t.Fatalf("factory fields %+v, want %+v", f, want)
	}
	n := FactoryCS1Timing.WithRdAccess(9).WithRdCycle(12).WithOEOff(11).WithCSRdOff(12).WithGap(4)
	if n.RdAccess() != 9 || n.RdCycle() != 12 || n.OEOff() != 11 || n.CSRdOff() != 12 || n.Gap() != 4 || !n.SameCSEn() {
		t.Fatalf("setters: %s", n)
	}
	if n.WrCycle() != 20 || n.WEOff() != 16 || n.CSWrOff() != 20 || n.Config1 != FactoryCS1Timing.Config1 {
		t.Fatalf("setters disturbed the write side / CONFIG1: %+v", n)
	}
	if n.Config5 != 0x0109140c {
		t.Fatalf("CONFIG5 = %#08x, want 0x0109140c", n.Config5)
	}
	if err := n.Validate(); err != nil {
		t.Fatalf("valid timing refused: %v", err)
	}
	if FactoryCS1Timing.ReadTicks() != 36 {
		t.Fatalf("ReadTicks = %d, want 36 (31 + gap 5 = the 360 ns/word of the R0 report at 100 MHz)", FactoryCS1Timing.ReadTicks())
	}
}

// TRLC-LINKS: REQ-SDS-131
func TestCS1TimingValidate(t *testing.T) {
	bad := []CS1Timing{
		FactoryCS1Timing.WithRdAccess(31),                                                    // access >= cycle
		FactoryCS1Timing.WithRdAccess(6),                                                     // below OEON + 3
		FactoryCS1Timing.WithRdAccess(16),                                                    // not latched before OEOFF
		FactoryCS1Timing.WithRdCycle(15),                                                     // OEOFF beyond the cycle
		FactoryCS1Timing.WithRdCycle(18),                                                     // CSRDOFF beyond the cycle
		FactoryCS1Timing.WithCSRdOff(15),                                                     // nCS releases before nOE
		FactoryCS1Timing.WithOEOn(3).WithRdAccess(6),                                         // fine for the fabric... no: CSON 0 <= 3 ok, access 6 = 3+3 ok, but access must be >= OEON+3 = 6 → valid; keep as a positive case below
		{Config2: 0x00141400, Config4: 0x10041004, Config5: 0x010d141f, Config6: 0x06000541}, // gap enable off
		FactoryCS1Timing.WithGap(0),
	}
	for i, tm := range bad {
		if i == 6 {
			if err := tm.Validate(); err != nil {
				t.Errorf("case %d (%s) should be valid: %v", i, tm, err)
			}
			continue
		}
		if err := tm.Validate(); err == nil {
			t.Errorf("case %d (%s) accepted", i, tm)
		}
	}
	if err := FactoryCS1Timing.Validate(); err != nil {
		t.Fatalf("factory timing refused: %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-131
func TestApplyTimingSequence(t *testing.T) {
	g := &fakeGPMC{regs: map[uint32]uint32{
		gpmcConfig1: 0x00001001, gpmcConfig2: 0x00141400, gpmcConfig3: 0x00020201,
		gpmcConfig4: 0x10041004, gpmcConfig5: 0x010d141f, gpmcConfig6: 0x060005c1, gpmcConfig7: 0x00000F41,
	}}
	got := readTiming(g)
	if got != FactoryCS1Timing {
		t.Fatalf("readTiming %+v", got)
	}
	n := FactoryCS1Timing.WithRdAccess(9).WithRdCycle(14).WithOEOff(14).WithCSRdOff(14)
	if err := applyTiming(g, n, true); err != nil {
		t.Fatal(err)
	}
	if g.regs[gpmcConfig5] != n.Config5 || g.regs[gpmcConfig4] != n.Config4 || g.regs[gpmcConfig2] != n.Config2 {
		t.Fatalf("timing not written: %+v", readTiming(g))
	}
	if g.regs[gpmcConfig7] != 0x00000F41 || g.regs[gpmcConfig1] != 0x00001001 {
		t.Fatalf("CONFIG1/7 disturbed: %+v", readTiming(g))
	}
	want := []string{"csvalid-off", "cfg6", "csvalid-on"}
	if len(g.events) != 3 || g.events[0] != want[0] || g.events[1] != want[1] || g.events[2] != want[2] {
		t.Fatalf("events %v, want %v (CSVALID quiesced around the retiming)", g.events, want)
	}
	if err := applyTiming(g, FactoryCS1Timing.WithRdAccess(30), true); err == nil {
		t.Fatal("invalid timing written")
	}
	if err := applyTiming(g, FactoryCS1Timing.WithRdAccess(30), false); err != nil {
		t.Fatalf("restore path must not validate: %v", err)
	}
}
