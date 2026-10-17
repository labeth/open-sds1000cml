// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// USB full speed (12 Mbit/s) vs sigrok's usb_signalling + usb_packet, and
// the token CRC5 against sigrok's own CRC5 routine (oracle_usbls_test.go).
// At full speed the line idling high is D+: the repo decodes that one.

import (
	"math/rand"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018
func TestUSBTokenCRC5(t *testing.T) {
	// the USB specification's example: address 15h, endpoint Eh, the CRC5
	// sent as 1 0 1 1 1, read least significant first 1Dh
	if got := usbCRC5(0x15 | 0xE<<7); got != 0x1D {
		t.Fatalf("CRC5 of address 15h endpoint Eh is %02X, want 1D", got)
	}
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 500; i++ {
		v := rng.Intn(1 << 11)
		if usbCRC5(v) != usblsCRC5(usblsLSB(v, 11)) {
			t.Fatalf("CRC5 of %03X: %02X, sigrok's routine %02X", v, usbCRC5(v), usblsCRC5(usblsLSB(v, 11)))
		}
	}
}

// oracleUSBFSWave renders packets as the full-speed pair: J = (D+ high,
// D- low), NRZI from J, each packet's EOP two bit times of SE0 and a J.
// TRLC-LINKS: REQ-SDS-018
func oracleUSBFSWave(sr float64, packets [][]int, gapBits float64) (dp, dm []byte) {
	bt := 1.0 / 12e6
	wp, wm := newTimeline(sr), newTimeline(sr)
	sym := func(p, m byte, cells float64) {
		wp.add(p, cells*bt)
		wm.add(m, cells*bt)
	}
	j := true
	emit := func(isJ bool, cells float64) {
		if isJ {
			sym(1, 0, cells)
		} else {
			sym(0, 1, cells)
		}
	}
	emit(true, 20)
	for _, bits := range packets {
		j = true
		for _, b := range bits {
			if b == 0 {
				j = !j
			}
			emit(j, 1)
		}
		sym(0, 0, 2)
		emit(true, 1+gapBits)
	}
	emit(true, 20)
	return wp.bits, wm.bits
}

// TRLC-LINKS: REQ-SDS-018
func TestOracleUSBFS(t *testing.T) {
	const sr = 120000000 // 10 samples a bit
	packets := [][]int{
		usblsPacketBits(0xD, usblsTokenBits(0x15, 0xE)),                                      // SETUP
		usblsPacketBits(0x3, usblsDataBits([]int{0x80, 0x06, 0x00, 0x01, 0, 0, 0x40, 0}, 0)), // DATA0
		usblsPacketBits(0x2, nil),                                               // ACK
		usblsPacketBits(0x5, usblsTokenBits(0x7F, 0xF)),                         // SOF-shaped token
		usblsPacketBits(0xB, usblsDataBits([]int{0xFF, 0xFF, 0xFF, 0x00}, 0x1)), // DATA1, bad CRC
	}
	dp, dm := oracleUSBFSWave(sr, packets, 12)
	r := DecodeUSBLS(bitsToCodes(dp), 1.0/sr, USBLSCfg{})
	if !r.OK || r.Baud != 12000000 {
		t.Fatalf("repo: ok=%v %d b/s %s", r.OK, r.Baud, r.Error)
	}
	var repoPIDs []string
	for _, sp := range r.Spans {
		if sp.Kind == "addr" {
			repoPIDs = append(repoPIDs, sp.Text)
		}
	}
	if strings.Count(r.Text, "!") != 2 {
		t.Fatalf("repo flags %q: want the bad CRC16's two bytes", r.Text)
	}
	anns := sigrokDecode(t, sr, []string{"DP", "DM"}, [][]byte{dp, dm},
		"usb_signalling:dp=DP:dm=DM:signalling=full-speed,usb_packet:signalling=full-speed", "usb_packet=packet")
	var orcPIDs []string
	for _, a := range anns {
		f := strings.Fields(a.Text)
		if len(f) > 0 {
			orcPIDs = append(orcPIDs, f[0])
		}
	}
	if len(orcPIDs) != len(repoPIDs) {
		t.Fatalf("packets: repo %v sigrok %v", repoPIDs, orcPIDs)
	}
	for i := range repoPIDs {
		if !strings.EqualFold(repoPIDs[i], orcPIDs[i]) {
			t.Fatalf("packet %d: repo %s sigrok %s (%v / %v)", i, repoPIDs[i], orcPIDs[i], repoPIDs, orcPIDs)
		}
	}
	crcs := sigrokDecode(t, sr, []string{"DP", "DM"}, [][]byte{dp, dm},
		"usb_signalling:dp=DP:dm=DM:signalling=full-speed,usb_packet:signalling=full-speed", "usb_packet=crc16-err")
	if len(crcs) != 1 {
		t.Fatalf("sigrok CRC16 errors %d, want 1", len(crcs))
	}
}
