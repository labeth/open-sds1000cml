// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"math/rand"
	"testing"
)

// TestDetectLayerNamesItsOwn: each layer with evidence is named on its own
// records, and the stronger proof wins where packets satisfy two (an EDID
// at 0x50 is no EEPROM, an XFP's identifier no EEPROM either).
// TRLC-LINKS: REQ-SDS-247
func TestDetectLayerNamesItsOwn(t *testing.T) {
	want := map[string]string{"flash session": "spiflash", "sd card init": "sdcard", "avr isp": "avrisp",
		"edid in two reads": "edid", "eeprom 2-byte addresses": "eeprom24xx", "ds1307": "ds1307", "rtc8564": "rtc8564",
		"nunchuk": "nunchuk", "hdcp": "hdcp", "xfp": "xfp", "itm": "arm_itm", "tpiu": "arm_tpiu",
		"pan1321": "pan1321", "1-wire net": "ds28ea00", "ds2408": "onewire_net", "cfp": "cfp", "usb enumeration": "usb_request"}
	got := map[string]bool{}
	for _, v := range layerCurated() {
		w, ok := want[v.Name]
		if !ok {
			continue
		}
		got[v.Name] = true
		// the detection reads clean records: drop the curated broken ones
		var ps []Packet
		for _, p := range ApplyLayer(v.ID, v.Proto, clonePackets(v.Packets), v.Args) {
			if !p.NoteErr {
				ps = append(ps, p)
			}
		}
		id, why := DetectLayer(v.Proto, ps)
		if id != w {
			t.Errorf("%s: named %q (%s), want %q", v.Name, id, why, w)
		}
		if id != "" && why == "" {
			t.Errorf("%s: no evidence written", v.Name)
		}
	}
	for n := range want {
		if !got[n] {
			t.Errorf("no curated record %q", n)
		}
	}
	// the families: a DS2408's or a DS243x's ROM code names its layer
	for fam, w := range map[byte]string{0x29: "ds2408", 0x23: "ds243x", 0x2d: "ds243x", 0x42: "ds28ea00", 0x28: "ds28ea00", 0x01: "onewire_net"} {
		rom := []byte{fam, 1, 2, 3, 4, 5, 6, 0}
		rom[7] = byte(crc8Maxim(rom[:7]))
		ps := []Packet{{Bytes: append([]byte{0x55}, rom...), ByteErr: make([]bool, 9), Complete: true}}
		if id, _ := DetectLayer("onewire", ps); id != w {
			t.Errorf("family %#x: %q, want %q", fam, id, w)
		}
	}
	// an S.BUS line: frames of 25 bytes from 0x0F
	var ch [16]int
	sb := []Packet{{Bytes: sbusBytes(ch, 0), Complete: true}, {Bytes: sbusBytes(ch, 4), Complete: true}}
	if id, _ := DetectLayer("uart", sb); id != "sbus" {
		t.Errorf("S.BUS: %q", id)
	}
	if id, _ := DetectLayer("uart", sb[:1]); id != "" {
		t.Errorf("one S.BUS frame proves nothing: %q", id)
	}
}

// TestDetectLayerClaimsNothingUnproven: random SPI bursts, I2C transfers
// to other addresses, text and random bytes on a UART, 1-Wire
// transactions without a ROM command and USB traffic without a control
// request name no layer.
// TRLC-LINKS: REQ-SDS-247
func TestDetectLayerClaimsNothingUnproven(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	rnd := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(r.Intn(256))
		}
		return b
	}
	claims := map[string]int{}
	for rec := 0; rec < 3000; rec++ {
		k := 2 + r.Intn(10)
		var spi, i2c, uart, ow, usb, mdio []Packet
		for i := 0; i < k; i++ {
			spi = append(spi, mkSPI(rnd(1+r.Intn(24))...))
			i2c = append(i2c, mkI2C(0x30+r.Intn(16), r.Intn(2) == 0, rnd(r.Intn(8))...))
			text := []byte("temperature 21.5 C, humidity 40 %\r\n")
			if r.Intn(2) == 0 {
				text = rnd(1 + r.Intn(40))
			}
			uart = append(uart, Packet{Bytes: text, ByteErr: make([]bool, len(text)), Complete: true})
			b := rnd(1 + r.Intn(10))
			if _, rom := owROMCmds[int(b[0])]; rom {
				b[0] = 0x99
			}
			ow = append(ow, Packet{Bytes: b, ByteErr: make([]bool, len(b)), Complete: true})
			pid := []int{0x1, 0x9, 0x2, 0xa, 0xb, 0x5}[r.Intn(6)]
			usb = append(usb, mkUSB(pid, rnd(r.Intn(12))...))
			h := 1<<12 | r.Intn(1<<12) // clause 22
			mdio = append(mdio, Packet{Bytes: []byte{byte(h >> 8), byte(h), byte(r.Intn(256)), byte(r.Intn(256))}, HasAddr: true, Complete: true})
		}
		for bus, ps := range map[string][]Packet{"spi": spi, "i2c": i2c, "uart": uart, "onewire": ow, "usbls": usb, "mdio": mdio} {
			if id, why := DetectLayer(bus, ps); id != "" {
				claims[bus+" "+id]++
				if claims[bus+" "+id] == 1 {
					t.Errorf("%s: claimed %s (%s) on %q", bus, id, why, ps[0].Bytes)
				}
			}
		}
	}
	if len(claims) > 0 {
		t.Logf("claims %v", claims)
	}
}
