// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math/rand"
	"testing"
)

// boxDownClean is boxDown without noise: a box-averaged record at its best.
// TRLC-LINKS: REQ-SDS-018
func boxDownClean(w []uint8, box, phase int) []uint8 {
	var d []uint8
	for i := phase; i+box <= len(w); i += box {
		s := 0
		for k := 0; k < box; k++ {
			s += int(w[i+k])
		}
		d = append(d, uint8(s/box))
	}
	return d
}

// pickDown is every box-th sample from phase: a record of picked samples.
// TRLC-LINKS: REQ-SDS-018
func pickDown(w []uint8, box, phase int) []uint8 {
	var d []uint8
	for i := phase; i < len(w); i += box {
		d = append(d, w[i])
	}
	return d
}

// The buses BUS stops decoding at slow timebases stop where the record
// cannot hold them: at the finest sample a timebase's screen of 2^19 words
// allows (32 ns at 1ms/div, 64 at 2, 128 at 5, 256 at 10, 512 at 20), as
// their images decimate - picked samples on the qualify image's BUS records
// (WS281x, SWIM, S/PDIF), box averages on the frame image (USB) - each
// decodes its full-rate record's bytes from every phase at the last
// timebase it reads, and not from every phase at the next: WS281x and
// low-speed SWIM to 10ms/div, S/PDIF to 1ms/div, USB full speed to 200us/div
// (the full rate; 500us/div's 32 ns), USB low speed to 5ms/div.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-214
func TestBusDecodeEndsWhereTheRecordCannotHoldIt(t *testing.T) {
	const sr = 500e6 // the full rate, 2 ns
	type bus struct {
		name     string
		codes    []uint8
		decode   func(c []uint8, sampleS float64) Result
		pick     bool
		ok, fail int // samples of 2 ns a record's sample holds: at the last timebase read, and the next
	}
	var buses []bus
	{
		w := newTimeline(sr)
		w.add(0, 60e-6)
		wsFrame(w, 800e3, []int{0xFF, 0x00, 0x80, 0x12, 0x34, 0x56})
		wsFrame(w, 800e3, []int{0x01, 0x02, 0x03})
		buses = append(buses, bus{"WS281x", w.codes(), func(c []uint8, s float64) Result { return DecodeWS281x(c, s, WS281xCfg{}) }, true, 128, 256})
	}
	{
		const T = 22 / 4e6 // low speed: a bit is 22 clocks of 4 MHz
		w := newTimeline(sr)
		w.add(1, 20e-6)
		swimSync(w, T)
		for _, f := range [][3]int{{0, 3, 1}, {0, 8, 2}, {0, 8, 0x50}, {1, 8, 0x5A}, {1, 8, 0xC3}} {
			swimFrame(w, f[0], f[1], f[2], T, 1, false, 3*T)
		}
		buses = append(buses, bus{"SWIM", w.codes(), func(c []uint8, s float64) Result { return DecodeSWIM(c, s, LineCfg{}) }, true, 128, 256})
	}
	{
		w := newTimeline(sr)
		w.add(0, 50e-9)
		var s [][2]int
		for i := 0; i < 6; i++ {
			s = append(s, [2]int{(i * 0x1357) & 0xffffff, 0xFFFFF0 - i*0x10})
		}
		spdifStream(w, 48e3, 190, s)
		buses = append(buses, bus{"S/PDIF", w.codes(), func(c []uint8, s float64) Result { return DecodeSPDIF(c, s, SPDIFCfg{}) }, true, 16, 32})
	}
	usbPackets := [][]int{
		usblsPacketBits(0xD, usblsTokenBits(0x15, 0xE)),
		usblsPacketBits(0x3, usblsDataBits([]int{0x80, 0x06, 0x00, 0x01, 0, 0, 0x40, 0}, 0)),
		usblsPacketBits(0x2, nil),
	}
	{
		dp, _ := oracleUSBFSWave(sr, usbPackets, 12)
		buses = append(buses, bus{"USB full speed", bitsToCodes(dp), func(c []uint8, s float64) Result { return DecodeUSBLS(c, s, USBLSCfg{}) }, false, 1, 16})
	}
	{
		_, dm := oracleUSBLSWave(sr, usbPackets, 12)
		buses = append(buses, bus{"USB low speed", bitsToCodes(dm), func(c []uint8, s float64) Result { return DecodeUSBLS(c, s, USBLSCfg{}) }, false, 64, 128})
	}
	for _, b := range buses {
		t.Run(b.name, func(t *testing.T) {
			ref := b.decode(b.codes, 1/sr)
			if !ref.OK || len(ref.Bytes) == 0 {
				t.Fatalf("the full-rate record: %q", ref.Error)
			}
			want := fmt.Sprint(ref.Bytes)
			reads := func(r Result) bool { return r.OK && fmt.Sprint(r.Bytes) == want }
			down := func(box, phase int, rng *rand.Rand) []uint8 {
				switch {
				case box == 1:
					return b.codes
				case b.pick:
					return pickDown(b.codes, box, phase)
				case rng != nil:
					return boxDown(b.codes, box, rng)
				}
				return boxDownClean(b.codes, box, phase)
			}
			failed := 0
			for seed := 0; seed < 20; seed++ {
				rng := rand.New(rand.NewSource(int64(seed)))
				if r := b.decode(down(b.ok, seed*b.ok/20, rng), float64(b.ok)/sr); !reads(r) {
					t.Fatalf("seed %d: at %g ns a sample: %v %q, want %s", seed, 2*float64(b.ok), r.Bytes, r.Error, want)
				}
				if !reads(b.decode(down(b.fail, seed*b.fail/20, nil), float64(b.fail)/sr)) {
					failed++
				}
			}
			if failed == 0 {
				t.Fatalf("read from every phase at %g ns a sample, past where BUS stops", 2*float64(b.fail))
			}
			t.Logf("%g ns: every phase; %g ns: %d of 20 phases fail", 2*float64(b.ok), 2*float64(b.fail), failed)
		})
	}
}
