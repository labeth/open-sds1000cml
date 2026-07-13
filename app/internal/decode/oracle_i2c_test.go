package decode

// I2C vs the sigrok `i2c` decoder. Cases cover the clean path plus the edge
// cases that historically break I2C decoders: repeated START (write-then-read
// with no intervening STOP), a NAK'd address, master-NAK on the final read
// byte, address extremes 0x00/0x7F, asymmetric SCL duty cycle, fractional
// samples-per-clock, slave clock stretching, and a sub-bit SDA glitch while
// SCL is high (a documented divergence — see the sda-glitch-scl-high
// subtest).
//
// Where the two sides expose DIFFERENT granularity, the intersection is
// compared (rule 3):
//  - sigrok distinguishes "Start" from "Start repeat"; the repo decoder emits
//    one "start" span kind for both. Repo start count is asserted against
//    sigrok start+repeat-start, and the repeat-start count is asserted on the
//    sigrok side alone.
//  - sigrok encodes direction in the annotation CLASS (data-read/data-write,
//    address-read/address-write); the repo encodes it once per transaction in
//    the "rw" span next to the "addr" span. Payload bytes are therefore
//    compared per direction, pairing repo addr+rw spans (i2cRepoAddrs).
//  - sigrok's address classes carry TWO annotations per address byte: the
//    value ("Address write: 24", 7-bit with the default shifted format,
//    matching the repo's val>>1) and a bare direction marker ("Write"/
//    "Read"); i2cAnnVals keeps only the value annotations, so counts and
//    alignment stay 1:1 with repo spans.

import (
	"strconv"
	"strings"
	"testing"
)

// i2cTxn is one transaction for the oracle generator.
type i2cTxn struct {
	addr7   int
	read    bool  // R/W bit = 1
	nakAddr bool  // slave leaves SDA high on the address ACK clock
	data    []int // payload bytes (skipped entirely when nakAddr)
	nakLast bool  // NAK on the LAST data byte (master terminating a read)
	noStop  bool  // omit STOP: the next txn begins with a repeated START
}

// i2cGen drives SCL and SDA as two time-locked timelines. loFrac is the
// fraction of the clock period SCL spends low (duty-cycle control); SDA only
// changes while SCL is low except for the START/STOP conditions themselves.
// The current SDA level is tracked (d) so START/STOP can drop SCL first while
// HOLDING SDA — releasing SDA while SCL is still high would fabricate a STOP.
type i2cGen struct {
	scl, sda *timeline
	bt       float64 // one SCL period, seconds
	loFrac   float64
	d        byte
}

func newI2CGen(sr, fclk, loFrac float64) *i2cGen {
	return &i2cGen{scl: newTimeline(sr), sda: newTimeline(sr), bt: 1 / fclk, loFrac: loFrac, d: 1}
}

func (g *i2cGen) seg(c, d byte, dur float64) {
	g.scl.add(c, dur)
	g.sda.add(d, dur)
	g.d = d
}

func (g *i2cGen) idle(periods float64) { g.seg(1, 1, periods*g.bt) }

// start works from bus idle AND mid-transaction (repeated START): SCL is
// dropped so SDA can be released high without producing a STOP, then SDA
// falls while SCL is high. Mid-transaction the SCL rising with SDA high
// clocks in one stray bit on both sides — exactly as on a real bus — and
// both decoders must discard it when the START arrives.
func (g *i2cGen) start() {
	lo := g.bt * g.loFrac
	g.seg(0, g.d, lo/2)
	g.seg(0, 1, lo/2)
	g.seg(1, 1, g.bt/2)
	g.seg(1, 0, g.bt/2)
}

// byteACK clocks one byte MSB-first plus the 9th (ACK) clock; nak leaves SDA
// high on the 9th clock.
func (g *i2cGen) byteACK(v int, nak bool) {
	lo, hi := g.bt*g.loFrac, g.bt*(1-g.loFrac)
	for k := 7; k >= 0; k-- {
		b := byte(v>>k) & 1
		g.seg(0, b, lo)
		g.seg(1, b, hi)
	}
	a := byte(0)
	if nak {
		a = 1
	}
	g.seg(0, a, lo)
	g.seg(1, a, hi)
}

// stretch emulates slave clock stretching: SCL held low for the given number
// of full clock periods with SDA parked at its current level (SDA may legally
// move while SCL is low, but a quiet line keeps the vector minimal).
func (g *i2cGen) stretch(periods float64) { g.seg(0, g.d, periods*g.bt) }

// byteACKStretched is byteACK with clock stretching inserted at the two spots
// real slaves stretch: after `afterBit` data bits (1-based, i.e. mid-byte)
// for midPeriods, and again just before the 9th (ACK) clock for ackPeriods.
func (g *i2cGen) byteACKStretched(v int, nak bool, afterBit int, midPeriods, ackPeriods float64) {
	lo, hi := g.bt*g.loFrac, g.bt*(1-g.loFrac)
	for k := 7; k >= 0; k-- {
		b := byte(v>>k) & 1
		g.seg(0, b, lo)
		g.seg(1, b, hi)
		if 8-k == afterBit {
			g.stretch(midPeriods)
		}
	}
	g.stretch(ackPeriods)
	a := byte(0)
	if nak {
		a = 1
	}
	g.seg(0, a, lo)
	g.seg(1, a, hi)
}

// byteACKGlitched is byteACK (ACK'd) with a ONE-sample SDA dip to 0 injected
// during the SCL-high plateau of data bit `glitchBit` (1-based; that bit must
// be a 1 for a dip to exist). The dip lands 4 samples into the plateau, well
// after the rising edge that already sampled the bit — so it is pure noise,
// not a data change. By the letter of the I2C spec the dip's falling edge is
// a START and its rising recovery a STOP; decoders differ in which of the two
// they act on (see the sda-glitch-scl-high subtest).
func (g *i2cGen) byteACKGlitched(v, glitchBit int) {
	lo, hi := g.bt*g.loFrac, g.bt*(1-g.loFrac)
	dt := 1 / g.scl.sr // one sample
	for k := 7; k >= 0; k-- {
		b := byte(v>>k) & 1
		g.seg(0, b, lo)
		if 8-k == glitchBit {
			g.seg(1, b, 4*dt)
			g.seg(1, 0, dt)
			g.seg(1, b, hi-5*dt)
		} else {
			g.seg(1, b, hi)
		}
	}
	g.seg(0, 0, lo)
	g.seg(1, 0, hi)
}

func (g *i2cGen) stop() {
	lo := g.bt * g.loFrac
	g.seg(0, g.d, lo/2) // SCL drops first, SDA held (no false condition)
	g.seg(0, 0, lo/2)   // SDA set up low while SCL is low
	g.seg(1, 0, g.bt/2)
	g.seg(1, 1, g.bt/2) // SDA rises while SCL high = STOP
}

// oracleI2CWaves renders transactions at the given clock rate and duty cycle.
// Timings accumulate in seconds so non-integer samples-per-clock behave like
// a real capture.
func oracleI2CWaves(sr, fclk, loFrac float64, txns []i2cTxn) (scl, sda []byte) {
	g := newI2CGen(sr, fclk, loFrac)
	g.idle(4)
	for _, tx := range txns {
		g.start()
		ab := tx.addr7 << 1
		if tx.read {
			ab |= 1
		}
		g.byteACK(ab, tx.nakAddr)
		if !tx.nakAddr {
			for i, d := range tx.data {
				g.byteACK(d, tx.nakLast && i == len(tx.data)-1)
			}
		}
		if !tx.noStop {
			g.stop()
			g.idle(2)
		}
	}
	g.idle(4)
	return g.scl.bits, g.sda.bits
}

// i2cAnnVals keeps only the value-carrying annotations ("<prefix>: XX") of a
// sigrok address/data class stream — dropping the bare "Write"/"Read"
// direction markers the address classes also emit — and parses the hex
// values, so the result is 1:1 with repo addr/data spans.
func i2cAnnVals(t *testing.T, anns []ann, prefix string) ([]ann, []int) {
	t.Helper()
	var keep []ann
	var vals []int
	for _, a := range anns {
		rest, ok := strings.CutPrefix(a.Text, prefix+": ")
		if !ok {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(rest), 16, 32)
		if err != nil {
			t.Fatalf("annotation %q: %q is not a hex byte", a.Text, rest)
		}
		keep = append(keep, a)
		vals = append(vals, int(v))
	}
	return keep, vals
}

// i2cRepoAddrs pairs each repo "addr" span with the "rw" span that follows it
// and splits the addresses by direction — the same partition sigrok's
// address-write/address-read classes provide.
func i2cRepoAddrs(t *testing.T, res Result) (w, r []int) {
	t.Helper()
	addr := -1
	for _, s := range res.Spans {
		switch s.Kind {
		case "addr":
			addr = s.Val
		case "rw":
			if addr < 0 {
				t.Fatalf("rw span %q with no preceding addr span", s.Text)
			}
			if s.Text == "R" {
				r = append(r, addr)
			} else {
				w = append(w, addr)
			}
			addr = -1
		}
	}
	return w, r
}

func TestOracleI2C(t *testing.T) {
	needSigrok(t)
	const sr = 1_000_000

	run := func(t *testing.T, scl, sda []byte, class string) []ann {
		return sigrokDecode(t, sr, []string{"SCL", "SDA"}, [][]byte{scl, sda},
			"i2c:scl=SCL:sda=SDA", "i2c="+class)
	}
	decode := func(t *testing.T, scl, sda []byte) Result {
		res := DecodeI2C(bitsToCodes(scl), bitsToCodes(sda), 1.0/sr, I2CCfg{})
		if !res.OK {
			t.Fatalf("repo decode failed: %s", res.Error)
		}
		return res
	}
	// framing asserts start/repeat-start/stop counts on both sides; the repo
	// has no separate repeat-start kind, so its "start" count is checked
	// against the sigrok sum (granularity intersection).
	framing := func(t *testing.T, res Result, scl, sda []byte, starts, reps, stops int) {
		t.Helper()
		if n := len(run(t, scl, sda, "start")); n != starts {
			t.Fatalf("sigrok saw %d starts, want %d", n, starts)
		}
		if n := len(run(t, scl, sda, "repeat-start")); n != reps {
			t.Fatalf("sigrok saw %d repeat-starts, want %d", n, reps)
		}
		if n := len(run(t, scl, sda, "stop")); n != stops {
			t.Fatalf("sigrok saw %d stops, want %d", n, stops)
		}
		if n := countSpans(res, "start"); n != starts+reps {
			t.Fatalf("repo saw %d starts, want %d (sigrok start+repeat-start)", n, starts+reps)
		}
		if n := countSpans(res, "stop"); n != stops {
			t.Fatalf("repo saw %d stops, want %d", n, stops)
		}
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors on STOP-terminated traffic", n)
		}
	}

	t.Run("single-write", func(t *testing.T) {
		payload := []int{0x00, 0x55, 0xAA, 0xFF, 0x0F, 0xF0, 0x80, 0x01}
		scl, sda := oracleI2CWaves(sr, 50_000, 0.5, []i2cTxn{{addr7: 0x24, data: payload}})
		res := decode(t, scl, sda)
		dw, dwVals := i2cAnnVals(t, run(t, scl, sda, "data-write"), "Data write")
		eqBytes(t, "generated payload (sigrok view)", dwVals, payload) // guards against a vacuous empty==empty pass
		eqBytes(t, "write payload", res.Bytes, dwVals)
		eqAligned(t, "write data spans", res, "data", dw, 2, 22) // end: +1 SCL period (sigrok extends through the ACK edge)
		_, aw := i2cAnnVals(t, run(t, scl, sda, "address-write"), "Address write")
		repoW, repoR := i2cRepoAddrs(t, res)
		eqBytes(t, "write address", repoW, aw)
		eqBytes(t, "read addresses (none expected)", repoR, nil)
		// 9 ACK clocks (address + 8 data), zero NAKs, sample-aligned.
		acks := run(t, scl, sda, "ack")
		eqAligned(t, "ACK positions", res, "ack", acks, 2, 22) // end: +1 SCL period (convention, see harness)
		if n := len(run(t, scl, sda, "nack")); n != 0 || countSpans(res, "nak") != 0 {
			t.Fatalf("NAKs on clean write: repo %d, sigrok %d", countSpans(res, "nak"), n)
		}
		if len(acks) != len(payload)+1 {
			t.Fatalf("sigrok saw %d ACKs, want %d", len(acks), len(payload)+1)
		}
		framing(t, res, scl, sda, 1, 0, 1)
	})

	t.Run("single-read", func(t *testing.T) {
		payload := []int{0xDE, 0xAD, 0xBE, 0xEF}
		// Master NAKs the final byte — the standard read termination.
		scl, sda := oracleI2CWaves(sr, 50_000, 0.5,
			[]i2cTxn{{addr7: 0x50, read: true, data: payload, nakLast: true}})
		res := decode(t, scl, sda)
		dr, drVals := i2cAnnVals(t, run(t, scl, sda, "data-read"), "Data read")
		eqBytes(t, "generated payload (sigrok view)", drVals, payload)
		eqBytes(t, "read payload", res.Bytes, drVals)
		eqAligned(t, "read data spans", res, "data", dr, 2, 22) // end: +1 SCL period (convention, see harness)
		_, ar := i2cAnnVals(t, run(t, scl, sda, "address-read"), "Address read")
		repoW, repoR := i2cRepoAddrs(t, res)
		eqBytes(t, "read address", repoR, ar)
		eqBytes(t, "write addresses (none expected)", repoW, nil)
		// ACK on address + all but the last byte; exactly one NAK, aligned.
		if n := countSpans(res, "ack"); n != len(payload) || len(run(t, scl, sda, "ack")) != len(payload) {
			t.Fatalf("ACK count: repo %d, want %d", n, len(payload))
		}
		eqAligned(t, "NAK position", res, "nak", run(t, scl, sda, "nack"), 2, 22) // end: +1 SCL period (convention)
		if n := countSpans(res, "nak"); n != 1 {
			t.Fatalf("repo saw %d NAKs, want 1", n)
		}
		framing(t, res, scl, sda, 1, 0, 1)
	})

	t.Run("repeated-start-write-then-read", func(t *testing.T) {
		// Register read idiom: write the register index, repeated START, read
		// back two bytes — no STOP in between.
		scl, sda := oracleI2CWaves(sr, 50_000, 0.5, []i2cTxn{
			{addr7: 0x3A, data: []int{0x10}, noStop: true},
			{addr7: 0x3A, read: true, data: []int{0x77, 0x88}, nakLast: true},
		})
		res := decode(t, scl, sda)
		_, dwVals := i2cAnnVals(t, run(t, scl, sda, "data-write"), "Data write")
		_, drVals := i2cAnnVals(t, run(t, scl, sda, "data-read"), "Data read")
		eqBytes(t, "write leg payload", dwVals, []int{0x10})
		eqBytes(t, "read leg payload", drVals, []int{0x77, 0x88})
		// Repo keeps one flat byte stream; sigrok splits by direction — the
		// intersection is the concatenation in wire order.
		eqBytes(t, "combined payload", res.Bytes, append(dwVals, drVals...))
		_, aw := i2cAnnVals(t, run(t, scl, sda, "address-write"), "Address write")
		_, ar := i2cAnnVals(t, run(t, scl, sda, "address-read"), "Address read")
		repoW, repoR := i2cRepoAddrs(t, res)
		eqBytes(t, "write leg address", repoW, aw)
		eqBytes(t, "read leg address", repoR, ar)
		framing(t, res, scl, sda, 1, 1, 1)
	})

	t.Run("nak-address", func(t *testing.T) {
		// Slave absent: address byte goes out, SDA stays high on the ACK
		// clock, master gives up with STOP. Both sides must still report the
		// address and flag exactly one NAK and zero data bytes.
		scl, sda := oracleI2CWaves(sr, 50_000, 0.5, []i2cTxn{{addr7: 0x29, nakAddr: true}})
		res := decode(t, scl, sda)
		_, aw := i2cAnnVals(t, run(t, scl, sda, "address-write"), "Address write")
		repoW, _ := i2cRepoAddrs(t, res)
		eqBytes(t, "NAK'd address", repoW, aw)
		eqBytes(t, "NAK'd address value", aw, []int{0x29})
		eqAligned(t, "NAK position", res, "nak", run(t, scl, sda, "nack"), 2, 22) // end: +1 SCL period (convention)
		if n := countSpans(res, "nak"); n != 1 {
			t.Fatalf("repo saw %d NAKs, want 1", n)
		}
		if n := len(run(t, scl, sda, "ack")); n != 0 || countSpans(res, "ack") != 0 {
			t.Fatalf("ACKs on a NAK'd address: repo %d, sigrok %d", countSpans(res, "ack"), n)
		}
		if n := len(run(t, scl, sda, "data-write")); n != 0 || len(res.Bytes) != 0 {
			t.Fatalf("data after NAK'd address: repo %d bytes, sigrok %d annotations", len(res.Bytes), n)
		}
		framing(t, res, scl, sda, 1, 0, 1)
	})

	t.Run("address-extremes", func(t *testing.T) {
		// 0x00 (general call) written and 0x7F read, back-to-back STOP-
		// separated transactions in ONE capture — the second START must be a
		// plain Start again on the sigrok side (STOP resets its repeat flag).
		scl, sda := oracleI2CWaves(sr, 50_000, 0.5, []i2cTxn{
			{addr7: 0x00, data: []int{0x12}},
			{addr7: 0x7F, read: true, data: []int{0x34}, nakLast: true},
		})
		res := decode(t, scl, sda)
		_, aw := i2cAnnVals(t, run(t, scl, sda, "address-write"), "Address write")
		_, ar := i2cAnnVals(t, run(t, scl, sda, "address-read"), "Address read")
		repoW, repoR := i2cRepoAddrs(t, res)
		eqBytes(t, "address 0x00 write", repoW, aw)
		eqBytes(t, "address 0x7F read", repoR, ar)
		eqBytes(t, "write address value", aw, []int{0x00})
		eqBytes(t, "read address value", ar, []int{0x7F})
		_, dwVals := i2cAnnVals(t, run(t, scl, sda, "data-write"), "Data write")
		_, drVals := i2cAnnVals(t, run(t, scl, sda, "data-read"), "Data read")
		eqBytes(t, "combined payload", res.Bytes, append(dwVals, drVals...))
		framing(t, res, scl, sda, 2, 0, 2)
	})

	t.Run("duty-cycle-fractional-spc", func(t *testing.T) {
		// SCL low 70% of the period AND 27.027 samples/clock at 1 MHz: bit
		// sampling happens on the rising edge on both sides, so an asymmetric
		// duty cycle with fractional timing must change nothing.
		payload := []int{0x69, 0x96, 0x5A, 0xA5}
		scl, sda := oracleI2CWaves(sr, 37_000, 0.7, []i2cTxn{{addr7: 0x42, data: payload}})
		res := decode(t, scl, sda)
		dw, dwVals := i2cAnnVals(t, run(t, scl, sda, "data-write"), "Data write")
		eqBytes(t, "generated payload (sigrok view)", dwVals, payload)
		eqBytes(t, "duty-cycle payload", res.Bytes, dwVals)
		eqAligned(t, "duty-cycle data spans", res, "data", dw, 2, 30) // end: +1 fractional SCL period (27.03 samples)
		acks := run(t, scl, sda, "ack")
		if len(acks) != len(payload)+1 || countSpans(res, "ack") != len(payload)+1 {
			t.Fatalf("ACK count: repo %d, sigrok %d, want %d",
				countSpans(res, "ack"), len(acks), len(payload)+1)
		}
		framing(t, res, scl, sda, 1, 0, 1)
	})

	t.Run("clock-stretching", func(t *testing.T) {
		// A slave stretches SCL low mid-transfer: 5 clock periods after the
		// 4th data bit of the first byte AND 7 periods before that byte's ACK
		// clock. Both decoders sample on SCL rising edges only, so a stretch
		// merely delays the remaining edges — payloads, ACK positions and
		// span placement must agree exactly as on the unstretched bus.
		const fclk = 50_000
		const spc = sr / fclk // 20 samples per SCL period
		g := newI2CGen(sr, fclk, 0.5)
		g.idle(4)
		g.start()
		g.byteACK(0x24<<1, false) // address 0x24 write, ACK'd
		g.byteACKStretched(0xA7, false, 4, 5, 7)
		g.byteACK(0x3C, false)
		g.stop()
		g.idle(4)
		scl, sda := g.scl.bits, g.sda.bits
		res := decode(t, scl, sda)
		dw, dwVals := i2cAnnVals(t, run(t, scl, sda, "data-write"), "Data write")
		eqBytes(t, "generated payload (sigrok view)", dwVals, []int{0xA7, 0x3C})
		eqBytes(t, "stretched payload", res.Bytes, dwVals)
		eqAligned(t, "stretched data spans", res, "data", dw, 2, 22) // end: +1 SCL period (convention, see harness)
		_, aw := i2cAnnVals(t, run(t, scl, sda, "address-write"), "Address write")
		repoW, repoR := i2cRepoAddrs(t, res)
		eqBytes(t, "stretched-transfer address", repoW, aw)
		eqBytes(t, "read addresses (none expected)", repoR, nil)
		acks := run(t, scl, sda, "ack")
		eqAligned(t, "ACK positions", res, "ack", acks, 2, 22) // end: +1 SCL period (convention)
		if len(acks) != 3 || countSpans(res, "nak") != 0 || len(run(t, scl, sda, "nack")) != 0 {
			t.Fatalf("handshake counts: sigrok %d ACKs (want 3), repo %d NAKs (want 0)",
				len(acks), countSpans(res, "nak"))
		}
		// Guard against a vacuous pass if the generator ever loses the
		// stretches: the 0xA7 span must be ~5 periods wider than the normal
		// 7-period first-to-last-bit extent, and its ACK must sit ~7 periods
		// (not 1) past the span end. Checked on repo spans; eqAligned above
		// already ties sigrok to the same geometry.
		var data, ack []Span
		for _, s := range res.Spans {
			switch s.Kind {
			case "data":
				data = append(data, s)
			case "ack":
				ack = append(ack, s)
			}
		}
		if w := data[0].I1 - data[0].I0; w < 11*spc {
			t.Fatalf("0xA7 span %d samples wide; mid-byte stretch missing from the vector", w)
		}
		if d := ack[1].I0 - data[0].I1; d < 6*spc {
			t.Fatalf("0xA7 ACK %d samples after last bit; pre-ACK stretch missing from the vector", d)
		}
		framing(t, res, scl, sda, 1, 0, 1)
	})

	t.Run("sda-glitch-scl-high", func(t *testing.T) {
		// DOCUMENTED DIVERGENCE (pinned per side, SPI mid-word-gap pattern).
		// A one-sample SDA dip during SCL high, 4 samples after bit 2 of data
		// byte 0xF0 was clocked. By the spec's letter the dip is a START
		// (falling edge) immediately followed by a STOP (rising recovery).
		//  - The repo scans per sample with symmetric rules: it emits BOTH —
		//    a start span at the dip and a stop span one sample later — then
		//    (out of transaction) ignores the remaining clocks, and still
		//    reports the real STOP at the end. No bytes survive the glitch.
		//  - sigrok's FSM sees the falling edge (repeat-start) but then
		//    enters FIND ADDRESS, whose wait({0:'r'}) watches ONLY SCL
		//    risings (i2c/pd.py): the dip's recovery is invisible. It
		//    re-parses the 6 remaining data bits + ACK clock + STOP-setup
		//    rising as a phantom "Address write: 60" (0b1100_0000>>1), then
		//    waits in FIND ACK where the real STOP is invisible too — ZERO
		//    stop annotations.
		// Both still agree on start count (repo starts == sigrok
		// start+repeat-start), surviving data bytes (none) and ACKs (1).
		const fclk = 50_000
		const spc = sr / fclk // 20 samples per SCL period
		g := newI2CGen(sr, fclk, 0.5)
		g.idle(4)
		g.start()
		g.byteACK(0x24<<1, false)  // address 0x24 write, ACK'd
		g.byteACKGlitched(0xF0, 2) // dip mid-plateau of bit 2 (SDA high there)
		g.stop()
		g.idle(4)
		scl, sda := g.scl.bits, g.sda.bits
		res := decode(t, scl, sda)
		var starts, stops []Span
		for _, s := range res.Spans {
			switch s.Kind {
			case "start":
				starts = append(starts, s)
			case "stop":
				stops = append(stops, s)
			}
		}
		// Agreement: the dip's falling edge is a (repeated) START on both
		// sides, at the same sample.
		sigStarts := run(t, scl, sda, "start")
		reps := run(t, scl, sda, "repeat-start")
		if len(sigStarts) != 1 || len(reps) != 1 {
			t.Fatalf("sigrok saw %d starts + %d repeat-starts, want 1 + 1", len(sigStarts), len(reps))
		}
		if len(starts) != 2 {
			t.Fatalf("repo saw %d starts, want 2 (real + glitch)", len(starts))
		}
		if d := starts[1].I0 - reps[0].I0; d > 2 || d < -2 {
			t.Fatalf("glitch start misaligned: repo %d, sigrok repeat-start %d", starts[1].I0, reps[0].I0)
		}
		// Agreement: no data byte survives the glitch; the address ACK is the
		// only handshake bit either side resolves.
		eqBytes(t, "surviving data bytes", res.Bytes, nil)
		if n := len(run(t, scl, sda, "data-write")); n != 0 {
			t.Fatalf("sigrok decoded %d data bytes across the glitch, want 0", n)
		}
		if countSpans(res, "ack") != 1 || len(run(t, scl, sda, "ack")) != 1 ||
			countSpans(res, "nak") != 0 || len(run(t, scl, sda, "nack")) != 0 {
			t.Fatalf("handshake counts: repo %d ACK/%d NAK, sigrok %d/%d, want 1/0 both",
				countSpans(res, "ack"), countSpans(res, "nak"),
				len(run(t, scl, sda, "ack")), len(run(t, scl, sda, "nack")))
		}
		// DIVERGENCE pin, repo side: the dip recovery is a STOP one sample
		// after the glitch START, and the real STOP (SDA rise half a period
		// before the trailing 4-period idle) is still reported — 2 stops,
		// address list just the genuine 0x24.
		if len(stops) != 2 {
			t.Fatalf("repo saw %d stops, want 2 (glitch recovery + real STOP)", len(stops))
		}
		if stops[0].I0 != starts[1].I0+1 {
			t.Fatalf("glitch stop at %d, want %d (one sample after the glitch start)", stops[0].I0, starts[1].I0+1)
		}
		if want := len(scl) - 4*spc - spc/2; stops[1].I0 != want {
			t.Fatalf("real STOP at %d, want %d", stops[1].I0, want)
		}
		repoW, repoR := i2cRepoAddrs(t, res)
		eqBytes(t, "repo addresses (post-glitch bits discarded)", repoW, []int{0x24})
		eqBytes(t, "repo read addresses (none expected)", repoR, nil)
		if n := countSpans(res, "frame-error"); n != 0 {
			t.Fatalf("repo flagged %d frame errors; glitch STOP closed its transaction", n)
		}
		// DIVERGENCE pin, sigrok side: zero stops, and the post-glitch bits
		// come back as the phantom address 0x60.
		if n := len(run(t, scl, sda, "stop")); n != 0 {
			t.Fatalf("sigrok saw %d stops, want 0 (FSM blind to STOP in FIND ADDRESS/FIND ACK)", n)
		}
		_, aw := i2cAnnVals(t, run(t, scl, sda, "address-write"), "Address write")
		eqBytes(t, "sigrok addresses (genuine + phantom)", aw, []int{0x24, 0x60})
	})
}
