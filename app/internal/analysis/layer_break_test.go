// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// layerSane checks what a layer wrote: printable ASCII (the LCD's font), of
// a length the list can hold, the packets' bytes left as they were, and the
// same notes written again from the same packets.
// TRLC-LINKS: REQ-SDS-241
func layerSane(t *testing.T, name string, v layerVec) {
	t.Helper()
	in := clonePackets(v.Packets)
	ps := ApplyLayer(v.ID, v.Proto, in, v.Args)
	again := ApplyLayer(v.ID, v.Proto, clonePackets(v.Packets), v.Args)
	if len(ps) != len(again) {
		t.Fatalf("%s: %d packets, then %d", name, len(ps), len(again))
	}
	for i, p := range ps {
		if p.Note != again[i].Note || p.NoteErr != again[i].NoteErr {
			t.Fatalf("%s packet %d: %q then %q", name, i, p.Note, again[i].Note)
		}
		if len(p.Note) > 600 {
			t.Fatalf("%s packet %d: a note of %d characters", name, i, len(p.Note))
		}
		for _, c := range []byte(p.Note) {
			if c < 0x20 || c > 0x7e {
				t.Fatalf("%s packet %d: %q holds 0x%02X", name, i, p.Note, c)
			}
		}
	}
	for i := range in {
		if fmt.Sprint(in[i].Bytes) != fmt.Sprint(v.Packets[i].Bytes) {
			t.Fatalf("%s: packet %d's bytes changed", name, i)
		}
	}
}

// TestLayerBreak runs every layer on adversarial records: random packets of
// every length and flag (eight seeds), empty and huge packets, all-zero and
// all-one bytes, packets of other addresses, broken checks. Nothing panics,
// every note stays printable and bounded, and the checks that fail are
// flagged.
// TRLC-LINKS: REQ-SDS-241, REQ-SDS-242, REQ-SDS-243, REQ-SDS-244, REQ-SDS-245, REQ-SDS-246, REQ-SDS-249
func TestLayerBreak(t *testing.T) {
	for seed := int64(10); seed < 18; seed++ {
		for _, v := range layerRandom(seed) {
			layerSane(t, v.Name, v)
		}
	}
	big := make([]byte, 4096)
	for i := range big {
		big[i] = byte(i * 7)
	}
	ones := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	zeros := make([]byte, 25)
	for _, l := range Layers()[1:] {
		var ps []Packet
		for _, b := range [][]byte{nil, {}, big, ones, zeros, {0x55}} {
			p := Packet{Bytes: b, ByteErr: make([]bool, len(b)), Complete: true}
			switch l.Bus {
			case "i2c":
				p.HasAddr, p.Addr = true, 0x50
			case "usbls", "mdio":
				p.HasAddr, p.Addr = true, 0x3
			}
			ps = append(ps, p)
		}
		layerSane(t, l.ID+" edges", layerVec{ID: l.ID, Proto: l.Bus, Packets: ps, Args: LayerArgs{Addr: -1}})
	}
	// broken checks the layers flag
	flagged := []struct {
		id, bus string
		p       Packet
	}{
		{"spiflash", "spi", mkSPI(0x03, 0x00)},
		{"spiflash", "spi", mkSPI(0x20, 0x00, 0x00, 0x10)},
		{"sdcard", "spi", mkSPI(0x51, 0, 0, 0x10, 0, 0x55)},
		{"edid", "i2c", mkI2C(0x50, true, append(append([]byte(nil), edidBlock()[:127]...), 0x00)...)},
		{"ds1307", "i2c", mkI2C(0x68, false, 0, 0x60, 0x60, 0x25, 0x08, 0x32, 0x13, 0xaa)},
		{"atsha204a", "i2c", mkI2C(0x64, true, 4, 0, 0x12, 0x34)},
		{"sbus", "uart", Packet{Bytes: append(append([]byte{0x0f}, make([]byte, 23)...), 0x77), Complete: true}},
		{"arm_itm", "uart", mkSPI(0x03, 1, 2)},
		{"pan1321", "uart", mkSPI([]byte("ERR=-2\r\n")...)},
		{"usb_request", "usbls", Packet{Bytes: []byte{1, 2, 3}, ByteErr: []bool{false, true, true}, Addr: 3, HasAddr: true, Err: FaultCRC}},
	}
	for _, c := range flagged {
		ps := ApplyLayer(c.id, c.bus, []Packet{c.p}, LayerArgs{Addr: -1})
		if len(ps) != 1 || !ps[0].NoteErr {
			t.Errorf("%s: % X not flagged: %q", c.id, c.p.Bytes, ps[0].Note)
		}
	}
	// a layer leaves the transfers of other devices without a note
	for _, id := range []string{"eeprom24xx", "ds1307", "rtc8564", "lm75", "edid", "xfp", "hdcp", "nunchuk", "pca9571", "mxc6225xu", "tca6408a", "mcp23017", "atsha204a"} {
		ps := ApplyLayer(id, "i2c", []Packet{mkI2C(0x7e, false, 1, 2), mkI2C(0x7e, true, 3)}, LayerArgs{Addr: -1})
		for _, p := range ps {
			if p.Note != "" {
				t.Errorf("%s noted 0x7E: %q", id, p.Note)
			}
		}
	}
}

// FuzzLayers feeds a layer random packets: no panic, printable notes.
// TRLC-LINKS: REQ-SDS-241
func FuzzLayers(f *testing.F) {
	for _, v := range layerCurated() {
		for _, p := range v.Packets {
			f.Add(uint8(layerIndexOf(v.ID)), p.Bytes, uint8(p.Addr), p.Read)
		}
	}
	f.Fuzz(func(t *testing.T, layer uint8, b []byte, addr uint8, read bool) {
		ls := Layers()
		l := ls[int(layer)%len(ls)]
		var ps []Packet
		for k := 0; k < 3; k++ { // the bytes split into three packets
			lo, hi := k*len(b)/3, (k+1)*len(b)/3
			p := Packet{Bytes: b[lo:hi], ByteErr: make([]bool, hi-lo), Complete: true, Read: read != (k == 1)}
			if l.Bus == "i2c" || l.Bus == "usbls" || l.Bus == "mdio" {
				p.HasAddr, p.Addr = true, int(addr&0x7f)
			}
			ps = append(ps, p)
		}
		layerSane(t, l.ID, layerVec{ID: l.ID, Proto: l.Bus, Packets: ps, Args: LayerArgs{Addr: int(addr) - 1, Dir: int(addr) % 3}})
		if id, why := DetectLayer(l.Bus, ps); id != "" && strings.TrimSpace(why) == "" {
			t.Fatalf("%s named without evidence", id)
		}
	})
}

// layerIndexOf is a layer's place in the list (0 unknown).
// TRLC-LINKS: REQ-SDS-241
func layerIndexOf(id string) int {
	for i, l := range Layers() {
		if l.ID == id {
			return i
		}
	}
	return 0
}

// TestLayerRandomFlagsAreStable is a seeded sweep of every layer that also
// checks the flagged share: random bytes are mostly not a chip's protocol,
// so a layer that flags none of them reads too much into noise.
// TRLC-LINKS: REQ-SDS-241
func TestLayerRandomFlagsAreStable(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for _, l := range Layers()[1:] {
		if l.ID == "i2cdemux" || l.ID == "i2cfilter" || l.ID == "rgbled" || l.ID == "nesgamepad" || l.ID == "ad5626" ||
			l.ID == "adf435x" || l.ID == "ltc242x" || l.Bus == "i2c" || l.ID == "max72xx" {
			continue // they read any bytes (pixels, buttons, codes, register writes)
		}
		flagged, noted := 0, 0
		for rec := 0; rec < 200; rec++ {
			b := make([]byte, 1+r.Intn(30))
			for i := range b {
				b[i] = byte(r.Intn(256))
			}
			p := Packet{Bytes: b, ByteErr: make([]bool, len(b)), Complete: true, HasAddr: l.Bus == "usbls" || l.Bus == "mdio", Addr: r.Intn(16)}
			ps := ApplyLayer(l.ID, l.Bus, []Packet{p}, LayerArgs{Addr: -1})
			if ps[0].Note != "" {
				noted++
			}
			if ps[0].NoteErr {
				flagged++
			}
		}
		if noted > 0 && flagged == 0 && l.Bus != "usbls" && l.Bus != "mdio" {
			t.Errorf("%s flags none of %d random transactions", l.ID, noted)
		}
	}
}
