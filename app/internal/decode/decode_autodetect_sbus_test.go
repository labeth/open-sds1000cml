// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"testing"
)

// sbusFrame is an S.BUS frame: 0x0F, sixteen 11-bit channels packed LSB
// first into 22 bytes, the flags byte and the end byte.
// TRLC-LINKS: REQ-SDS-244
func sbusFrame(ch [16]int, flags, end int) []int {
	out := []int{0x0f}
	acc, n := 0, 0
	for _, v := range ch {
		acc |= (v & 0x7ff) << n
		n += 11
		for n >= 8 {
			out = append(out, acc&0xff)
			acc >>= 8
			n -= 8
		}
	}
	return append(out, flags, end)
}

// sbusLineOpts shapes a synthetic S.BUS line: its frames' period, header,
// length and end byte, the framing and whether it idles low.
type sbusLineOpts struct {
	periodS      float64
	frames       int
	header, end  int
	length       int // bytes a frame, 25 for S.BUS
	baud         float64
	parity       string
	inverted     bool
	leadS, cutS  float64 // idle before the first frame; the record's start cut off
	sampleRate   float64
	seed         int64
	channelsZero bool
}

// sbusOpts is a true S.BUS line: 100 kBd 8E2 inverted, a frame each period.
// TRLC-LINKS: REQ-SDS-244
func sbusOpts(periodS float64, sr float64) sbusLineOpts {
	return sbusLineOpts{periodS: periodS, frames: 4, header: 0x0f, end: 0x00, length: 25, baud: 100000,
		parity: "even", inverted: true, leadS: 1e-3, sampleRate: sr, seed: 1}
}

// sbusLine records the line o describes.
// TRLC-LINKS: REQ-SDS-244
func sbusLine(o sbusLineOpts) []uint8 {
	rng := rand.New(rand.NewSource(o.seed))
	w := newTimeline(o.sampleRate)
	w.idle(o.leadS)
	for f := 0; f < o.frames; f++ {
		t0 := w.t
		var ch [16]int
		for i := range ch {
			if !o.channelsZero {
				ch[i] = 172 + rng.Intn(1640)
			}
		}
		b := sbusFrame(ch, 0, o.end)
		b[0] = o.header
		for len(b) < o.length {
			b = append(b[:len(b)-1], rng.Intn(256), o.end)
		}
		if len(b) > o.length {
			b = append(b[:o.length-1], o.end)
		}
		for _, v := range b {
			tlUART(w, v, o.baud, o.parity, 2)
		}
		w.idle(o.periodS - (w.t - t0))
	}
	if o.inverted {
		for i := range w.bits {
			w.bits[i] ^= 1
		}
	}
	c := w.codes()
	return c[int(o.cutS*o.sampleRate):]
}

// isSBUS reports a detection on S.BUS's framing.
// TRLC-LINKS: REQ-SDS-244
func isSBUS(r Result) bool {
	return r.Proto == "uart" && r.Inverted && r.Parity == "even" && r.Baud == sbusBaud
}

// TestAutodetectFindsSBUS: an S.BUS line (100 kBd 8E2, idling low), its
// frames every 7 or 14 ms, at two sample rates, on either channel, with the
// record starting inside a frame and with converter noise, is named a UART
// on S.BUS's framing, its bytes the frames from 0x0F.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-244, REQ-SDS-247
func TestAutodetectFindsSBUS(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	noisy := func(c []uint8) []uint8 {
		out := make([]uint8, len(c))
		prev := float64(c[0])
		for i, v := range c {
			prev += (float64(v) - prev) * 0.5
			out[i] = uint8(min(255, max(0, prev+rng.NormFloat64()*2)))
		}
		return out
	}
	for _, period := range []float64{7e-3, 14e-3} {
		for _, sr := range []float64{1e6, 2.5e6} {
			for _, cut := range []float64{0, 1.5e-3 + 1e-3} {
				o := sbusOpts(period, sr)
				o.cutS = cut
				c := sbusLine(o)
				for k, pair := range [][2][]uint8{{c, idleLike(c)}, {idleLike(c), c}, {noisy(c), idleLike(c)}} {
					r, st := AutodetectFast(pair[0], pair[1], 1/sr, "hex")
					if !isSBUS(r) || r.Roles.ChA != k&1 {
						t.Errorf("S.BUS %v ms at %v Sa/s cut %v case %d: named %q %d Bd parity %q inverted %v on C%d (%s)",
							period*1e3, sr, cut, k, r.Proto, r.Baud, r.Parity, r.Inverted, r.Roles.ChA+1, r.Src)
						continue
					}
					if n := sbusFrames(r, st); n < 2 { // the spans count the strided samples
						t.Errorf("S.BUS %v ms: %d frames", period*1e3, n)
					}
				}
			}
		}
	}
	// a frame of all channels at zero and S.BUS2's end bytes
	o := sbusOpts(7e-3, 1e6)
	o.channelsZero, o.end = true, 0x14
	if r, _ := AutodetectFast(sbusLine(o), nil, 1e-6, "hex"); !isSBUS(r) {
		t.Errorf("S.BUS2 zero channels: named %q (%s)", r.Proto, r.Src)
	}
}

// TestAutodetectSBUSCrossConfusion: S.BUS's framing takes no other line.
// The captures of every protocol, UART text at LIN's, DMX512's, MIDI's,
// Modbus's and S.BUS's own rates, an inverted UART, and lines that are
// nearly S.BUS (another header, 24 bytes a frame, a bad end byte, frames 10
// ms apart, idling high, odd parity) are never read on S.BUS's framing.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-244, REQ-SDS-247
func TestAutodetectSBUSCrossConfusion(t *testing.T) {
	for _, tc := range protoDetectCases() {
		if r, _ := AutodetectFast(tc.c1, tc.c2, tc.colT, "hex"); isSBUS(r) {
			t.Errorf("%s read as S.BUS", tc.name)
		}
	}
	rng := rand.New(rand.NewSource(9))
	for _, baud := range []float64{19200, 250000, 31250, 9600, 100000, 115200} {
		for _, inv := range []bool{false, true} {
			sr := 32 * baud
			w := newTimeline(sr)
			w.idle(2e-3)
			for i := 0; i < 40; i++ {
				tlUART(w, 0x20+rng.Intn(0x5f), baud, "even", 2)
				if i%10 == 9 {
					w.idle(3e-3)
				}
			}
			w.idle(2e-3)
			if inv {
				for i := range w.bits {
					w.bits[i] ^= 1
				}
			}
			c := w.codes()
			if r, _ := AutodetectFast(c, idleLike(c), 1/sr, "hex"); isSBUS(r) {
				t.Errorf("UART %v Bd text (inverted %v) read as S.BUS", baud, inv)
			}
		}
	}
	near := map[string]func(*sbusLineOpts){
		"header 0E":    func(o *sbusLineOpts) { o.header = 0x0e },
		"24 bytes":     func(o *sbusLineOpts) { o.length = 24 },
		"26 bytes":     func(o *sbusLineOpts) { o.length = 26 },
		"end 55":       func(o *sbusLineOpts) { o.end = 0x55 },
		"10 ms apart":  func(o *sbusLineOpts) { o.periodS = 10e-3 },
		"idle high":    func(o *sbusLineOpts) { o.inverted = false },
		"odd parity":   func(o *sbusLineOpts) { o.parity = "odd" },
		"no parity":    func(o *sbusLineOpts) { o.parity = "none" },
		"115200 Bd":    func(o *sbusLineOpts) { o.baud = 115200 },
		"one frame":    func(o *sbusLineOpts) { o.frames = 1 },
		"3 ms apart":   func(o *sbusLineOpts) { o.periodS = 3.5e-3 },
		"20 ms apart":  func(o *sbusLineOpts) { o.periodS = 20e-3 },
		"end 0x44":     func(o *sbusLineOpts) { o.end = 0x44 },
		"header F0 lo": func(o *sbusLineOpts) { o.header = 0xf0 },
	}
	for name, f := range near {
		o := sbusOpts(7e-3, 1e6)
		f(&o)
		c := sbusLine(o)
		if r, _ := AutodetectFast(c, idleLike(c), 1e-6, "hex"); isSBUS(r) {
			t.Errorf("%s: read as S.BUS (%s)", name, r.Text)
		}
	}
}

// TestSBUSFramesCountsWholeFrames: the frames the record cuts are not
// counted, and a broken frame between whole ones makes the line no S.BUS.
// TRLC-LINKS: REQ-SDS-244
func TestSBUSFramesCountsWholeFrames(t *testing.T) {
	o := sbusOpts(7e-3, 1e6)
	o.frames, o.cutS = 5, 2.5e-3
	c := sbusLine(o)
	r := DecodeUART(c, 1e-6, UARTCfg{Baud: sbusBaud, Bits: 8, Parity: "even", Inverted: true})
	if n := sbusFrames(r, 1e-6); n != 4 {
		t.Errorf("4 whole frames after a cut one: counted %d", n)
	}
	for i := range r.Spans {
		if r.Spans[i].Val == 0x0f && i > 30 {
			r.Spans[i+5].Kind = "parity-error"
			break
		}
	}
	if n := sbusFrames(r, 1e-6); n != 0 {
		t.Errorf("a frame with a parity error: counted %d", n)
	}
	if n := sbusFrames(r, 0); n != 0 {
		t.Errorf("no sample time: %d", n)
	}
	for _, b := range []int{0x00, 0x04, 0x14, 0x24, 0x34} {
		if !sbusEnd(b) {
			t.Errorf("end byte %02X refused", b)
		}
	}
	for _, b := range []int{0x01, 0x44, 0x0f, 0x80} {
		if sbusEnd(b) {
			t.Errorf("end byte %02X taken", b)
		}
	}
}
