// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	"github.com/labeth/open-dso-core/decode"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestPlanHardwarePacket(t *testing.T) {
	const all = 1<<6 | 1<<8 | 1<<10
	base := core.SerialParams{HaveThr: true, Threshold: 128, ChA: 1}
	with := func(f func(*core.SerialParams)) core.SerialParams { p := base; f(&p); return p }
	for _, c := range []struct {
		name  string
		p     core.SerialParams
		mask  uint16
		arinc uint32
		want  sramcapture.PacketTriggerConfig
	}{
		{"can-fd", with(func(p *core.SerialParams) {
			p.Proto, p.Baud, p.DataBaud, p.Bytes = core.SerCAN, 1000000, 5000000, []int{1, 2, 3, 4, 5, 6, 7, 8}
		}), all, 0, sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 6, Channel: 1, Ticks: 32000, Aux: 6400, Pattern: 0x0102030405060708, Length: 8}},
		{"can-too-long", with(func(p *core.SerialParams) { p.Proto, p.Baud, p.Bytes = core.SerCAN, 1000000, make([]int, 9) }), all, 0, sramcapture.PacketTriggerConfig{}},
		{"can-too-fast", with(func(p *core.SerialParams) { p.Proto, p.Baud = core.SerCAN, 20000000 }), all, 0, sramcapture.PacketTriggerConfig{}},
		{
			"flexray", with(func(p *core.SerialParams) { p.Proto, p.Baud, p.Bytes = core.SerFlexRay, 10000000, []int{0x55} }), all, 0,
			sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 10, Channel: 1, Ticks: 3200, Pattern: 0x55, Length: 1},
		},
		{"flexray-absent", with(func(p *core.SerialParams) { p.Proto, p.Baud = core.SerFlexRay, 10000000 }), 1 << 6, 0, sramcapture.PacketTriggerConfig{}},
		{
			"arinc", with(func(p *core.SerialParams) { p.Proto, p.Baud, p.Bytes = core.SerARINC, 100000, []int{0x2abcd} }), all, 0x8d72a15c,
			sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 8, Channel: 1, Ticks: 1250, Aux: 0x8d72a15c, Pattern: 0x7ffff<<42 | 0x2abcd<<10},
		},
		{
			"arinc-any", with(func(p *core.SerialParams) { p.Proto, p.Baud = core.SerARINC, 12500 }), all, 0x8d72a15c,
			sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 8, Channel: 1, Ticks: 10000, Aux: 0x8d72a15c},
		},
		{"arinc-no-levels", with(func(p *core.SerialParams) { p.Proto, p.Baud = core.SerARINC, 100000 }), all, 0, sramcapture.PacketTriggerConfig{}},
		{"arinc-two-words", with(func(p *core.SerialParams) { p.Proto, p.Baud, p.Bytes = core.SerARINC, 100000, []int{1, 2} }), all, 0x8d72a15c, sramcapture.PacketTriggerConfig{}},
		{"auto-threshold", with(func(p *core.SerialParams) { p.Proto, p.Baud, p.HaveThr = core.SerCAN, 500000, false }), all, 0, sramcapture.PacketTriggerConfig{}},
		{"uart", with(func(p *core.SerialParams) { p.Proto, p.Baud = core.SerUART, 115200 }), all, 0, sramcapture.PacketTriggerConfig{}},
	} {
		if got := PlanHardwarePacket(&c.p, c.mask, c.arinc); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
	m := PlanHardwareManchester(&core.SerialParams{Proto: core.SerManchester, Baud: 1000000, HaveThr: true, Threshold: 128, IEEE: true, MSB: true, Bits: 16, Bytes: []int{0xA55A, 0x0FF0}})
	if m != (sramcapture.ManchesterTriggerConfig{Enabled: true, IEEE: true, MSB: true, Bits: 16, BitTicks: 125, Pattern: 0xA55A0FF0, Length: 2}) {
		t.Fatalf("Manchester %+v", m)
	}
	if m := PlanHardwareManchester(&core.SerialParams{Proto: core.SerManchester, Baud: 1000000, HaveThr: true, Bytes: []int{256}}); m.Enabled {
		t.Fatal("a word wider than the default eight bits was accepted")
	}
}

// The FPGA's integer slicer must classify every code exactly as the
// decoder's float thresholds do.
// TRLC-LINKS: REQ-SDS-018
func TestARINCLevelsMatchDecoder(t *testing.T) {
	for _, rails := range [][3]int{{128, 210, 40}, {120, 200, 60}, {131, 180, 90}, {100, 250, 5}} {
		var codes []uint8
		for i := range 4000 {
			v := rails[0]
			switch i % 20 {
			case 0, 1, 2:
				v = rails[1]
			case 10, 11:
				v = rails[2]
			}
			codes = append(codes, uint8(v+(i*7)%3-1))
		}
		p := core.SerialParams{Proto: core.SerARINC, Baud: 100000, HaveThr: true, Threshold: float64(rails[0])}
		aux := ArincLevels(codes, p)
		if aux == 0 {
			t.Fatalf("rails %v: no levels", rails)
		}
		_, hi, lo, exitHi, exitLo, reason := decode.ARINC429Levels(codes, decode.ARINC429Cfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: true})
		if reason != "" {
			t.Fatal(reason)
		}
		h, l, eh, el := int(aux>>8&0xff), int(aux&0xff), int(aux>>24), int(aux>>16&0xff)
		for c := range 256 {
			v := float64(c)
			if (v >= hi) != (c >= h) || (v <= lo) != (c <= l) || (v < exitHi) != (c < eh) || (v > exitLo) != (c > el) {
				t.Fatalf("rails %v code %d: float %.2f %.2f %.2f %.2f integer %d %d %d %d", rails, c, hi, lo, exitHi, exitLo, h, l, eh, el)
			}
		}
	}
}
