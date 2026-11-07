// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// packetEngineBus adds the packet image's capability registers to the
// stacking test bus (ADR-IMAGE-REGROUP-DECIMATION): revision 10; general has
// UART, I2C and decimation, packet the packet block, frame MIL, SENT and USB,
// line Manchester; packet, frame and line decimate by box averages, fast
// by picking (ADR-TRIGGER-ANY-TIMEBASE); all have sequence and envelope.
type packetEngineBus struct{ *stackEngineBus }

// TRLC-LINKS: REQ-SDS-013
func (b packetEngineBus) Read(p uint8, s uint16) (uint16, error) {
	if b.image == "packet" || b.image == "line" || b.image == "frame" || b.image == "general" || b.image == "qualify" || b.image == "fast" {
		packet, line, general, qualify, fast := b.image == "packet", b.image == "line", b.image == "general", b.image == "qualify", b.image == "fast"
		frame := b.image == "frame"
		switch s {
		case 64:
			return map[bool]uint16{true: 0x5301}[general] | map[bool]uint16{true: 0x5303}[fast], nil
		case 43:
			return 0x454e, nil
		case 47:
			return map[bool]uint16{true: 0x534f}[packet || line || frame], nil
		case 28:
			return 0, nil
		case 29:
			return 8, nil
		case 93:
			return map[bool]uint16{true: 0x4d01}[frame], nil
		case 97:
			return map[bool]uint16{true: 0x5501}[frame], nil
		case 77:
			return map[bool]uint16{true: 0x5e01}[frame], nil
		case 73:
			return 0x5201, nil
		case 48:
			return map[bool]uint16{true: 0x5502}[general] | map[bool]uint16{true: 0x5503}[fast], nil
		case 53:
			return map[bool]uint16{true: 0x4901}[general] | map[bool]uint16{true: 0x4902}[fast], nil
		case 13:
			return 10, nil
		case 109:
			return map[bool]uint16{true: 0x5001}[packet], nil
		case 110:
			return map[bool]uint16{true: 0x0540}[packet], nil
		case 102:
			return map[bool]uint16{true: 0x4d01}[line], nil
		case 30:
			return map[bool]uint16{true: 0x4258}[packet || line || frame] | map[bool]uint16{true: 0x5053}[fast] | map[bool]uint16{true: 0x5041}[qualify], nil
		case 63:
			return 0, nil
		case 74:
			return map[bool]uint16{true: 0x5154}[qualify], nil
		case 75:
			return map[bool]uint16{true: 0x000f}[qualify], nil
		case 76:
			return map[bool]uint16{true: 0x5359}[qualify], nil
		case 118:
			return map[bool]uint16{false: 0x5351}[qualify], nil
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

// TRLC-LINKS: REQ-SDS-013
func (f *packetImages) LoadFrame() error {
	f.loads = append(f.loads, "frame")
	f.b.image = "frame"
	return nil
}

// TRLC-LINKS: REQ-SDS-013
func (f *packetImages) HasFrame() bool { return true }

// TRLC-LINKS: REQ-SDS-035
func (f *packetImages) LoadStream() error { return nil }

// TRLC-LINKS: REQ-SDS-035
func (f *packetImages) HasStream() bool { return false }

// TRLC-LINKS: REQ-SDS-011
func (f *packetImages) LoadQualify() error {
	f.loads = append(f.loads, "qualify")
	f.b.image = "qualify"
	return nil
}

// TRLC-LINKS: REQ-SDS-011
func (f *packetImages) HasQualify() bool { return true }

// TRLC-LINKS: REQ-SDS-229
func (f *packetImages) LoadFast() error {
	f.loads = append(f.loads, "fast")
	f.b.image = "fast"
	return nil
}

// TRLC-LINKS: REQ-SDS-229
func (f *packetImages) HasFast() bool { return true }

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
	t.Helper()
	b := &stackEngineBus{image: "general"}
	fake := packetEngineBus{b}
	capture, err := sramcapture.New(fake)
	if err != nil {
		t.Fatal(err)
	}
	images := &packetImages{b: b}
	e := &Engine{
		b: fake, sram: capture, sramJobs: make(chan core.OwnerJob, 1), done: make(chan struct{}), images: images,
		logf: t.Logf, clk: Clock{Now: time.Now, Sleep: func(time.Duration) {}},
	}
	e.probeSerialHardware()
	return e, b, images
}

// The serial trigger's protocol chooses the image between acquisitions, and
// the packet image's plan decimates by box averages with the shared packet
// block; MIL-1553 and SENT run on the frame image.
// TRLC-LINKS: REQ-SDS-013
func TestPacketImageFollowsSerialTrigger(t *testing.T) {
	e, b, images := newPacketTestEngine(t)
	if e.serviceImage() || len(images.loads) != 0 {
		t.Fatalf("switched without a serial trigger: %v", images.loads)
	}
	e.SetSerialParams(SerialParams{Proto: core.SerCAN, Baud: 500000, HaveThr: true, Threshold: 120, Bytes: []int{0xAA, 0x0F}})
	e.SetSerialMode(SerialTrigger)
	if !e.serviceImage() || b.image != "packet" || e.protocolImage != acq.ImagePacket || e.caps().Packet != 0x0540 || e.caps().RawOnly || !e.caps().BoxDecim || e.caps().Manchester || e.caps().MIL1553 {
		t.Fatalf("packet image: loads %v image %s protocol image %q mask %x raw %v box %v", images.loads, b.image, e.protocolImage, e.caps().Packet, e.caps().RawOnly, e.caps().BoxDecim)
	}
	cfg, plan, _, _, _ := e.sramConfig()
	// The packet image is sequence-only: the decoder runs unfiltered and the
	// pattern becomes a qualified sequence.
	if !cfg.Normal || cfg.TriggerLevel != 120 || !e.caps().SequenceOnly ||
		cfg.Packet != (sramcapture.PacketTriggerConfig{Enabled: true, Protocol: 6, Ticks: 64000, Aux: 64000}) ||
		cfg.Sequence.Length != 2 || !cfg.Sequence.Qualify || cfg.Sequence.Elements[1] != (sramcapture.SequenceElement{Kind: 2, Value: 0x0F, Mask: 0xff}) {
		t.Fatalf("plan %+v config %+v sequence %+v", plan, cfg.Packet, cfg.Sequence)
	}
	if acq.HardwareSerialName(&cfg) != "can-sequence" {
		t.Fatalf("backend %q", acq.HardwareSerialName(&cfg))
	}
	if e.serviceImage() {
		t.Fatal("reloaded an image that is already loaded")
	}
	// Without a threshold it waits on the general image until a capture has
	// shown the line's levels (TestEveryBusPlansFromTheScope).
	e.SetSerialParams(SerialParams{Proto: core.SerCAN, Baud: 500000})
	if !e.serviceImage() || b.image != "general" || e.protocolImage != "" || e.caps().Packet != 0 || e.caps().RawOnly || e.caps().BoxDecim || !e.caps().UART || !e.caps().Envelope {
		t.Fatalf("general image: loads %v image %s", images.loads, b.image)
	}
	// Manchester runs on the line image.
	e.SetSerialParams(SerialParams{Proto: core.SerManchester, Baud: 1000000, HaveThr: true, Threshold: 128, Bits: 16, IEEE: true})
	if !e.serviceImage() || b.image != "line" || !e.caps().Manchester || e.caps().USBLS || e.caps().MIL1553 || e.caps().Packet != 0 || !e.caps().BoxDecim {
		t.Fatalf("line image: loads %v image %s", images.loads, b.image)
	}
	e.SetSerialParams(SerialParams{Proto: core.SerManchester, Baud: 1000000, HaveThr: true, Threshold: 128, Bits: 16, IEEE: true})
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.Manchester.Enabled || cfg.Packet.Enabled || acq.HardwareSerialName(&cfg) != "manchester" {
		t.Fatalf("Manchester plan %+v", cfg.Manchester)
	}
	// MIL-1553, SENT and USB run on the frame image.
	e.SetSerialParams(SerialParams{Proto: core.SerMIL1553, Baud: 1000000, HaveThr: true, Threshold: 128})
	if !e.serviceImage() || b.image != "frame" || e.protocolImage != acq.ImageFrame || !e.caps().MIL1553 || !e.caps().SENT || !e.caps().USBLS || e.caps().Packet != 0 || !e.caps().BoxDecim {
		t.Fatalf("frame image: loads %v image %s", images.loads, b.image)
	}
	e.SetSerialParams(SerialParams{Proto: core.SerUSB, Baud: 1500000, HaveThr: true, Threshold: 128})
	if e.serviceImage() || b.image != "frame" {
		t.Fatalf("USB left the frame image: %v", images.loads)
	}
	// A failed packet load returns to the general image and is not retried.
	images.failPacket = true
	e.SetSerialParams(SerialParams{Proto: core.SerFlexRay, Baud: 10000000, HaveThr: true, Threshold: 128})
	if !e.serviceImage() || b.image != "general" || e.protocolImage != "" || e.failedImage != acq.ImagePacket {
		t.Fatalf("failed load: loads %v image %s", images.loads, b.image)
	}
	if e.serviceImage() {
		t.Fatal("retried a failed packet load")
	}
	e.SetSerialMode(SerialOff)
	e.serviceImage()
	if e.failedImage != "" || strings.Join(images.loads, ",") != "packet,general,line,frame,packet,general" {
		t.Fatalf("loads %v", images.loads)
	}
}

// The ARINC slicer levels come from a sampled record: a live envelope's
// (min, max) pairs give wrong ones, kept until the request changes, and the
// matcher never fires. On an envelope the engine asks for a sampled record
// instead.
// TRLC-LINKS: REQ-SDS-018
func TestARINCLevelsWaitForASampledRecord(t *testing.T) {
	e := levelsEngine(SerialParams{Proto: core.SerARINC, ChA: 0, Baud: 100000, Bytes: []int{0xABCD}})
	e.clk = Clock{Now: time.Now, Sleep: func(time.Duration) {}}
	var codes []uint8
	for i := range 4000 {
		v := 128
		switch i % 20 {
		case 0, 1, 2:
			v = 210
		case 10, 11:
			v = 40
		}
		codes = append(codes, uint8(v+(i*7)%3-1))
	}
	e.learnSerialLevels(&sample.Frame{SampleS: 1e-6, Traces: []sample.Trace{{Codes8: codes, Valid: len(codes)}, {Ch: 1, Codes8: codes, Valid: len(codes)}}}, [2]core.ChScale{{Vdiv: 1, Off: 0}, {Vdiv: 1, Off: 0}})
	// a decimated envelope too (200us/div's): a decimated sampled record is
	// asked for, which reads in milliseconds
	env := &sample.Frame{SampleS: 1e-6, AcqDecim: int(64), Content: sample.PeakPairs, Flags: sample.Ordered, Traces: []sample.Trace{{Codes8: codes, Valid: len(codes)}, {Ch: 1, Codes8: codes, Valid: len(codes)}}}
	e.noteARINCLevels(env)
	if e.arincAux.Load() != 0 || !e.leased(&e.sampleLease) {
		t.Fatalf("levels from an envelope %#x, a sampled record asked for %v", e.arincAux.Load(), e.leased(&e.sampleLease))
	}
	// a decimated record too coarse for the bits asks for the full rate
	e.noteARINCLevels(&sample.Frame{SampleS: 2e-6, AcqDecim: int(1000), Traces: []sample.Trace{{Codes8: codes, Valid: len(codes)}, {Ch: 1, Codes8: codes, Valid: len(codes)}}})
	if e.arincAux.Load() != 0 || !e.leased(&e.decodeLease) {
		t.Fatalf("levels from a coarse record %#x, the full rate asked for %v", e.arincAux.Load(), e.leased(&e.decodeLease))
	}
	// a decimated record resolving the bits (1 us samples, 10 us bits) gives them
	e.noteARINCLevels(&sample.Frame{SampleS: 1e-6, AcqDecim: int(500), Traces: []sample.Trace{{Codes8: codes, Valid: len(codes)}, {Ch: 1, Codes8: codes, Valid: len(codes)}}})
	if e.arincAux.Load() == 0 {
		t.Fatal("no levels from a sampled record")
	}
}

// SPI past the general image's level receiver loads the fast image, whose
// receiver on the samples takes it with its gap and the channels' phase; a
// slower clock stays on the general image.
// TRLC-LINKS: REQ-SDS-229
func TestFastImageTakesFastSPI(t *testing.T) {
	e, b, images := newPacketTestEngine(t)
	e.SetSerialParams(SerialParams{Proto: core.SerSPI, SPIClockHz: 1000000, ChA: 1, ChB: 0, MSB: true, HaveThr: true, Threshold: 128, Bytes: []int{0x4f}})
	e.SetSerialMode(SerialTrigger)
	if e.serviceImage() || b.image != "general" || !e.caps().SPI || e.caps().SampleSPI {
		t.Fatalf("1 MHz left the general image: %v", images.loads)
	}
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.SPI.Enabled || cfg.SPI.GapTicks != 188 || cfg.SPI.Delay != 0 {
		t.Fatalf("1 MHz plan %+v", cfg.SPI)
	}
	e.SetSerialParams(SerialParams{Proto: core.SerSPI, SPIClockHz: 104000000, ChA: 1, ChB: 0, MSB: true, HaveThr: true, Threshold: 128, Bytes: []int{0x4f}})
	if !e.serviceImage() || b.image != "fast" || e.protocolImage != acq.ImageFast || !e.caps().SampleSPI || !e.caps().BoxDecim || !e.caps().PickDecim {
		t.Fatalf("104 MHz: loads %v image %s", images.loads, b.image)
	}
	cfg, _, _, _, _ := e.sramConfig()
	if !cfg.Normal || cfg.TriggerChannel != 1 || cfg.TriggerLevel != 128 ||
		cfg.SPI != (sramcapture.SPITriggerConfig{Enabled: true, ClockChannel: 1, MSB: true, GapTicks: 4, Pattern: 0x4f, Length: 1, Delay: 23}) {
		t.Fatalf("104 MHz plan %+v", cfg.SPI)
	}
	if acq.HardwareSerialName(&cfg) != "spi" {
		t.Fatalf("backend %q", acq.HardwareSerialName(&cfg))
	}
	e.SetSerialParams(SerialParams{Proto: core.SerSPI, SPIClockHz: 1000000, ChA: 1, ChB: 0, MSB: true, HaveThr: true, Threshold: 128})
	if !e.serviceImage() || b.image != "general" || strings.Join(images.loads, ",") != "fast,general" {
		t.Fatalf("back to 1 MHz: loads %v", images.loads)
	}
}

// The qualify image picks samples on asking, and BUS's sampled records ask
// for them while a serial trigger reads the raw samples (a WS281x frame
// start at 10ms/div): not without the lease, nor with the serial trigger off.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-214
func TestBusRecordsPickSamplesOnTheQualifyImage(t *testing.T) {
	e, b, _ := newPacketTestEngine(t)
	setCaps(e, func(c *acq.Caps) { c.Envelope = true })
	e.SetSerialParams(SerialParams{Proto: core.SerWS281x, Baud: 800000, HaveThr: true, Threshold: 128, Pattern: []PatternElem{{Kind: core.PatternStart}}})
	e.SetSerialMode(SerialTrigger)
	if !e.serviceImage() || b.image != "qualify" || !e.caps().PickAlt || e.caps().BoxDecim || e.caps().PickDecim {
		t.Fatalf("qualify image %s: pick on asking %v box %v pick %v", b.image, e.caps().PickAlt, e.caps().BoxDecim, e.caps().PickDecim)
	}
	band, _ := PlanTdiv(10e-3)
	e.band = band
	if cfg, _, _, _, _ := e.sramConfig(); cfg.Pick {
		t.Fatal("picked without BUS's lease")
	}
	e.LeaseBusSamples(104e-9, 5*time.Second)
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.Pick || cfg.DecimationLog2 == 0 || acq.HardwareSerialName(&cfg) == "" {
		t.Fatalf("BUS's lease: pick %v decimation %d trigger %q", cfg.Pick, cfg.DecimationLog2, acq.HardwareSerialName(&cfg))
	}
	e.SetSerialMode(SerialOff)
	if cfg, _, _, _, _ := e.sramConfig(); cfg.Pick {
		t.Fatal("picked with no serial trigger: a decimated edge reads the filter's words")
	}
}
