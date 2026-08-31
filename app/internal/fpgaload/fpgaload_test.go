package fpgaload

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"open-sds/app/internal/iface"
)

// fakeConfig / fakeSer record the reload sequence and inject failures. order is
// shared so a test can assert Configure ran before the nCONFIG pulse.
type fakeConfig struct {
	order     *[]string
	pulses    int
	pulseErr  error
	doneAfter int // ConfDone reports true on the Nth call (1-based); 0 = never
	doneCalls int
	doneErr   error
}

func (f *fakeConfig) PulseNCONFIG() error {
	*f.order = append(*f.order, "pulse")
	f.pulses++
	return f.pulseErr
}

func (f *fakeConfig) ConfDone() (bool, error) {
	f.doneCalls++
	if f.doneErr != nil {
		return false, f.doneErr
	}
	return f.doneAfter > 0 && f.doneCalls >= f.doneAfter, nil
}

type fakeSer struct {
	order      *[]string
	configured int
	configErr  error
	chunks     [][]byte
	sendErrAt  int // fail on the Nth SendChunk (1-based); 0 = never
	sendErr    error
}

func (f *fakeSer) Configure() error {
	*f.order = append(*f.order, "configure")
	f.configured++
	return f.configErr
}

func (f *fakeSer) SendChunk(b []byte) error {
	f.chunks = append(f.chunks, append([]byte(nil), b...))
	if f.sendErrAt > 0 && len(f.chunks) == f.sendErrAt {
		return f.sendErr
	}
	return nil
}

// fastOpts is a deterministic Options: no real sleeping, a tight poll bound.
func fastOpts() (Options, *[]string) {
	order := &[]string{}
	return Options{
		Timeout:   5 * time.Millisecond,
		PollEvery: 1 * time.Millisecond,
		Sleep:     func(time.Duration) {},
	}, order
}

// dataChunks returns the streamed bytes excluding the trailing init-clock chunk.
func dataStreamed(f *fakeSer) []byte {
	if len(f.chunks) == 0 {
		return nil
	}
	var out []byte
	for _, c := range f.chunks[:len(f.chunks)-1] {
		out = append(out, c...)
	}
	return out
}

// rbf builds an n-byte container in NATIVE Quartus order — the shape of every
// image this app embeds, and the one the loader must bit-reverse. Reload now
// auto-detects the order, so a test payload has to be a real container: a blob
// of arbitrary bytes is (correctly) refused.
func rbf(n int) []byte { return container(hdrNative, n) }

// rbfRaw builds an n-byte container in PRE-REVERSED vendor order — the shape of
// the on-NAND factory image, which the loader must ship raw.
func rbfRaw(n int) []byte { return container(hdrPreReversed, n) }

func container(hdr []byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 1)
	}
	for i := 0; i < rbfPreambleLen && i < n; i++ {
		b[i] = 0xFF
	}
	if n > rbfHeaderOff {
		copy(b[rbfHeaderOff:], hdr)
	}
	return b
}

func TestReloadRejectsShortBitstream(t *testing.T) {
	o, order := fastOpts()
	cfg := &fakeConfig{order: order, doneAfter: 1}
	ser := &fakeSer{order: order}
	if err := Reload(cfg, ser, rbf(minRBFLen-1), o); err == nil {
		t.Fatal("expected error for a too-short bitstream")
	}
	// Nothing must touch the fabric when the input is rejected.
	if ser.configured != 0 || cfg.pulses != 0 {
		t.Fatalf("short bitstream disturbed the fabric: configured=%d pulses=%d", ser.configured, cfg.pulses)
	}
}

func TestReloadConfiguresBeforePulse(t *testing.T) {
	o, order := fastOpts()
	cfg := &fakeConfig{order: order, doneAfter: 1}
	ser := &fakeSer{order: order}
	if err := Reload(cfg, ser, rbf(70000), o); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(*order) < 2 || (*order)[0] != "configure" || (*order)[1] != "pulse" {
		t.Fatalf("want configure before pulse, got %v", *order)
	}
}

func TestReloadStreamsExactBytesAndInitClocks(t *testing.T) {
	o, order := fastOpts()
	o.ChunkSize = 4096
	o.InitClocks = 16
	cfg := &fakeConfig{order: order, doneAfter: 2}
	ser := &fakeSer{order: order}
	// A pre-reversed (factory-order) container: auto-detection must ship it RAW.
	in := rbfRaw(70000) // not a multiple of the chunk size
	if err := Reload(cfg, ser, in, o); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	// Pre-reversed container ⇒ no bitrev ⇒ the stream equals the input exactly.
	if got := dataStreamed(ser); !bytes.Equal(got, in) {
		t.Fatalf("streamed %d data bytes, want %d (mismatch)", len(got), len(in))
	}
	last := ser.chunks[len(ser.chunks)-1]
	if len(last) != 16 || !bytes.Equal(last, make([]byte, 16)) {
		t.Fatalf("trailing chunk = %d bytes, want 16 zero init clocks", len(last))
	}
	// Every data chunk is bounded by ChunkSize.
	for i, c := range ser.chunks[:len(ser.chunks)-1] {
		if len(c) > 4096 {
			t.Fatalf("chunk %d is %d bytes, exceeds ChunkSize", i, len(c))
		}
	}
}

// TestReloadBitReverses pins the auto path: a NATIVE-order container is
// bit-reversed onto the wire with nothing set in Options.
func TestReloadBitReverses(t *testing.T) {
	o, order := fastOpts()
	cfg := &fakeConfig{order: order, doneAfter: 1}
	ser := &fakeSer{order: order}
	in := rbf(70000)
	orig := append([]byte(nil), in...)
	if err := Reload(cfg, ser, in, o); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	want := make([]byte, len(in))
	for i, v := range in {
		want[i] = bitrevTable[v]
	}
	if got := dataStreamed(ser); !bytes.Equal(got, want) {
		t.Fatal("bit-reversed stream does not match bitrev(input)")
	}
	// The input slice must be untouched (bitrev works on a copy).
	if !bytes.Equal(in, orig) {
		t.Fatal("Reload mutated the caller's bitstream")
	}
}

func TestReloadConfDoneTimeout(t *testing.T) {
	o, order := fastOpts() // Timeout 5ms / poll 1ms => ~6 polls
	cfg := &fakeConfig{order: order, doneAfter: 0}
	ser := &fakeSer{order: order}
	err := Reload(cfg, ser, rbf(70000), o)
	if err == nil {
		t.Fatal("expected CONF_DONE timeout")
	}
	if cfg.doneCalls == 0 {
		t.Fatal("timeout without polling CONF_DONE")
	}
}

func TestReloadPropagatesErrors(t *testing.T) {
	sentinel := errors.New("boom")
	cases := []struct {
		name string
		mut  func(*fakeConfig, *fakeSer)
	}{
		{"configure", func(_ *fakeConfig, s *fakeSer) { s.configErr = sentinel }},
		{"pulse", func(c *fakeConfig, _ *fakeSer) { c.pulseErr = sentinel }},
		{"stream", func(_ *fakeConfig, s *fakeSer) { s.sendErrAt = 1; s.sendErr = sentinel }},
		{"confdone", func(c *fakeConfig, _ *fakeSer) { c.doneErr = sentinel }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, order := fastOpts()
			cfg := &fakeConfig{order: order, doneAfter: 1}
			ser := &fakeSer{order: order}
			tc.mut(cfg, ser)
			if err := Reload(cfg, ser, rbf(70000), o); !errors.Is(err, sentinel) {
				t.Fatalf("want wrapped sentinel, got %v", err)
			}
		})
	}
}

// ─── EnsureStandard ──────────────────────────────────────────────────────────

func goodRead(_ iface.Plane, sel uint16) (uint16, error) {
	switch sel {
	case iface.SelVERSION:
		return 0x0052, nil
	case iface.SelBUILDID_LO:
		return uint16(iface.BuildID & 0xFFFF), nil
	case iface.SelBUILDID_HI:
		return uint16(iface.BuildID >> 16), nil
	}
	return 0, nil
}

func badRead(_ iface.Plane, sel uint16) (uint16, error) {
	if sel == iface.SelVERSION {
		return 0x0000, nil // wrong version → Verify fails
	}
	return 0, nil
}

func TestEnsureStandardSkipsReloadWhenVerified(t *testing.T) {
	o, order := fastOpts()
	cfg := &fakeConfig{order: order, doneAfter: 1}
	ser := &fakeSer{order: order}
	if err := EnsureStandard(goodRead, cfg, ser, o); err != nil {
		t.Fatalf("EnsureStandard: %v", err)
	}
	if cfg.pulses != 0 || ser.configured != 0 {
		t.Fatalf("verified fabric was needlessly reconfigured: pulses=%d configured=%d", cfg.pulses, ser.configured)
	}
}

func TestEnsureStandardErrsWithoutBitstream(t *testing.T) {
	// The default (!withbitstream) test build has no embedded bitstream, so a
	// mismatched fabric cannot be reconfigured.
	if len(Standard()) != 0 {
		t.Skip("built with an embedded bitstream")
	}
	o, order := fastOpts()
	cfg := &fakeConfig{order: order, doneAfter: 1}
	ser := &fakeSer{order: order}
	if err := EnsureStandard(badRead, cfg, ser, o); err == nil {
		t.Fatal("expected an error when the fabric mismatches and no bitstream is embedded")
	}
	if cfg.pulses != 0 {
		t.Fatal("attempted a reload with no bitstream")
	}
}
