// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// Long or tokenized patterns stay on the general image (ADR-GENERAL-RAW-IMAGE)
// and plan the decoder unfiltered with the sequence trigger.
// TRLC-LINKS: REQ-SDS-013
func TestSequenceOnGeneralImage(t *testing.T) {
	e, b, images := newPacketTestEngine(t)
	e.SetSerialParams(SerialParams{Proto: core.SerUART, Baud: 115200, HaveThr: true, Threshold: 103, ChA: 1, Bytes: []int{1, 2, 3, 4}})
	e.SetSerialMode(SerialTrigger)
	if e.serviceImage() {
		t.Fatalf("a native UART pattern switched images: %v", images.loads)
	}
	e.SetSerialParams(SerialParams{Proto: core.SerUART, Baud: 115200, HaveThr: true, Threshold: 103, ChA: 1, Pattern: []PatternElem{{Kind: 4, Value: 0, Mask: 0}}})
	if e.serviceImage() || b.image != "general" || !e.hardwareSequence || !e.hardwareUART {
		t.Fatalf("general image: %v %s", images.loads, b.image)
	}
	cfg, _, _, _, _ := e.sramConfig()
	if !cfg.UART.Enabled || cfg.UART.Length != 0 || cfg.Sequence.Length != 1 || cfg.Sequence.Elements[0].Kind != sramcapture.SequenceError ||
		acq.HardwareSerialName(cfg) != "uart-sequence" || !cfg.Normal {
		t.Fatalf("UART error plan %+v %+v", cfg.UART, cfg.Sequence)
	}
	// CAN sequences run on the packet image, which has the trigger too.
	e.SetSerialParams(SerialParams{Proto: core.SerCAN, Baud: 500000, HaveThr: true, Threshold: 103, Bytes: make([]int, 12)})
	if !e.serviceImage() || b.image != "packet" {
		t.Fatalf("packet image: %v", images.loads)
	}
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.Packet.Enabled || cfg.Packet.Length != 0 || cfg.Sequence.Length != 12 || !cfg.Sequence.Qualify {
		t.Fatalf("CAN plan %+v %+v", cfg.Packet, cfg.Sequence)
	}
}

// A frame-start match fires as the frame's first unit ends: the trigger
// point goes to that unit's start, also on SPI, whose decoder marks no frame
// start.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func TestFrameUnitStart(t *testing.T) {
	clk, dat := spiWave([]int{0x48, 0x69}, 10)
	p := SerialParams{Proto: core.SerSPI, ChA: 0, ChB: 1, MSB: true, Pattern: []PatternElem{{Kind: core.PatternStart}, {Kind: core.PatternData}}}
	d, _ := core.LookupSerialDecoder(core.SerSPI)
	res := d.Decode(clk, dat, 1e-6, p)
	var first, second [2]int
	n := 0
	for _, sp := range res.Spans {
		if sp.Kind == "data" {
			switch n {
			case 0:
				first = [2]int{sp.I0, sp.I1}
			case 1:
				second = [2]int{sp.I0, sp.I1}
			}
			n++
		}
	}
	if n < 2 {
		t.Fatalf("the SPI test frame decodes %d units", n)
	}
	if got := core.FrameUnitStart(clk, dat, 1e-6, p, first[1]+1, 100); got != float64(100+first[0]) {
		t.Fatalf("trigger at the first unit's end placed at %g, want %d", got, 100+first[0])
	}
	if got := core.FrameUnitStart(clk, dat, 1e-6, p, second[1], 0); got != float64(second[0]) {
		t.Fatalf("placed at %g, want %d", got, second[0])
	}
}

// Until a match has been placed, the record keeps before the FPGA's trigger
// what the match covers: a frame start's four units (a SENT frame's SYNC and
// first nibble: with the trigger at 10 % of the raw record at 200us/div, 0.2
// ms, the SYNC began at the record's start and no frame start was found to
// place it on), or the matched units, which end at the trigger (an ARINC 429
// word of 320 us).
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-223
func TestMatchKeepsItsUnits(t *testing.T) {
	pre := func(p SerialParams) uint32 {
		e := &Engine{band: Band{TdivS: 200e-6}, rawOnly: true}
		e.trigPosFrac.Store(math.Float64bits(.1))
		e.SetSerialParams(p)
		e.SetSerialMode(SerialTrigger)
		c, _, _, _, _ := e.sramConfig()
		return c.PreWords
	}
	sent := SerialParams{Proto: core.SerSENT, TickNs: 3000}
	base := pre(sent)
	sent.Pattern = []PatternElem{{Kind: core.PatternStart}, {Kind: core.PatternData}}
	// four of SENT's longest nibbles, 27 ticks of 3 us, at 2 ns a sample and two samples a word
	if got, want := pre(sent)-base, uint32(4*27*3000/2/2); got != want {
		t.Fatalf("frame start keeps %d words more before the trigger, want %d", got, want)
	}
	arinc := SerialParams{Proto: core.SerARINC, Baud: 100000}
	base = pre(arinc)
	arinc.Pattern = []PatternElem{{Kind: core.PatternData}}
	// one word with its gap, 36 bits at 100 kbit/s
	if got, want := pre(arinc)-base, uint32(36*10000/2/2); got != want {
		t.Fatalf("an ARINC 429 word keeps %d words more before the trigger, want %d", got, want)
	}
}
