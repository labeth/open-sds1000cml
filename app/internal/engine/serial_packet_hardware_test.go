// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// packetEngineBus adds the packet image's capability registers to the
// stacking test bus (ADR-IMAGE-REGROUP-DECIMATION): revision 10; general has
// UART, I2C and decimation, packet the packet block with MIL and SENT, line
// Manchester and USB; packet and line are raw-only; all have sequence and envelope.
type packetEngineBus struct{ *stackEngineBus }

// TRLC-LINKS: REQ-SDS-013
func (b packetEngineBus) Read(p uint8, s uint16) (uint16, error) {
	if b.image == "packet" || b.image == "line" || b.image == "general" {
		packet, line, general := b.image == "packet", b.image == "line", b.image == "general"
		switch s {
		case 43:
			return 0x454e, nil
		case 47:
			return map[bool]uint16{true: 0x534f}[packet || line], nil
		case 28:
			return 0, nil
		case 29:
			return 8, nil
		case 93:
			return map[bool]uint16{true: 0x4d01}[packet], nil
		case 97:
			return map[bool]uint16{true: 0x5501}[line], nil
		case 77:
			return map[bool]uint16{true: 0x5e01}[packet], nil
		case 118:
			return 0x5351, nil
		case 73:
			return 0x5201, nil
		case 48:
			return map[bool]uint16{true: 0x5502}[general], nil
		case 53:
			return map[bool]uint16{true: 0x4901}[general], nil
		case 13:
			return 10, nil
		case 109:
			return map[bool]uint16{true: 0x5001}[packet], nil
		case 110:
			return map[bool]uint16{true: 0x0540}[packet], nil
		case 102:
			return map[bool]uint16{true: 0x4d01}[line], nil
		case 30:
			return map[bool]uint16{false: 0x4e44}[general], nil
		case 63:
			return 0, nil
		}
	}
	return b.stackEngineBus.Read(p, s)
}

type packetImages struct {
	b          *stackEngineBus
	loads      []string
	failPacket bool
}

// TRLC-LINKS: REQ-SDS-013
func (f *packetImages) LoadPacket() error {
	f.loads = append(f.loads, "packet")
	if f.failPacket {
		f.b.image = "broken"
		return errors.New("load failed")
	}
	f.b.image = "packet"
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *packetImages) LoadLine() error {
	f.loads = append(f.loads, "line")
	f.b.image = "line"
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *packetImages) HasLine() bool { return true }

// TRLC-LINKS: REQ-SDS-035
func (f *packetImages) LoadStream() error { return nil }

// TRLC-LINKS: REQ-SDS-035
func (f *packetImages) HasStream() bool { return false }

// TRLC-LINKS: REQ-SDS-005
func (f *packetImages) LoadGeneral() error {
	f.loads = append(f.loads, "general")
	f.b.image = "general"
	return nil
}

// TRLC-LINKS: REQ-SDS-141
func (f *packetImages) LoadStack() error { return errors.New("no stacking image") }

// TRLC-LINKS: REQ-SDS-141
func (f *packetImages) HasStack() bool { return false }

// TRLC-LINKS: REQ-SDS-013
func (f *packetImages) HasPacket() bool { return true }

// TRLC-LINKS: REQ-SDS-013
func newPacketTestEngine(t *testing.T) (*Engine, *stackEngineBus, *packetImages) {
	b := &stackEngineBus{image: "general"}
	fake := packetEngineBus{b}
	capture, err := sramcapture.New(fake)
	if err != nil {
		t.Fatal(err)
	}
	images := &packetImages{b: b}
	e := &Engine{b: fake, sram: capture, sramJobs: make(chan sramJob, 1), done: make(chan struct{}), images: images,
		logf: t.Logf, clk: Clock{Now: time.Now, Sleep: func(time.Duration) {}}}
	e.probeSerialHardware()
	return e, b, images
}

// The serial trigger's protocol chooses the image between acquisitions, and
// the packet image's plan is raw-only with the shared packet block.
// TRLC-LINKS: REQ-SDS-013
func TestPacketImageFollowsSerialTrigger(t *testing.T) {
	e, b, images := newPacketTestEngine(t)
	if e.serviceImage() || len(images.loads) != 0 {
		t.Fatalf("switched without a serial trigger: %v", images.loads)
	}
	e.SetSerialParams(SerialParams{Proto: serCAN, Baud: 500000, HaveThr: true, Threshold: 120, Bytes: []int{0xAA, 0x0F}})
	e.SetSerialMode(SerialTrigger)
	if !e.serviceImage() || b.image != "packet" || e.protocolImage != imagePacket || e.hardwarePacket != 0x0540 || !e.rawOnly || e.hardwareManchester {
		t.Fatalf("packet image: loads %v image %s protocol image %q mask %x raw %v", images.loads, b.image, e.protocolImage, e.hardwarePacket, e.rawOnly)
	}
	cfg, plan, _, _, _ := e.sramConfig()
	// The packet image is sequence-only: the decoder runs unfiltered and the
	// pattern becomes a qualified sequence.
	if plan.Log != 0 || !cfg.Normal || cfg.TriggerLevel != 120 || !e.sequenceOnly ||
		cfg.Packet != (sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 6, Ticks: 64000, Aux: 64000}) ||
		cfg.Sequence.Length != 2 || !cfg.Sequence.Qualify || cfg.Sequence.Elements[1] != (sramcapture.SequenceElement{Kind: 2, Value: 0x0F, Mask: 0xff}) {
		t.Fatalf("plan %+v config %+v sequence %+v", plan, cfg.Packet, cfg.Sequence)
	}
	if hardwareSerialName(cfg) != "can-sequence" {
		t.Fatalf("backend %q", hardwareSerialName(cfg))
	}
	if e.serviceImage() {
		t.Fatal("reloaded an image that is already loaded")
	}
	// Auto threshold stays a software request, on the general image.
	e.SetSerialParams(SerialParams{Proto: serCAN, Baud: 500000})
	if !e.serviceImage() || b.image != "general" || e.protocolImage != "" || e.hardwarePacket != 0 || e.rawOnly || !e.hardwareUART || !e.hardwareEnvelope {
		t.Fatalf("general image: loads %v image %s", images.loads, b.image)
	}
	// Manchester runs on the line image with USB.
	e.SetSerialParams(SerialParams{Proto: serManchester, Baud: 1000000, HaveThr: true, Threshold: 128, Bits: 16, IEEE: true})
	if !e.serviceImage() || b.image != "line" || !e.hardwareManchester || !e.hardwareUSBLS || e.hardwareMIL1553 || e.hardwarePacket != 0 || !e.rawOnly {
		t.Fatalf("line image: loads %v image %s", images.loads, b.image)
	}
	e.SetSerialParams(SerialParams{Proto: serUSB, Baud: 1500000, HaveThr: true, Threshold: 128})
	if e.serviceImage() || b.image != "line" {
		t.Fatalf("USB left the line image: %v", images.loads)
	}
	e.SetSerialParams(SerialParams{Proto: serManchester, Baud: 1000000, HaveThr: true, Threshold: 128, Bits: 16, IEEE: true})
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.Manchester.Enabled || cfg.Packet.Enabled || hardwareSerialName(cfg) != "manchester" {
		t.Fatalf("Manchester plan %+v", cfg.Manchester)
	}
	// A failed packet load returns to the general image and is not retried.
	images.failPacket = true
	e.SetSerialParams(SerialParams{Proto: serFlexRay, Baud: 10000000, HaveThr: true, Threshold: 128})
	if !e.serviceImage() || b.image != "general" || e.protocolImage != "" || e.failedImage != imagePacket {
		t.Fatalf("failed load: loads %v image %s", images.loads, b.image)
	}
	if e.serviceImage() {
		t.Fatal("retried a failed packet load")
	}
	e.SetSerialMode(SerialOff)
	e.serviceImage()
	if e.failedImage != "" || strings.Join(images.loads, ",") != "packet,general,line,packet,general" {
		t.Fatalf("loads %v", images.loads)
	}
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestPlanHardwarePacket(t *testing.T) {
	const all = 1<<6 | 1<<8 | 1<<10
	base := SerialParams{HaveThr: true, Threshold: 128, ChA: 1}
	with := func(f func(*SerialParams)) SerialParams { p := base; f(&p); return p }
	for _, c := range []struct {
		name  string
		p     SerialParams
		mask  uint16
		arinc uint32
		want  sramcapture.PacketTriggerConfig
	}{
		{"can-fd", with(func(p *SerialParams) {
			p.Proto, p.Baud, p.DataBaud, p.Bytes = serCAN, 1000000, 5000000, []int{1, 2, 3, 4, 5, 6, 7, 8}
		}), all, 0, sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 6, Channel: 1, Ticks: 32000, Aux: 6400, Pattern: 0x0102030405060708, Length: 8}},
		{"can-too-long", with(func(p *SerialParams) { p.Proto, p.Baud, p.Bytes = serCAN, 1000000, make([]int, 9) }), all, 0, sramcapture.PacketTriggerConfig{}},
		{"can-too-fast", with(func(p *SerialParams) { p.Proto, p.Baud = serCAN, 20000000 }), all, 0, sramcapture.PacketTriggerConfig{}},
		{"flexray", with(func(p *SerialParams) { p.Proto, p.Baud, p.Bytes = serFlexRay, 10000000, []int{0x55} }), all, 0,
			sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 10, Channel: 1, Ticks: 3200, Pattern: 0x55, Length: 1}},
		{"flexray-absent", with(func(p *SerialParams) { p.Proto, p.Baud = serFlexRay, 10000000 }), 1 << 6, 0, sramcapture.PacketTriggerConfig{}},
		{"arinc", with(func(p *SerialParams) { p.Proto, p.Baud, p.Bytes = serARINC, 100000, []int{0x2abcd} }), all, 0x8d72a15c,
			sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 8, Channel: 1, Ticks: 1250, Aux: 0x8d72a15c, Pattern: 0x7ffff<<42 | 0x2abcd<<10}},
		{"arinc-any", with(func(p *SerialParams) { p.Proto, p.Baud = serARINC, 12500 }), all, 0x8d72a15c,
			sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 8, Channel: 1, Ticks: 10000, Aux: 0x8d72a15c}},
		{"arinc-no-levels", with(func(p *SerialParams) { p.Proto, p.Baud = serARINC, 100000 }), all, 0, sramcapture.PacketTriggerConfig{}},
		{"arinc-two-words", with(func(p *SerialParams) { p.Proto, p.Baud, p.Bytes = serARINC, 100000, []int{1, 2} }), all, 0x8d72a15c, sramcapture.PacketTriggerConfig{}},
		{"auto-threshold", with(func(p *SerialParams) { p.Proto, p.Baud, p.HaveThr = serCAN, 500000, false }), all, 0, sramcapture.PacketTriggerConfig{}},
		{"uart", with(func(p *SerialParams) { p.Proto, p.Baud = serUART, 115200 }), all, 0, sramcapture.PacketTriggerConfig{}},
	} {
		if got := planHardwarePacket(c.p, c.mask, c.arinc); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
	m := planHardwareManchester(SerialParams{Proto: serManchester, Baud: 1000000, HaveThr: true, Threshold: 128, IEEE: true, MSB: true, Bits: 16, Bytes: []int{0xA55A, 0x0FF0}})
	if m != (sramcapture.ManchesterTriggerConfig{Enabled: true, IEEE: true, MSB: true, Bits: 16, BitTicks: 125, Pattern: 0xA55A0FF0, Length: 2}) {
		t.Fatalf("Manchester %+v", m)
	}
	if m := planHardwareManchester(SerialParams{Proto: serManchester, Baud: 1000000, HaveThr: true, Bytes: []int{256}}); m.Enabled {
		t.Fatal("a word wider than the default eight bits was accepted")
	}
}

// The FPGA's integer slicer must classify every code exactly as the
// decoder's float thresholds do.
// TRLC-LINKS: REQ-SDS-018
func TestARINCLevelsMatchDecoder(t *testing.T) {
	for _, rails := range [][3]int{{128, 210, 40}, {120, 200, 60}, {131, 180, 90}, {100, 250, 5}} {
		var codes []uint8
		for i := 0; i < 4000; i++ {
			v := rails[0]
			switch i % 20 {
			case 0, 1, 2:
				v = rails[1]
			case 10, 11:
				v = rails[2]
			}
			codes = append(codes, uint8(v+(i*7)%3-1))
		}
		p := SerialParams{Proto: serARINC, Baud: 100000, HaveThr: true, Threshold: float64(rails[0])}
		aux := arincLevels(codes, p)
		if aux == 0 {
			t.Fatalf("rails %v: no levels", rails)
		}
		_, hi, lo, exitHi, exitLo, reason := decode.ARINC429Levels(codes, decode.ARINC429Cfg{Bitrate: p.Baud, Threshold: p.Threshold, HaveThr: true})
		if reason != "" {
			t.Fatal(reason)
		}
		h, l, eh, el := int(aux>>8&0xff), int(aux&0xff), int(aux>>24), int(aux>>16&0xff)
		for c := 0; c < 256; c++ {
			v := float64(c)
			if (v >= hi) != (c >= h) || (v <= lo) != (c <= l) || (v < exitHi) != (c < eh) || (v > exitLo) != (c > el) {
				t.Fatalf("rails %v code %d: float %.2f %.2f %.2f %.2f integer %d %d %d %d", rails, c, hi, lo, exitHi, exitLo, h, l, eh, el)
			}
		}
	}
}
