// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"math"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// BUS: the list holds the complete packets still while the messages repeat;
// Adjust selects a packet, Position jumps match to match and its push goes
// back to the trigger, the S/DIV push zooms onto the selected packet (a
// running scope stops so it holds still) and again zooms off.
// TRLC-LINKS: REQ-SDS-214
func TestBusPacketTools(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := func() { r.show(uartFrame([]string{"AB\r\n", "Hi!\r\n", "OK\r\n"}, 460800, 100e-6, 100e-9, 20000)) }
	uart()
	press(t, c, "f2")
	uart()
	v := c.View()
	if len(v.Bus.Lines) < 6 || v.Bus.Lines[0].Str != "AB.." || v.Bus.Lines[0].Hex[0] != "41" || !v.Bus.Lines[0].Sel {
		t.Fatalf("packet list %+v", v.Bus.Lines)
	}
	if d, _ := c.Snapshot(); d.DecFormat != 0 {
		t.Fatalf("the strip is not hex by default: %d", d.DecFormat)
	}
	rows := len(c.ana.list.Rows())
	uart() // the same messages again: the list holds still
	if len(c.ana.list.Rows()) != rows {
		t.Fatalf("the list moved: %d -> %d rows", rows, len(c.ana.list.Rows()))
	}
	c.SheetSet("bus.match", nil, "4F 4B") // "OK"
	turn(c, "horizpos", 1)
	if sel := c.View().Bus.SelNum; sel != 3 {
		t.Fatalf("Position: packet %d, want the first OK (3)", sel)
	}
	turn(c, "horizpos", 1)
	if sel := c.View().Bus.SelNum; sel != 6 {
		t.Fatalf("Position again: packet %d, want 6", sel)
	}
	turn(c, "adjust", -1)
	if sel := c.View().Bus.SelNum; sel != 5 {
		t.Fatalf("Adjust back: packet %d", sel)
	}
	press(t, c, "tdivpush") // zoom onto packet 5
	if c.zoom <= 1 || r.eng.Snapshot().Running || !strings.Contains(c.View().Notice, "on packet 5") {
		t.Fatalf("zoom: %d running %v notice %q", c.zoom, r.eng.Snapshot().Running, c.View().Notice)
	}
	press(t, c, "tdivpush")
	if c.zoom != 1 {
		t.Fatalf("zoom off: %d", c.zoom)
	}
	press(t, c, "horizpospush")
	if sel := c.View().Bus.SelNum; sel != 1 {
		t.Fatalf("Position push: packet %d", sel)
	}
}

// BUS again: the eye folds the data line; Adjust moves the threshold and
// Position the sample point, the Position push centres both, the Adjust
// push clears the eye.
// TRLC-LINKS: REQ-SDS-215
func TestBusEyeTools(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	uart := func() { r.show(uartFrame([]string{"Hello, eye!\r\n"}, 460800, 60e-6, 100e-9, 20000)) }
	uart()
	press(t, c, "f2", "f2")
	for i := 0; i < 4; i++ {
		uart()
	}
	e := c.View().Eye
	if e == nil || e.Why != "" || !strings.Contains(e.Figures[0][0][1], "kbit/s") || e.SampleCol != e.Cols/2 {
		t.Fatalf("eye %+v", e)
	}
	th := e.ThreshRow
	turn(c, "adjust", 10)
	turn(c, "horizpos", 8)
	e = c.View().Eye
	if e.ThreshRow >= th || e.SampleCol <= e.Cols/2 || !strings.HasPrefix(e.Cursor, "SAMPLE PT +0.12 UI") {
		t.Fatalf("cursors: row %d (was %d) col %d cursor %q", e.ThreshRow, th, e.SampleCol, e.Cursor)
	}
	press(t, c, "horizpospush")
	if e = c.View().Eye; e.ThreshRow != th || e.SampleCol != e.Cols/2 {
		t.Fatalf("Position push: row %d col %d", e.ThreshRow, e.SampleCol)
	}
	press(t, c, "adjustpush")
	if e = c.View().Eye; e.Why == "" {
		t.Fatal("Adjust push did not clear the eye")
	}
	uart()
	if e = c.View().Eye; e.Why != "" {
		t.Fatalf("the eye did not fold again: %q", e.Why)
	}
}

// prbsSPIFrame is the bench's PRBS source as an SPI bus: PRBS7 data on C1
// changing at each bit boundary and its bit clock on C2, rising at the
// boundary and falling mid-bit, n samples sampleS apart; the bit grid starts
// phase samples into the record.
// TRLC-LINKS: REQ-SDS-215
func prbsSPIFrame(bitS, sampleS float64, n int, phase float64) *engine.Frame {
	c1, c2 := make([]uint8, n), make([]uint8, n)
	spb := bitS / sampleS
	lfsr := 0x7f
	data := uint8(40)
	for i := 0; i < n; i++ {
		b := (float64(i) - phase) / spb
		k := math.Floor(b)
		if i == 0 || math.Floor((float64(i-1)-phase)/spb) != k { // a bit boundary: the next PRBS7 bit
			data = map[bool]uint8{true: 210, false: 40}[lfsr>>6&1 == 1]
			lfsr = (lfsr<<1 | (lfsr>>6^lfsr>>5)&1) & 0x7f
		}
		c1[i] = data
		c2[i] = 40
		if b-k < 0.5 {
			c2[i] = 210
		}
	}
	return &engine.Frame{C1: c1, C2: c2, Valid: n, WinCols: n, EdgeX: float64(n) / 2, Ptp: 170,
		TdivS: float64(n) * sampleS / 10, SampleS: sampleS, Trigd: true}
}

// The eye of an SPI bus keeps folding capture after capture: the clock
// period it folds at is measured on each capture and differs in its last
// digits, which must not start the eye over on every capture (a 2 Mbit/s
// PRBS would show "196 UI folded from 1 capture" at 11 frames a second).
// TRLC-LINKS: REQ-SDS-215
func TestBusEyeAccumulatesAnSPIBus(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	frame := func(k int) *engine.Frame { return prbsSPIFrame(500e-9, 24e-9, 5000, 3+float64(k)*0.37) }
	r.show(frame(0))
	press(t, c, "f2") // BUS finds the bus
	if d, _ := c.Snapshot(); d.DecProto == 0 {
		t.Fatalf("BUS found no bus: %q", c.View().Notice)
	}
	press(t, c, "f2") // the eye
	leases := r.eng.leases.Load()
	for k := 1; k <= 6; k++ {
		r.show(frame(k))
	}
	// the eye asks for full-rate raw frames while it folds, as the web's eye
	// does: the screen's decimated readout smears the edges and leaves the
	// timing figures bounds
	if n := r.eng.leases.Load() - leases; n < 6 {
		t.Fatalf("the eye leased raw frames %d times over 6 captures", n)
	}
	e := c.View().Eye
	if e == nil || e.Why != "" || !strings.HasSuffix(e.Right, "from 6 captures") {
		t.Fatalf("eye after 6 captures: %q %q", e.Right, e.Why)
	}
	// the bus's rate really changes: the eye starts over at the new one
	for k := 1; k <= 5; k++ {
		r.show(prbsSPIFrame(400e-9, 24e-9, 5000, 3+float64(k)*0.37))
	}
	e = c.View().Eye
	if e.Why != "" || e.Figures[0][0][1] != "2.5000 Mbit/s" || strings.HasSuffix(e.Right, "from 1 capture") {
		t.Fatalf("eye after the rate changed: %q %q %q", e.Figures[0][0][1], e.Right, e.Why)
	}
}

// The eye's bit rate reads to five figures with its trailing zeros, as the
// web's eye card shows it (eng(v, "bit/s", 5)).
// TRLC-LINKS: REQ-SDS-215
func TestSI5(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{{2.0001e6, "2.0001 Mbit/s"}, {2e6, "2.0000 Mbit/s"}, {115200, "115.20 kbit/s"}, {9600, "9.6000 kbit/s"},
		{999.9999e3, "1.0000 Mbit/s"}, {300, "300.00 bit/s"}, {math.NaN(), "---"}} {
		if got := si5(c.v, "bit/s"); got != c.want {
			t.Errorf("si5(%g) = %q, want %q", c.v, got, c.want)
		}
	}
}

// The packet list names each packet's address (I2C "24 W") and a fault of the
// packet as a whole (an I2C NAK, a CAN CRC) as the web's list does: a NAKed
// write read like an answered one.
// TRLC-LINKS: REQ-SDS-214
func TestBusListNamesAddressAndFault(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	show := func(nack bool) {
		f := i2cFrame(100e3, 100e-9, 40000, 0x55, 0xAA)
		if nack { // SDA high in the address byte's ACK slot: no device answered
			q := 1 / 100e3 / 4 / 100e-9
			start := 8*q + 3*q // idle, START, SCL low
			for i := int(start + 8*4*q); i < int(start+9*4*q); i++ {
				f.C2[i] = 210
			}
		}
		r.show(f)
	}
	show(false)
	press(t, c, "f2")
	show(false)
	ln := c.View().Bus.Lines
	if len(ln) == 0 || ln[0].Addr != "24 W" || ln[0].Fault != "" {
		t.Fatalf("an answered write: %+v", ln)
	}
	show(true)
	ln = c.View().Bus.Lines
	if len(ln) == 0 || ln[0].Fault != "NAK" {
		t.Fatalf("a NAKed write: %+v", ln)
	}
}
