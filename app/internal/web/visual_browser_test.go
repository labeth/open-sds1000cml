// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"math"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/testenv"
)

// TestVisualMIDIBrowser: a MIDI bus (two notes on, one off) decodes in BUS,
// the Visual view draws the keyboard with the note held, lists the messages,
// and its Sound plays the notes in the browser.
// TRLC-LINKS: REQ-SDS-278, REQ-SDS-279
func TestVisualMIDIBrowser(t *testing.T) {
	testenv.NeedNode(t)
	const spb = 8
	sc := newWsScope(uartSig([]byte{0x90, 60, 100, 0x90, 64, 90, 0x80, 60, 0}, spb, 100, 200), 1/(31250.0*spb))
	srv := httptest.NewServer(New(sc, newFuzzAnalog(), newDecodePanel(sc, &fuzzPanel{z: sc.fuzzScope}), nil).Handler())
	defer srv.Close()
	wsDriver(t, "visual_browser.mjs", srv.URL, "midi")
}

// audioScope serves a wsScope's frames, and while the bus samples are leased
// records of I2S at that interval: a 1 kHz sine at 48 kHz, 32-bit slots, left
// at half scale and right at a quarter, word select on C1 and data on C2.
type audioScope struct {
	*wsScope
	mu     sync.Mutex
	leased float64
	seq    uint64
}

// TRLC-LINKS: REQ-SDS-280
func (a *audioScope) LeaseBusSamples(sampleS float64, _ time.Duration) {
	a.mu.Lock()
	a.leased = sampleS
	a.mu.Unlock()
}

// TRLC-LINKS: REQ-SDS-280
func (a *audioScope) WithFrame(fn func(*engine.Frame)) {
	a.mu.Lock()
	leased := a.leased
	a.mu.Unlock()
	if !(leased > 0) {
		a.wsScope.WithFrame(fn)
		return
	}
	a.mu.Lock()
	a.seq++
	seq := a.seq + 1000000
	a.mu.Unlock()
	fn(i2sRecord(seq, leased))
}

// i2sRecord is a record of I2S sampled every dt (see audioScope).
// TRLC-LINKS: REQ-SDS-280
func i2sRecord(seq uint64, dt float64) *engine.Frame {
	const fs, slot = 48000.0, 32
	bitS := 1 / (fs * 2 * slot)
	n := int(0.012 / dt) // 12 ms
	c1, c2 := make([]uint8, n), make([]uint8, n)
	for i := 0; i < n; i++ {
		j := int(float64(i) * dt / bitS)
		frame, within := j/(2*slot), j%(2*slot)
		ch := within / slot
		c1[i] = map[bool]uint8{true: 230, false: 25}[ch == 1]
		// the data one bit behind the word select (I2S)
		dj := j - 1
		df, dw := dj/(2*slot), dj%(2*slot)
		amp := 0.5
		if dw/slot == 1 {
			amp = 0.25
		}
		v := uint32(int32(amp * math.Sin(2*math.Pi*1000*float64(df)/fs) * math.MaxInt32))
		bit := (v >> (31 - uint(dw%slot))) & 1
		c2[i] = map[bool]uint8{true: 230, false: 25}[dj >= 0 && bit == 1]
		_ = frame
	}
	return &engine.Frame{Seq: seq, SampleS: dt, Valid: n, C1: c1, C2: c2, TdivS: 1e-3, WinCols: n}
}

// TestVisualAudioBrowser: the Visual view's Digital audio captures a whole
// record (/api/record.bin) of an I2S bus, measures it - 48 kHz, the left at
// -6 dBFS and the right at -12 - draws it and plays it looped.
// TRLC-LINKS: REQ-SDS-280
func TestVisualAudioBrowser(t *testing.T) {
	testenv.NeedNode(t)
	ws := newWsScope(squareSig(64, false), 0)
	sc := &audioScope{wsScope: ws}
	srv := httptest.NewServer(New(sc, newFuzzAnalog(), &fuzzPanel{z: ws.fuzzScope}, nil).Handler())
	defer srv.Close()
	wsDriver(t, "visual_browser.mjs", srv.URL, "audio")
}
