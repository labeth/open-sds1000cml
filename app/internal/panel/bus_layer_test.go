// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"encoding/json"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/settings"
)

// twoLineFrame renders a clock (C1) and a data line (C2) from timed levels:
// seg appends both lines' levels for d seconds.
type twoLineFrame struct {
	c1, c2  []uint8
	t       float64
	sampleS float64
}

// TRLC-LINKS: REQ-SDS-241
func (f *twoLineFrame) seg(a, b bool, d float64) {
	lv := func(v bool) uint8 {
		if v {
			return 200
		}
		return 50
	}
	for i := int(f.t / f.sampleS); i < min(int((f.t+d)/f.sampleS), len(f.c1)); i++ {
		f.c1[i], f.c2[i] = lv(a), lv(b)
	}
	f.t += d
}

// TRLC-LINKS: REQ-SDS-241
func (f *twoLineFrame) frame() *engine.Frame {
	n := len(f.c1)
	return &engine.Frame{C1: f.c1, C2: f.c2, Valid: n, WinCols: n, EdgeX: float64(n) / 2, Ptp: 150,
		TdivS: float64(n) * f.sampleS / 10, SampleS: f.sampleS, Trigd: true, FullRate: true}
}

// spiFlashFrame is an SPI flash session (mode 0, MSB first, 1 MHz) on C1
// (SCLK) and C2 (MOSI), its transactions 20 us apart, repeated over 1 ms.
// TRLC-LINKS: REQ-SDS-242, REQ-SDS-247
func spiFlashFrame() *engine.Frame {
	f := &twoLineFrame{c1: make([]uint8, 50000), c2: make([]uint8, 50000), sampleS: 20e-9}
	txns := [][]byte{{0x9f, 0, 0, 0}, {0x06}, {0x02, 0x00, 0x01, 0x00, 0xde, 0xad}, {0x05, 0}, {0x03, 0x00, 0x10, 0x00, 0, 0, 0, 0},
		{0x06}, {0x20, 0x01, 0x00, 0x00}}
	const bt = 1e-6
	f.seg(false, false, 10e-6)
	for f.t < 0.95e-3 {
		for _, tx := range txns {
			for _, v := range tx {
				for k := 7; k >= 0; k-- {
					b := v>>k&1 == 1
					f.seg(false, b, bt/2)
					f.seg(true, b, bt/2)
				}
			}
			f.seg(false, false, 20e-6)
		}
	}
	return f.frame()
}

// i2cEEPROMFrame is a 24xx EEPROM at 0x50 on C1 (SCL) and C2 (SDA), 100
// kHz: a page write, then random reads (the word address written, a
// repeated START, the data read), repeated over 20 ms.
// TRLC-LINKS: REQ-SDS-243, REQ-SDS-247
func i2cEEPROMFrame() *engine.Frame {
	f := &twoLineFrame{c1: make([]uint8, 40000), c2: make([]uint8, 40000), sampleS: 0.5e-6}
	const bt = 10e-6
	sda := true
	seg := func(scl, d bool, dur float64) {
		f.seg(scl, d, dur)
		sda = d
	}
	bit := func(b bool) {
		seg(false, b, bt/2)
		seg(true, b, bt/2)
	}
	byteAck := func(v int, nak bool) {
		for k := 7; k >= 0; k-- {
			bit(v>>k&1 == 1)
		}
		bit(nak)
	}
	start := func() {
		seg(false, sda, bt/4)
		seg(false, true, bt/4)
		seg(true, true, bt/2)
		seg(true, false, bt/2)
	}
	stop := func() {
		seg(false, sda, bt/4)
		seg(false, false, bt/4)
		seg(true, false, bt/2)
		seg(true, true, bt/2)
		seg(true, true, 4*bt)
	}
	seg(true, true, 50e-6)
	for f.t < 19e-3 {
		start()
		byteAck(0x50<<1, false)
		for _, v := range []int{0x10, 1, 2, 3, 4} {
			byteAck(v, false)
		}
		stop()
		for _, a := range []int{0x10, 0x12} {
			start()
			byteAck(0x50<<1, false)
			byteAck(a, false)
			start() // repeated START
			byteAck(0x50<<1|1, false)
			byteAck(1, false)
			byteAck(2, true)
			stop()
		}
		seg(true, true, 300e-6)
	}
	return f.frame()
}

// BUS entry names an SPI flash by its commands and reads its transactions;
// the packet list writes them, the strip captions them.
// TRLC-LINKS: REQ-SDS-241, REQ-SDS-242, REQ-SDS-247
func TestBusEntryNamesAnSPIFlashLayer(t *testing.T) {
	r := newViewRig(t)
	r.show(spiFlashFrame())
	press(t, r.c, "f2")
	r.show(spiFlashFrame())
	s := r.c.snap()
	if s.decProto != framecalc.ProtoSPI || s.decLayer != "spiflash" || !strings.Contains(s.ui.busNote, "layer SPI flash") {
		t.Fatalf("protocol %d layer %q: %q", s.decProto, s.decLayer, s.ui.busNote)
	}
	_, v := r.c.Snapshot()
	b := v.Bus
	if b == nil || b.Layer != "SPI flash" {
		t.Fatalf("BUS pane: %+v", b)
	}
	var notes []string
	for _, ln := range b.Lines {
		if ln.Note != "" {
			notes = append(notes, ln.Note)
		}
	}
	all := strings.Join(notes, " | ")
	for _, w := range []string{"RDID", "WREN", "PP 0x000100, 2 bytes: DE AD", "READ 0x001000, 4 bytes"} {
		if !strings.Contains(all, w) {
			t.Fatalf("packet list notes %q lack %q", all, w)
		}
	}
	captions := 0
	for _, m := range b.Marks {
		if m.Text != "" {
			captions++
		}
	}
	if captions == 0 {
		t.Fatal("no strip captions")
	}
}

// BUS entry names an I2C EEPROM by its random reads.
// TRLC-LINKS: REQ-SDS-241, REQ-SDS-243, REQ-SDS-247
func TestBusEntryNamesAnEEPROMLayer(t *testing.T) {
	r := newViewRig(t)
	r.show(i2cEEPROMFrame())
	press(t, r.c, "f2")
	r.show(i2cEEPROMFrame())
	s := r.c.snap()
	if s.decProto != framecalc.ProtoI2C || s.decLayer != "eeprom24xx" {
		t.Fatalf("protocol %d layer %q: %q", s.decProto, s.decLayer, s.ui.busNote)
	}
	_, v := r.c.Snapshot()
	var notes []string
	for _, ln := range v.Bus.Lines {
		notes = append(notes, ln.Note)
	}
	all := strings.Join(notes, " | ")
	if !strings.Contains(all, "EEPROM 50 page write @0x10, 4 bytes: 01 02 03 04") || !strings.Contains(all, "EEPROM 50 read @0x12, 2 bytes: 01 02") {
		t.Fatalf("notes %q", all)
	}
}

// The Layer row offers the layers of the protocol in use, keeps one across
// a restart (the settings) and drops it when the protocol changes; a layer
// of another bus is refused.
// TRLC-LINKS: REQ-SDS-241
func TestBusLayerRow(t *testing.T) {
	r := newViewRig(t)
	set(t, r.c, "bus.protocol", "SPI")
	m := rowModel(r.c.snap(), rowByID["bus.layer"])
	var names []string
	for _, o := range m.Options {
		names = append(names, o.Text)
	}
	if !strings.Contains(strings.Join(names, ","), "SPI flash (MOSI)") || strings.Contains(strings.Join(names, ","), "EEPROM") {
		t.Fatalf("SPI's layers: %v", names)
	}
	set(t, r.c, "bus.layer", "SPI flash (MOSI)")
	if r.c.snap().decLayer != "spiflash" {
		t.Fatalf("layer %q", r.c.snap().decLayer)
	}
	blob, err := json.Marshal(r.c.SettingsView())
	if err != nil {
		t.Fatal(err)
	}
	var vs settings.ViewState
	if err := json.Unmarshal(blob, &vs); err != nil {
		t.Fatal(err)
	}
	if vs.Decode.Layer != "spiflash" {
		t.Fatalf("saved %+v", vs.Decode)
	}
	r2 := newViewRig(t)
	r2.c.ApplySettingsView(vs)
	if s := r2.c.snap(); s.decLayer != "spiflash" || s.decProto != framecalc.ProtoSPI {
		t.Fatalf("restored %q on %d", s.decLayer, s.decProto)
	}
	set(t, r.c, "bus.protocol", "I2C")
	if r.c.snap().decLayer != "" {
		t.Fatalf("the SPI layer stayed on I2C: %q", r.c.snap().decLayer)
	}
	if res := r.c.SheetSet("bus.layer", nil, "SPI flash (MOSI)"); res.OK {
		t.Fatal("an SPI layer set on I2C")
	}
	// the I2C filter's address and direction
	set(t, r.c, "bus.layer", "Address filter")
	set(t, r.c, "bus.layer_address", "0x50")
	set(t, r.c, "bus.layer_direction", "Read")
	if s := r.c.snap(); s.decLayerAddr != 0x50 || s.decLayerDir != 1 {
		t.Fatalf("filter %#x %d", s.decLayerAddr, s.decLayerDir)
	}
	d, _ := r.c.Snapshot()
	if d.DecLayer != "i2cfilter" || d.DecLayerAddr != 0x50 || d.DecLayerDir != 1 {
		t.Fatalf("display %q %#x %d", d.DecLayer, d.DecLayerAddr, d.DecLayerDir)
	}
}

// The S.BUS layer sets its UART's framing (100 kbaud, even parity, idle
// low), which the decode and the hardware trigger then use; a protocol
// chosen by hand puts the framing back.
// TRLC-LINKS: REQ-SDS-244
func TestSBUSLayerSetsTheFraming(t *testing.T) {
	r := newViewRig(t)
	set(t, r.c, "bus.protocol", "UART")
	set(t, r.c, "bus.layer", "S.BUS")
	s := r.c.snap()
	if s.decBaud != 100000 || s.decParity != "even" || !s.decInv {
		t.Fatalf("framing %d %q %v", s.decBaud, s.decParity, s.decInv)
	}
	if p := decodeParamsOf(s); p.Parity != "even" || !p.Inverted || p.Baud != 100000 {
		t.Fatalf("decode params %+v", p)
	}
	if p := r.c.serialBus(engine.SerialParams{}); p.Parity != "even" || !p.Inverted {
		t.Fatalf("trigger params %+v", p)
	}
	if m := rowModel(r.c.snap(), rowByID["bus.uart_line"]); m.Text != "Inverted" {
		t.Fatalf("UART line row %q", m.Text)
	}
	set(t, r.c, "bus.protocol", "Modbus")
	set(t, r.c, "bus.protocol", "UART")
	if s := r.c.snap(); s.decParity != "" || s.decInv || s.decLayer != "" {
		t.Fatalf("framing kept across protocols: %q %v %q", s.decParity, s.decInv, s.decLayer)
	}
}

// From layer on the Match bytes row arms the selected transaction: a flash
// READ's command and address after the burst's start; an EEPROM read's
// device address and word address.
// TRLC-LINKS: REQ-SDS-248
func TestMatchFromLayer(t *testing.T) {
	r := newViewRig(t)
	r.show(spiFlashFrame())
	press(t, r.c, "f2")
	r.show(spiFlashFrame())
	// select the READ
	for i := 0; i < 20; i++ {
		p, ok := r.c.selectedPacket()
		if ok && len(p.Bytes) > 0 && p.Bytes[0] == 0x03 {
			break
		}
		r.c.busAdjust(1)
	}
	if err := r.c.matchFromLayer(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tokensOf(r.eng.SerialConfig()), " "); got != "SOF 03 00 10 00" {
		t.Fatalf("match %q", got)
	}
	r2 := newViewRig(t)
	r2.show(i2cEEPROMFrame())
	press(t, r2.c, "f2")
	r2.show(i2cEEPROMFrame())
	for i := 0; i < 20; i++ {
		p, ok := r2.c.selectedPacket()
		if ok && !p.Read && len(p.Bytes) == 1 && p.Bytes[0] == 0x12 {
			break
		}
		r2.c.busAdjust(1)
	}
	if err := r2.c.matchFromLayer(); err != nil {
		t.Fatal(err)
	}
	if p := r2.eng.SerialConfig(); p.Addr != 0x50 || strings.Join(tokensOf(p), " ") != "12" {
		t.Fatalf("I2C match %#x %q", p.Addr, tokensOf(p))
	}
	r3 := newViewRig(t)
	set(t, r3.c, "bus.protocol", "SPI")
	if err := r3.c.matchFromLayer(); err == nil {
		t.Fatal("a match from no layer")
	}
}

// sbusLineFrame is an S.BUS receiver's line on C1 (100 kBd 8E2, idling
// low), a frame every 7 ms over 30 ms, C2 idle.
// TRLC-LINKS: REQ-SDS-244, REQ-SDS-247
func sbusLineFrame() *engine.Frame {
	f := &twoLineFrame{c1: make([]uint8, 30000), c2: make([]uint8, 30000), sampleS: 1e-6}
	const bt = 10e-6
	bit := func(b int) { f.seg(b == 0, false, bt) } // inverted: a mark is low
	f.seg(false, false, 1.2e-3)
	for k := 0; f.t < 28e-3; k++ {
		t0 := f.t
		frame := make([]int, 25)
		frame[0] = 0x0f
		for i := 1; i < 23; i++ {
			frame[i] = (i*37 + k*11) & 0xff
		}
		for _, v := range frame {
			bit(0)
			ones := 0
			for j := 0; j < 8; j++ {
				bit(v >> j & 1)
				ones += v >> j & 1
			}
			bit(ones & 1)
			bit(1)
			bit(1)
		}
		f.seg(false, false, 7e-3-(f.t-t0))
	}
	return f.frame()
}

// BUS entry on an S.BUS line names it S.BUS: the UART on S.BUS's framing,
// the S.BUS layer, and the serial trigger armed on its frames' 0F. A match
// the user typed still wins.
// TRLC-LINKS: REQ-SDS-244, REQ-SDS-247, REQ-SDS-223
func TestBusEntryNamesSBUS(t *testing.T) {
	r := newViewRig(t)
	r.show(sbusLineFrame())
	press(t, r.c, "f2")
	r.show(sbusLineFrame())
	s := r.c.snap()
	if s.decProto != framecalc.ProtoUART || s.decLayer != "sbus" || s.decBaud != 100000 || s.decParity != "even" || !s.decInv {
		t.Fatalf("protocol %d layer %q %d Bd %q inverted %v: %q", s.decProto, s.decLayer, s.decBaud, s.decParity, s.decInv, s.ui.busNote)
	}
	if !strings.HasPrefix(s.ui.busNote, "S.BUS on C1") || !strings.Contains(s.ui.busNote, "TRIG: S.BUS frame start (0F)") {
		t.Fatalf("note %q", s.ui.busNote)
	}
	if p := r.eng.SerialConfig(); r.eng.stats.SerialMode == 0 || len(p.Bytes) != 1 || p.Bytes[0] != 0x0f || p.Parity != "even" || !p.Inverted {
		t.Fatalf("trigger %d on %+v", r.eng.stats.SerialMode, p)
	}
	_, v := r.c.Snapshot()
	if v.Bus == nil || v.Bus.Layer != "S.BUS" {
		t.Fatalf("BUS pane %+v", v.Bus)
	}
	if !sbusStart(engine.SerialParams{Bytes: []int{0x0f}}) || sbusStart(engine.SerialParams{Bytes: []int{0x0f, 0x00}}) {
		t.Fatal("sbusStart")
	}
	if err := r.c.setMatchTokens([]string{"0F", "E0"}); err != nil {
		t.Fatal(err)
	}
	if note := r.c.frameTrigger(busMsgs{}); note != "TRIG: your match 0F E0" {
		t.Fatalf("user match: %q", note)
	}
}
