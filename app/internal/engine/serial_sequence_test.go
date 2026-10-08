// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// TRLC-LINKS: REQ-SDS-013
func TestPlanSequence(t *testing.T) {
	long := make([]int, 12)
	for i := range long {
		long[i] = i * 17
	}
	c := planSequence(SerialParams{Proto: serCAN, Bytes: long})
	if c.Length != 12 || !c.Qualify || c.EndBadValue || c.Elements[11] != (sramcapture.SequenceElement{Kind: 2, Value: 187, Mask: 0xff}) {
		t.Fatalf("CAN long %+v", c)
	}
	// Errors and wildcards turn qualification off; the error element matches any error.
	c = planSequence(SerialParams{Proto: serCAN, Pattern: []PatternElem{{2, 0xaa, 0xff}, {2, 0, 0}, {4, 0, 0}}})
	if c.Length != 3 || c.Qualify || c.Elements[1] != (sramcapture.SequenceElement{Kind: 2}) || c.Elements[2] != (sramcapture.SequenceElement{Kind: 4}) {
		t.Fatalf("CAN tokens %+v", c)
	}
	c = planSequence(SerialParams{Proto: serARINC, Bytes: []int{0x2abcd, 0x15a3f}})
	if c.Length != 2 || !c.EndBadValue || c.Elements[0] != (sramcapture.SequenceElement{Kind: 2, Value: 0x2abcd << 10, Mask: 0x7ffff << 10}) {
		t.Fatalf("ARINC %+v", c)
	}
	c = planSequence(SerialParams{Proto: serI2C, Bytes: []int{1, 2, 3, 4, 5}})
	if c.Length != 5 || c.Qualify || c.Elements[0] != (sramcapture.SequenceElement{Kind: 2, Value: 1, Mask: 0x1ff}) {
		t.Fatalf("I2C %+v", c)
	}
	c = planSequence(SerialParams{Proto: serManchester, Bits: 16, Pattern: []PatternElem{{2, 0xA50A, 0xFF0F}}})
	if c.Length != 1 || c.Elements[0] != (sramcapture.SequenceElement{Kind: 2, Value: 0xA50A, Mask: 0xFF0F}) || !c.EndBadValue {
		t.Fatalf("Manchester %+v", c)
	}
	for _, bad := range []SerialParams{
		{Proto: serUART, Bytes: make([]int, 33)},
		{Proto: serUART, Bytes: []int{0x100}},
		{Proto: serSENT, Bytes: []int{16}},
		{Proto: serUART, Pattern: []PatternElem{{3, 0, 0}}},
	} {
		if c := planSequence(bad); c.Length != 0 {
			t.Fatalf("planned %+v from %+v", c, bad)
		}
	}
	if sequenceNeeded(SerialParams{Proto: serUART, Bytes: []int{1, 2, 3, 4}}) || !sequenceNeeded(SerialParams{Proto: serUART, Bytes: []int{1, 2, 3, 4, 5}}) ||
		sequenceNeeded(SerialParams{Proto: serCAN, Bytes: make([]int, 8)}) || !sequenceNeeded(SerialParams{Proto: serMIL1553, Bytes: []int{1, 2}}) {
		t.Fatal("native limits")
	}
}

// The software form follows the hardware contract on decoded spans.
// TRLC-LINKS: REQ-SDS-013
func TestMatchSequence(t *testing.T) {
	data := func(i, v int) decode.Span { return decode.Span{I0: i, I1: i, Kind: "data", Val: v} }
	sof := func(i int) decode.Span { return decode.Span{I0: i, I1: i, Kind: "sof"} }
	crcErr := func(i int) decode.Span { return decode.Span{I0: i, I1: i, Kind: "frame-error"} }
	// Two CAN packets carry AA 0F; the first ends in a CRC error.
	spans := []decode.Span{sof(0), data(1, 0x55), data(2, 0xaa), data(3, 0x0f), crcErr(4), sof(10), data(11, 0xaa), data(12, 0x0f), data(13, 0xf0)}
	if ok, at := matchSequence(spans, SerialParams{Proto: serCAN, Bytes: []int{0xaa, 0x0f}}); !ok || at != 11 {
		t.Fatalf("qualified data: %v %d", ok, at)
	}
	if ok, at := matchSequence(spans, SerialParams{Proto: serCAN, Pattern: []PatternElem{{4, 0, 0}}}); !ok || at != 4 {
		t.Fatalf("error: %v %d", ok, at)
	}
	if ok, at := matchSequence(spans, SerialParams{Proto: serCAN, Pattern: []PatternElem{{2, 0xa0, 0xf0}, {2, 0, 0}, {2, 0xf0, 0xff}}}); !ok || at != 11 {
		t.Fatalf("wildcards: %v %d", ok, at)
	}
	// A packet boundary breaks contiguity.
	if ok, _ := matchSequence(spans, SerialParams{Proto: serCAN, Pattern: []PatternElem{{4, 0, 0}, {2, 0xaa, 0xff}}}); ok {
		t.Fatal("matched across a packet boundary")
	}
	// UART is not framed: the data before an error still counts, and a long sequence matches.
	var uart []decode.Span
	for i, c := range "Hello, long sequence" {
		uart = append(uart, data(i, int(c)))
	}
	var want []int
	for _, c := range "Hello, long sequence" {
		want = append(want, int(c))
	}
	if ok, at := matchSequence(uart, SerialParams{Proto: serUART, Bytes: want}); !ok || at != 0 {
		t.Fatalf("UART long: %v %d", ok, at)
	}
	// I2C counts data only in transactions to the requested address.
	addr := func(i, a int) decode.Span { return decode.Span{I0: i, I1: i, Kind: "addr", Val: a} }
	rw := func(i int, t string) decode.Span { return decode.Span{I0: i, I1: i, Kind: "rw", Text: t} }
	start := decode.Span{Kind: "start"}
	i2c := []decode.Span{start, addr(1, 0x50), rw(2, "W"), data(3, 1), data(4, 2), {Kind: "stop"}, start, addr(10, 0x24), rw(11, "W"), data(12, 1), data(13, 2)}
	if ok, at := matchSequence(i2c, SerialParams{Proto: serI2C, Addr: 0x24, RW: 0, Pattern: []PatternElem{{2, 1, 0xff}, {2, 2, 0xff}}}); !ok || at != 12 {
		t.Fatalf("I2C address filter: %v %d", ok, at)
	}
	if ok, _ := matchSequence(i2c, SerialParams{Proto: serI2C, Addr: 0x24, RW: 1, Pattern: []PatternElem{{2, 1, 0xff}}}); ok {
		t.Fatal("I2C direction ignored")
	}
}

// Long or tokenized patterns stay on the general image (ADR-GENERAL-RAW-IMAGE)
// and plan the decoder unfiltered with the sequence trigger.
// TRLC-LINKS: REQ-SDS-013
func TestSequenceOnGeneralImage(t *testing.T) {
	e, b, images := newPacketTestEngine(t)
	e.SetSerialParams(SerialParams{Proto: serUART, Baud: 115200, HaveThr: true, Threshold: 103, ChA: 1, Bytes: []int{1, 2, 3, 4}})
	e.SetSerialMode(SerialTrigger)
	if e.serviceImage() {
		t.Fatalf("a native UART pattern switched images: %v", images.loads)
	}
	e.SetSerialParams(SerialParams{Proto: serUART, Baud: 115200, HaveThr: true, Threshold: 103, ChA: 1, Pattern: []PatternElem{{4, 0, 0}}})
	if e.serviceImage() || b.image != "general" || !e.hardwareSequence || !e.hardwareUART {
		t.Fatalf("general image: %v %s", images.loads, b.image)
	}
	cfg, _, _, _, _ := e.sramConfig()
	if !cfg.UART.Enabled || cfg.UART.Length != 0 || cfg.Sequence.Length != 1 || cfg.Sequence.Elements[0].Kind != sramcapture.SequenceError ||
		hardwareSerialName(cfg) != "uart-sequence" || !cfg.Normal {
		t.Fatalf("UART error plan %+v %+v", cfg.UART, cfg.Sequence)
	}
	// CAN sequences run on the packet image, which has the trigger too.
	e.SetSerialParams(SerialParams{Proto: serCAN, Baud: 500000, HaveThr: true, Threshold: 103, Bytes: make([]int, 12)})
	if !e.serviceImage() || b.image != "packet" {
		t.Fatalf("packet image: %v", images.loads)
	}
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.Packet.Enabled || cfg.Packet.Length != 0 || cfg.Sequence.Length != 12 || !cfg.Sequence.Qualify {
		t.Fatalf("CAN plan %+v %+v", cfg.Packet, cfg.Sequence)
	}
}
