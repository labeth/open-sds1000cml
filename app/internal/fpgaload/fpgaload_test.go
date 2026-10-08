// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD
package fpgaload

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"open-sds/app/internal/iface"
)

// fakePort models the MAX V configuration port: every write is recorded;
// nSTATUS drops on an nCONFIG-low write and rises statusAfter reads later;
// CONF_DONE asserts doneAfter reads after the port is parked following a
// complete shift (never when doneNever).
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-092
type fakePort struct {
	writes      []uint16
	reads       int
	statusAfter int // reads after the release before nSTATUS rises
	doneAfter   int // reads after the park before CONF_DONE rises
	doneNever   bool
	writeErrAt  int // fail the Nth write (1-based); 0 = never
	writeErr    error

	nconfigLow  bool
	releaseRead int // reads counted since the release
	parked      bool
	parkRead    int
	clocks      int // DCLK rising edges since the last nCONFIG low
	pulses      int
	confDone    bool
}

// TRLC-LINKS: REQ-SDS-005
func (f *fakePort) WriteCfg(v uint16) error {
	f.writes = append(f.writes, v)
	if f.writeErrAt > 0 && len(f.writes) == f.writeErrAt {
		return f.writeErr
	}
	prev := uint16(BitNCONFIG)
	if len(f.writes) >= 2 {
		prev = f.writes[len(f.writes)-2]
	}
	if v&BitNCONFIG == 0 {
		if !f.nconfigLow {
			f.pulses++
		}
		f.nconfigLow, f.confDone, f.parked = true, false, false
		f.clocks, f.releaseRead, f.parkRead = 0, 0, 0
		return nil
	}
	if f.nconfigLow { // release
		f.nconfigLow = false
		f.releaseRead = 0
	}
	if v&BitDCLK != 0 && prev&BitDCLK == 0 {
		f.clocks++
	}
	if v == BitNCONFIG && f.clocks > 0 && prev&BitDCLK != 0 {
		// the park write right after the last clock
		f.parked, f.parkRead = true, 0
	}
	return nil
}

// TRLC-LINKS: REQ-SDS-005
func (f *fakePort) ReadCfg() (uint16, error) {
	f.reads++
	var v uint16
	if f.nconfigLow {
		return 0, nil // nSTATUS asserted low, CONF_DONE low
	}
	f.releaseRead++
	if f.releaseRead > f.statusAfter {
		v |= BitNSTATUS
	}
	if f.parked {
		f.parkRead++
		if !f.doneNever && f.parkRead > f.doneAfter {
			f.confDone = true
		}
	}
	if f.confDone {
		v |= BitCONFDONE
	}
	return v, nil
}

// dataBits reconstructs the bytes shifted in: DATA0 sampled at every DCLK
// rising edge, MSB-first, between the nCONFIG release and the park.
// TRLC-LINKS: REQ-SDS-005
func (f *fakePort) dataBits() []byte {
	var out []byte
	var cur byte
	n := 0
	prevClk := false
	for i, w := range f.writes {
		if w&BitNCONFIG == 0 {
			out, cur, n, prevClk = nil, 0, 0, false
			continue
		}
		clk := w&BitDCLK != 0
		if clk && !prevClk {
			// the data was presented on the previous (DCLK-low) write with the same DATA0
			if i == 0 || f.writes[i-1]&BitDATA0 != w&BitDATA0 {
				panic("DATA0 changed on the clock edge")
			}
			cur = cur<<1 | byte(w&BitDATA0>>2)
			n++
			if n == 8 {
				out = append(out, cur)
				cur, n = 0, 0
			}
		}
		prevClk = clk
	}
	return out
}

// TRLC-LINKS: REQ-SDS-005
func fastOpts() Options {
	return Options{
		AllowAnyLen: true,
		InitClocks:  16,
		Timeout:     5 * time.Millisecond,
		PollEvery:   time.Millisecond,
		Sleep:       func(time.Duration) {},
	}
}

// TRLC-LINKS: REQ-SDS-005
func TestTransferProgressReportsWrittenBytes(t *testing.T) {
	for _, fail := range []bool{false, true} {
		p := &fakePort{}
		if fail {
			p.writeErrAt = 100
			p.writeErr = errors.New("transfer failed")
		}
		rbf := container(hdrNative, 40000)
		o := fastOpts()
		o.Attempts = 1
		last, calls := -1, 0
		o.Progress = func(sent, total int) {
			if total != len(rbf) || sent <= last || sent > total || len(p.writes) < 2+sent*16 {
				t.Fatalf("invalid progress %d/%d after %d writes", sent, total, len(p.writes))
			}
			last = sent
			calls++
		}
		err := Reload(p, rbf, o)
		if fail {
			if err == nil || last == len(rbf) {
				t.Fatal("failed transfer reported completion")
			}
		} else if err != nil || last != len(rbf) || calls != 4 {
			t.Fatalf("incomplete progress: last=%d calls=%d err=%v", last, calls, err)
		}
	}
}

// TRLC-LINKS: REQ-SDS-092
func container(hdr []byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 1)
	}
	for i := 0; i < rbfPreambleLen && i < n; i++ {
		b[i] = 0xFF
	}
	copy(b[rbfHeaderOff:], hdr)
	return b
}

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-092
func TestReloadSequenceNative(t *testing.T) {
	p := &fakePort{}
	rbf := container(hdrNative, 3000)
	orig := append([]byte(nil), rbf...)
	o := fastOpts()
	if err := Reload(p, rbf, o); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	// nCONFIG pulse first: all-low then nCONFIG high, nothing before it.
	if p.writes[0] != wordReset || p.writes[1] != wordIdle {
		t.Fatalf("first writes %#04x %#04x, want nCONFIG low then high", p.writes[0], p.writes[1])
	}
	if p.pulses != 1 {
		t.Fatalf("nCONFIG pulses = %d, want 1", p.pulses)
	}
	// nCONFIG never drops again while clocking.
	for i, w := range p.writes[1:] {
		if w&BitNCONFIG == 0 {
			t.Fatalf("write %d dropped nCONFIG mid-shift", i+1)
		}
	}
	// Exactly 8 clocks per byte plus the init clocks.
	if p.clocks != 8*len(rbf)+o.InitClocks {
		t.Fatalf("DCLK edges = %d, want 8*%d+%d", p.clocks, len(rbf), o.InitClocks)
	}
	// Two writes per bit: present, then latch (the DATA0 bit is stable across them).
	want := make([]byte, len(rbf))
	for i, v := range rbf {
		want[i] = bitrev(v)
	}
	got := p.dataBits()
	if len(got) < len(want) || !bytes.Equal(got[:len(want)], want) {
		t.Fatalf("shifted data != bitrev(rbf) (got %d bytes)", len(got))
	}
	// The init clocks carry DATA0 = 0.
	for _, b := range got[len(want):] {
		if b != 0 {
			t.Fatalf("init clocks carried data %#02x", b)
		}
	}
	if p.writes[len(p.writes)-1] != wordIdle {
		t.Fatalf("port not parked at nCONFIG-high/DCLK-low: last write %#04x", p.writes[len(p.writes)-1])
	}
	if !bytes.Equal(rbf, orig) {
		t.Fatal("Reload mutated the caller's image")
	}
	if p.reads == 0 {
		t.Fatal("CONF_DONE never polled")
	}
}

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-092
func TestReloadShipsPreReversedRaw(t *testing.T) {
	p := &fakePort{}
	rbf := container(hdrPreReversed, 1200)
	if err := Reload(p, rbf, fastOpts()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	got := p.dataBits()
	if !bytes.Equal(got[:len(rbf)], rbf) {
		t.Fatal("pre-reversed container must ship raw")
	}
}

// TRLC-LINKS: REQ-SDS-092
func TestReloadRefusesBeforeTouchingThePort(t *testing.T) {
	cases := []struct {
		name string
		rbf  []byte
		o    Options
	}{
		{"wrong length", container(hdrNative, 1000), Options{Sleep: func(time.Duration) {}}},
		{"unknown header", container([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9}, 1000), fastOpts()},
		{"short", make([]byte, 10), fastOpts()},
		{"bad preamble", func() []byte { b := container(hdrNative, 1000); b[3] = 0; return b }(), fastOpts()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &fakePort{}
			if err := Reload(p, c.rbf, c.o); err == nil {
				t.Fatal("expected an error")
			}
			if len(p.writes) != 0 || p.reads != 0 {
				t.Fatalf("port touched: %d writes %d reads", len(p.writes), p.reads)
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-005
func TestReloadTimeoutRetriesThenFails(t *testing.T) {
	p := &fakePort{doneNever: true}
	o := fastOpts()
	o.Attempts = 3
	err := Reload(p, container(hdrNative, 500), o)
	if err == nil {
		t.Fatal("expected CONF_DONE timeout")
	}
	if p.pulses != 3 {
		t.Fatalf("nCONFIG pulses = %d, want 3 attempts", p.pulses)
	}
	// each attempt polls CONF_DONE up to Timeout/PollEvery+1 times
	if p.reads < 3*6 {
		t.Fatalf("only %d reads across 3 attempts", p.reads)
	}
}

// TRLC-LINKS: REQ-SDS-005
func TestReloadNStatusTimeout(t *testing.T) {
	p := &fakePort{statusAfter: 1000}
	o := fastOpts()
	o.StatusPolls, o.Attempts = 3, 1
	err := Reload(p, container(hdrNative, 500), o)
	if err == nil || p.clocks != 0 {
		t.Fatalf("expected an nSTATUS timeout with no data clocked (err=%v clocks=%d)", err, p.clocks)
	}
}

// TRLC-LINKS: REQ-SDS-005
func TestReloadPropagatesWriteErrors(t *testing.T) {
	sentinel := errors.New("boom")
	p := &fakePort{writeErrAt: 5, writeErr: sentinel}
	o := fastOpts()
	o.Attempts = 1
	if err := Reload(p, container(hdrNative, 500), o); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel, got %v", err)
	}
}

// TRLC-LINKS: REQ-SDS-092
func TestReloadExplicitOrder(t *testing.T) {
	rbf := container(hdrNative, 400)
	o := fastOpts()
	o.BitOrder = BitOrderRaw // contradicts the container
	if err := Reload(&fakePort{}, rbf, o); err == nil {
		t.Fatal("contradicting order must be refused without Force")
	}
	o.Force = true
	p := &fakePort{}
	if err := Reload(p, rbf, o); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.dataBits()[:len(rbf)], rbf) {
		t.Fatal("forced raw order must ship raw")
	}
}

// ---- EnsureDefault ----

// TRLC-LINKS: REQ-SDS-004
type fakeFabric struct {
	good  bool
	port  *fakePort
	reads int
}

// TRLC-LINKS: REQ-SDS-004
func (f *fakeFabric) read(sel uint16) (uint16, error) {
	f.reads++
	// the identity becomes good once the fake port reports CONF_DONE
	good := f.good || (f.port != nil && f.port.confDone)
	if !good {
		return 0x00c0, nil // floating bus
	}
	switch sel {
	case iface.SelBuildidLo:
		return iface.BuildIDLo, nil
	case iface.SelBuildidHi:
		return iface.BuildIDHi, nil
	case iface.SelVersion:
		return iface.VersionMagic, nil
	case iface.SelFabricId:
		return iface.FabricID, nil
	}
	return 0, nil
}

// TRLC-LINKS: REQ-SDS-004
func TestEnsureDefaultSkipsWhenVerified(t *testing.T) {
	p := &fakePort{}
	fab := &fakeFabric{good: true}
	if err := EnsureDefault(fab.read, p, container(hdrNative, 400), fastOpts()); err != nil {
		t.Fatal(err)
	}
	if len(p.writes) != 0 {
		t.Fatalf("verified fabric was reloaded (%d writes)", len(p.writes))
	}
}

// TRLC-LINKS: REQ-SDS-004, REQ-SDS-005
func TestEnsureDefaultReloadsAndVerifies(t *testing.T) {
	p := &fakePort{}
	fab := &fakeFabric{port: p}
	if err := EnsureDefault(fab.read, p, container(hdrNative, 400), fastOpts()); err != nil {
		t.Fatal(err)
	}
	if p.pulses != 1 {
		t.Fatalf("pulses = %d, want 1", p.pulses)
	}
	if fab.reads < 8 {
		t.Fatalf("identity read %d times, want before and after the reload", fab.reads)
	}
}

// TRLC-LINKS: REQ-SDS-004
func TestEnsureDefaultWithoutBitstream(t *testing.T) {
	p := &fakePort{}
	fab := &fakeFabric{}
	if err := EnsureDefault(fab.read, p, nil, fastOpts()); err == nil {
		t.Fatal("mismatch without a bitstream must fail")
	}
	if len(p.writes) != 0 {
		t.Fatal("attempted a reload with no bitstream")
	}
	if Default() != nil {
		t.Skip("built with the bitstream embedded")
	}
}

// TRLC-LINKS: REQ-SDS-004
func TestEnsureDefaultPostVerifyFails(t *testing.T) {
	p := &fakePort{}
	fab := &fakeFabric{} // never becomes good (port not linked)
	err := EnsureDefault(fab.read, p, container(hdrNative, 400), fastOpts())
	if err == nil {
		t.Fatal("post-reload verify must fail when the identity stays wrong")
	}
	if p.pulses != 1 {
		t.Fatalf("pulses = %d, want 1", p.pulses)
	}
}

// ---- container / bit order ----

// TRLC-LINKS: REQ-SDS-092
func TestDetectOrder(t *testing.T) {
	if o, err := DetectOrder(container(hdrNative, 100)); err != nil || o != OrderNative || !o.Reverse() {
		t.Fatalf("native: %v %v", o, err)
	}
	if o, err := DetectOrder(container(hdrPreReversed, 100)); err != nil || o != OrderPreReversed || o.Reverse() {
		t.Fatalf("pre-reversed: %v %v", o, err)
	}
	// One option bit away (INIT_DONE) → refused with a hint naming the order.
	h := append([]byte(nil), hdrNative...)
	h[4] = 0xF5
	if _, err := DetectOrder(container(h, 100)); err == nil {
		t.Fatal("option-bit header must be refused")
	}
	// The headers are exact per-byte bit reversals of each other.
	for i := range hdrNative {
		if bitrev(hdrNative[i]) != hdrPreReversed[i] {
			t.Fatalf("header byte %d: bitrev(%#02x) = %#02x, want %#02x", i, hdrNative[i], bitrev(hdrNative[i]), hdrPreReversed[i])
		}
	}
}

// TRLC-LINKS: REQ-SDS-092
func TestBitrev(t *testing.T) {
	cases := map[byte]byte{0x00: 0x00, 0xFF: 0xFF, 0x01: 0x80, 0x6A: 0x56, 0xF7: 0xEF, 0xF3: 0xCF, 0xFB: 0xDF, 0x12: 0x48}
	for in, want := range cases {
		if got := bitrev(in); got != want {
			t.Errorf("bitrev(%#02x) = %#02x, want %#02x", in, got, want)
		}
		if bitrev(bitrev(in)) != in {
			t.Errorf("bitrev not an involution at %#02x", in)
		}
	}
}

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-092
func TestRBFLen(t *testing.T) {
	if RBFLen != 368011 { // fpga-specs 05 §3.3
		t.Fatalf("RBFLen = %d", RBFLen)
	}
	if cfgByteOff != 0x0E {
		t.Fatalf("config port byte offset %#x, want 0x0E (physical 0x0300000E)", cfgByteOff)
	}
}
