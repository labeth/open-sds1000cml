// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"fmt"
	"strings"
)

// Layers: the application protocol a decoded bus carries, chosen per bus on
// the BUS sheet (REQ-SDS-241). A layer reads the bus's packets (Packets) as
// the transactions of a chip or a protocol stacked on the bus and writes each
// packet's Note: "READ 0x001000, 64 bytes" on an SPI flash, "EEPROM 50 write
// @0x0010: 12 34" on an I2C EEPROM, "ITM ch0 'hello'" on a UART carrying a
// Cortex-M's trace. The packet list, the decode strip and the web table show
// the notes; the bus's own bytes stay as they are.
//
// The scope has two channels, so an SPI bus is its clock and one data line
// without a chip select: a transaction is a packet, a burst of bytes the
// clock's pause of 1.5 byte times ends (the chip select's release on the
// wire). A master that pauses inside a transaction splits it, and one that
// clocks transactions back to back joins them; each SPI layer reads its
// chip's commands off the data line it names (Layer.Line), MOSI for the
// commands a host sends, MISO for the converters and pads that only answer.
// The web page has the same layers (layers.js) and gives the same notes.

// Layer is one application layer.
// TRLC-LINKS: REQ-SDS-241
type Layer struct {
	ID   string // the settings' and the web page's name: "spiflash"
	Name string // the BUS sheet's: "SPI flash"
	Bus  string // the decode it reads (decode.Result.Proto): spi i2c uart onewire mdio usbls
	Line string // SPI: the data line it reads, "MOSI" or "MISO"
}

// layerFns is a layer's work: notes reads the packets of a record in order
// (a layer may carry state from packet to packet: a register bank, an
// EEPROM's address pointer, a trace stream); detect returns the evidence
// that the packets are this layer's ("" when they are not); match returns
// the match tokens that select a packet's transaction on the bus's own
// hardware trigger, and the I2C address it needs (-1 any).
type layerFns struct {
	notes  func(ps []Packet)
	detect func(ps []Packet) string
	match  func(p Packet) ([]string, int)
}

// Layers lists the layers in the BUS sheet's order; the first is none.
// TRLC-LINKS: REQ-SDS-241
func Layers() []Layer { return append([]Layer(nil), layerList...) }

// LayerByID finds a layer by its ID.
// TRLC-LINKS: REQ-SDS-241
func LayerByID(id string) (Layer, bool) {
	for _, l := range layerList {
		if l.ID == id {
			return l, true
		}
	}
	return Layer{}, false
}

// LayersOn lists the layers a bus decode (decode.Result.Proto) can carry,
// none first.
// TRLC-LINKS: REQ-SDS-241
func LayersOn(proto string) []Layer {
	out := []Layer{layerList[0]}
	for _, l := range layerList[1:] {
		if l.Bus == busOf(proto) {
			out = append(out, l)
		}
	}
	return out
}

// busOf names the bus a decoder's packets come from as the layers name it.
// TRLC-LINKS: REQ-SDS-241
func busOf(proto string) string {
	if proto == "usb" {
		return "usbls"
	}
	return proto
}

// LayerArgs are a layer's settings: the I2C filter's address (-1 any) and
// direction (0 both, 1 read, 2 write).
// TRLC-LINKS: REQ-SDS-241, REQ-SDS-243
type LayerArgs struct {
	Addr int
	Dir  int
}

// ApplyLayer writes each packet's Note with layer id's reading of it and
// returns the packets the list shows: all of them, but for the I2C filter,
// the transfers it keeps. A layer of another bus, or none, leaves every note
// empty.
// TRLC-LINKS: REQ-SDS-241
func ApplyLayer(id, proto string, ps []Packet, args LayerArgs) []Packet {
	for i := range ps {
		ps[i].Note, ps[i].NoteErr = "", false
	}
	l, ok := LayerByID(id)
	if !ok || id == "" || l.Bus != busOf(proto) {
		return ps
	}
	if id == "i2cfilter" {
		return FilterI2C(ps, args.Addr, args.Dir)
	}
	if f, ok := layerImpls[id]; ok && f.notes != nil {
		f.notes(ps)
	}
	return ps
}

// DetectLayer names the layer whose evidence the packets of a bus hold, and
// the evidence; "" when none proves itself. The layers are tried in the
// list's order, so a layer with a stronger proof is listed before one its
// packets also satisfy (EDID before an EEPROM at 0x50).
// TRLC-LINKS: REQ-SDS-247
func DetectLayer(proto string, ps []Packet) (id, why string) {
	for _, l := range layerList[1:] {
		if l.Bus != busOf(proto) {
			continue
		}
		f := layerImpls[l.ID]
		if f.detect == nil {
			continue
		}
		if w := f.detect(ps); w != "" {
			return l.ID, w
		}
	}
	return "", ""
}

// LayerMatch is the match that selects packet p's transaction on the bus's
// hardware trigger (the FPGA decides it): the bus's match tokens and the I2C
// address it needs (-1 any). ok is false when the layer offers none.
// TRLC-LINKS: REQ-SDS-248
func LayerMatch(id string, p Packet) (toks []string, addr int, ok bool) {
	f, found := layerImpls[id]
	if !found || f.match == nil {
		return nil, -1, false
	}
	toks, addr = f.match(p)
	return toks, addr, len(toks) > 0 || addr >= 0
}

// ---- formatting shared by the layers (layers.js formats alike) ----

// hx writes v in upper-case hex, at least n digits.
// TRLC-LINKS: REQ-SDS-241
func hx[T ~int | ~int64](v T, n int) string {
	if v < 0 {
		return fmt.Sprintf("%0*X", n, v)
	}
	if v < 256 && n == 2 { // a byte: from the table
		return hexByte[v]
	}
	// %0*X written directly: no fmt call a value.
	var b [24]byte
	i := len(b)
	for u := uint64(v); u != 0 || i == len(b); u >>= 4 {
		i--
		b[i] = "0123456789ABCDEF"[u&15]
	}
	for len(b)-i < n && i > 0 {
		i--
		b[i] = '0'
	}
	if len(b)-i < n {
		return fmt.Sprintf("%0*X", n, v)
	}
	return string(b[i:])
}

// hexByte is every byte as two upper-case hex digits.
var hexByte = func() (t [256]string) {
	for i := range t {
		t[i] = fmt.Sprintf("%02X", i)
	}
	return t
}()

// hexList writes bytes as "12 34 56", at most max of them and ".." after.
// TRLC-LINKS: REQ-SDS-241
func hexList(b []byte, max int) string {
	var sb strings.Builder
	for i, v := range b {
		if i == max {
			sb.WriteString(" ..")
			break
		}
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(hx(int(v), 2))
	}
	return sb.String()
}

// quoted writes bytes as text in single quotes, '.' for a byte that does
// not print, at most max of them and ".." after.
// TRLC-LINKS: REQ-SDS-241
func quoted(b []byte, max int) string {
	var sb strings.Builder
	sb.WriteByte('\'')
	for i, v := range b {
		if i == max {
			sb.WriteString("'..")
			return sb.String()
		}
		if v >= 0x20 && v < 0x7f {
			sb.WriteByte(v)
		} else {
			sb.WriteByte('.')
		}
	}
	sb.WriteByte('\'')
	return sb.String()
}

// printable writes text from a device with '.' for a byte that does not
// print, so a note stays ASCII (the LCD's font) whatever the bus carried.
// TRLC-LINKS: REQ-SDS-241
func printable(s string) string {
	b := []byte(s)
	for i, v := range b {
		if v < 0x20 || v >= 0x7f {
			b[i] = '.'
		}
	}
	return string(b)
}

// fixed writes num/den with places decimals, rounded half away from zero, in
// integer arithmetic (so the web's twin writes the same digits).
// TRLC-LINKS: REQ-SDS-241
func fixed(num, den int64, places int) string {
	if den < 0 {
		num, den = -num, -den
	}
	if den == 0 {
		return "?"
	}
	neg := num < 0
	if neg {
		num = -num
	}
	scale := int64(1)
	for i := 0; i < places; i++ {
		scale *= 10
	}
	q := (num*scale*2 + den) / (den * 2)
	s := fmt.Sprint(q / scale)
	if places > 0 {
		s += "." + fmt.Sprintf("%0*d", places, q%scale)
	}
	if neg && q != 0 {
		s = "-" + s
	}
	return s
}

// plural is "1 byte" / "2 bytes".
// TRLC-LINKS: REQ-SDS-241
func plural(n int, w string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, w)
	}
	return fmt.Sprintf("%d %ss", n, w)
}

// be is bytes most significant first as a number.
// TRLC-LINKS: REQ-SDS-241
func be(b []byte) int {
	v := 0
	for _, x := range b {
		v = v<<8 | int(x)
	}
	return v
}

// be64 is up to eight bytes most significant first as a number: a 32-bit
// word past int's range on the scope's 32-bit processor.
// TRLC-LINKS: REQ-SDS-241
func be64(b []byte) int64 {
	var v int64
	for _, x := range b {
		v = v<<8 | int64(x)
	}
	return v
}

// le64 is up to eight bytes least significant first as a number.
// TRLC-LINKS: REQ-SDS-241
func le64(b []byte) int64 {
	var v int64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | int64(b[i])
	}
	return v
}

// le is bytes least significant first as a number.
// TRLC-LINKS: REQ-SDS-241
func le(b []byte) int {
	v := 0
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | int(b[i])
	}
	return v
}

// regName names register r from a table, or writes it as hex.
// TRLC-LINKS: REQ-SDS-241
func regName(t map[int]string, r int) string {
	if n, ok := t[r]; ok && n != "" && n != "-" {
		return n
	}
	return "0x" + hx(r, 2)
}

// anyBad reports a packet with a byte the bus decoder flagged.
// TRLC-LINKS: REQ-SDS-241
func anyBad(p Packet) bool {
	for _, b := range p.ByteErr {
		if b {
			return true
		}
	}
	return p.Err != ""
}

// hexToks are bytes as match tokens.
// TRLC-LINKS: REQ-SDS-248
func hexToks(b []byte) []string {
	out := make([]string, len(b))
	for i, v := range b {
		out[i] = hx(int(v), 2)
	}
	return out
}

// sofToks are a frame-start token and then bytes: the transaction's first
// bytes, where a bus with frame starts marks them (an SPI burst after its
// gap).
// TRLC-LINKS: REQ-SDS-248
func sofToks(b []byte) []string { return append([]string{"SOF"}, hexToks(b)...) }
