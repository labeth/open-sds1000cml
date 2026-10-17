// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of the pulse-coded single-wire protocols against
// sigrok's onewire_link -> onewire_network, rgb_led_ws281x, am230x, cec and
// sae_j1850_vpw decoders (oracle_test.go).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// sortAnns orders annotations of several classes by their start.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func sortAnns(a []ann) []ann {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].I0 < a[j-1].I0; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
	return a
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestOracleOneWire(t *testing.T) {
	const sr = 2000000
	w := newTimeline(sr)
	w.idle(100e-6)
	rom := owROM(0x28, 0xFF, 0x4C, 0x60, 0x91, 0x16, 0x05)
	rom2 := owROM(0x10, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06)
	owReset(w, false, true)
	owByte(w, 0x55, false)
	for _, v := range rom {
		owByte(w, v, false)
	}
	owByte(w, 0x44, false)
	owByte(w, 0xBE, false)
	w.idle(100e-6)
	owReset(w, false, true)
	owByte(w, 0x33, false)
	for _, v := range rom2 {
		owByte(w, v, false)
	}
	w.idle(100e-6)
	owReset(w, false, true)
	owByte(w, 0xF0, false)
	owSearchROM(w, rom)
	owByte(w, 0x12, false)
	w.idle(100e-6)
	owReset(w, false, true)
	owByte(w, 0xCC, false)
	owByte(w, 0x4E, false)
	owByte(w, 0x7F, false)
	w.idle(100e-6)
	owReset(w, false, false)
	w.idle(300e-6)

	r := DecodeOneWire(w.codes(), 1.0/sr, OneWireCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	romAt := -1
	var romB []int
	for _, sp := range r.Spans {
		switch {
		case sp.Kind == "ack" && sp.Text == "PRES":
			repo = append(repo, "P:true")
		case sp.Kind == "nak":
			repo = append(repo, "P:false")
		case sp.Kind == "data" || sp.Kind == "frame-error":
			if _, named := oneWireROM[sp.Val]; named && romAt < 0 && (len(repo) == 0 || strings.HasPrefix(repo[len(repo)-1], "P:")) {
				repo = append(repo, fmt.Sprintf("C:%02x", sp.Val))
				if sp.Val != 0xCC {
					romAt, romB = 0, nil
				}
				continue
			}
			if romAt >= 0 {
				romB = append(romB, sp.Val)
				if len(romB) == 8 {
					v := uint64(0)
					for i := 7; i >= 0; i-- {
						v = v<<8 | uint64(romB[i])
					}
					repo = append(repo, fmt.Sprintf("R:%016x", v))
					romAt = -1
				}
				continue
			}
			repo = append(repo, fmt.Sprintf("D:%02x", sp.Val))
		}
	}
	anns := sigrokDecode(t, sr, []string{"OWR"}, [][]byte{w.bits}, "onewire_link:owr=OWR,onewire_network", "onewire_network=text")
	var orc []string
	cmdRe := regexp.MustCompile(`^ROM command: 0x([0-9a-f]{2})`)
	for _, a := range anns {
		switch {
		case strings.HasPrefix(a.Text, "Reset/presence: "):
			orc = append(orc, "P:"+strings.TrimPrefix(a.Text, "Reset/presence: "))
		case cmdRe.MatchString(a.Text):
			orc = append(orc, "C:"+cmdRe.FindStringSubmatch(a.Text)[1])
		case strings.HasPrefix(a.Text, "ROM: 0x"):
			orc = append(orc, "R:"+strings.TrimPrefix(a.Text, "ROM: 0x"))
		case strings.HasPrefix(a.Text, "Data: 0x"):
			orc = append(orc, "D:"+strings.TrimPrefix(a.Text, "Data: 0x"))
		}
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("1-Wire differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestOracleWS281x(t *testing.T) {
	const sr = 20000000
	w := newTimeline(sr)
	w.add(0, 60e-6)
	leds := []int{0xFF, 0x00, 0x80, 0x12, 0x34, 0x56, 0x00, 0x00, 0x00, 0xA5, 0x5A, 0x01}
	wsFrame(w, 800e3, leds)
	wsFrame(w, 800e3, leds[:6])
	r := DecodeWS281x(w.codes(), 1.0/sr, WS281xCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for i := 0; i+2 < len(r.Bytes); i += 3 {
		g, rd, b := r.Bytes[i], r.Bytes[i+1], r.Bytes[i+2]
		repo = append(repo, fmt.Sprintf("#%02x%02x%02x", rd, g, b))
	}
	anns := sigrokDecode(t, sr, []string{"DIN"}, [][]byte{w.bits}, "rgb_led_ws281x:din=DIN", "rgb_led_ws281x=rgb")
	var orc []string
	for _, a := range anns {
		orc = append(orc, a.Text)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("WS281x differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestOracleDHT(t *testing.T) {
	const sr = 1000000
	w := newTimeline(sr)
	w.idle(2e-3)
	dhtReading(w, 1000, [4]int{0x02, 0x8C, 0x01, 0x5F}, -1)
	w.idle(2e-3)
	dhtReading(w, 18000, [4]int{0x2D, 0x00, 0x17, 0x00}, 0x11)
	w.idle(2e-3)
	dhtReading(w, 1000, [4]int{0x01, 0x90, 0x80, 0x65}, -1)
	r := DecodeDHT(w.codes(), 1.0/sr, DHTCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" || sp.Kind == "frame-error" {
			repo = append(repo, fmt.Sprintf("0x%02x", sp.Val))
		}
		if sp.Kind == "frame-error" {
			repo = append(repo, "not OK")
		} else if sp.Kind == "data" && len(repo)%6 == 5 {
			repo = append(repo, "OK")
		}
	}
	anns := sortAnns(append(sigrokDecode(t, sr, []string{"SDA"}, [][]byte{w.bits}, "am230x:sda=SDA", "am230x=byte"),
		sigrokDecode(t, sr, []string{"SDA"}, [][]byte{w.bits}, "am230x:sda=SDA", "am230x=checksum")...))
	var orc []string
	for _, a := range anns {
		orc = append(orc, strings.TrimPrefix(strings.TrimPrefix(a.Text, "Byte: "), "Checksum: "))
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("DHT differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestOracleCEC(t *testing.T) {
	const sr = 100000
	w := newTimeline(sr)
	w.idle(5e-3)
	cecMessage(w, []int{0x40, 0x04}, []int{0, 0})
	cecMessage(w, []int{0x4F, 0x82, 0x10, 0x00}, []int{1, 1, 1, 1})
	cecMessage(w, []int{0x10, 0x8F}, []int{0, 0})
	cecMessage(w, []int{0x01}, []int{1})
	r := DecodeCEC(w.codes(), 1.0/sr, CECCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		switch sp.Kind {
		case "data":
			repo = append(repo, fmt.Sprintf("0x%02x", sp.Val))
		case "ack":
			repo = append(repo, "ACK")
		case "nak":
			repo = append(repo, "NACK")
		}
	}
	pd := "cec:cec=CEC"
	anns := sortAnns(append(append(sigrokDecode(t, sr, []string{"CEC"}, [][]byte{w.bits}, pd, "cec=bytes"),
		sigrokDecode(t, sr, []string{"CEC"}, [][]byte{w.bits}, pd, "cec=ack")...),
		sigrokDecode(t, sr, []string{"CEC"}, [][]byte{w.bits}, pd, "cec=nack")...))
	var orc []string
	for _, a := range anns {
		orc = append(orc, a.Text)
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("CEC differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-233
func TestOracleJ1850(t *testing.T) {
	needSigrokPD(t, "sae_j1850_vpw")
	const sr = 1000000
	w := newTimeline(sr)
	w.add(0, 500e-6)
	// (no mode 1 frames: sigrok's decoder fails on its PID annotation)
	frames := [][]int{{0x68, 0x6A, 0xF1, 0x09, 0x02}, {0x48, 0x6B, 0x10, 0x41, 0x00, 0xBE, 0x1F, 0xB8, 0x10}, {0x61, 0x1A, 0xF1, 0x22}}
	for _, f := range frames {
		j1850Frame(w, f, false)
	}
	r := DecodeJ1850(w.codes(), 1.0/sr, J1850Cfg{})
	if !r.OK || strings.Contains(r.Text, "!") {
		t.Fatalf("repo: %q %s", r.Text, r.Error)
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "sae_j1850_vpw:data=D", "sae_j1850_vpw=byte")
	var orc []int
	for _, a := range anns {
		v, err := strconv.ParseInt(a.Text, 16, 32)
		if err != nil {
			t.Fatalf("annotation %q", a.Text)
		}
		orc = append(orc, int(v))
	}
	eqBytes(t, "J1850 bytes", r.Bytes, orc)
}
