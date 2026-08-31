// SPDX-License-Identifier: MIT
//
// Offline regression guards for .rbf container auto-detection. No /dev, no
// hardware. The header prefixes below are copied byte-for-byte off real files on
// this workstation (paths + sha256 recorded with each), so the table cases hold
// even where the multi-hundred-KB originals are not present; the full-file cases
// run additionally when they are.

package fpgaload

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// ─── real bytes, transcribed from real files ─────────────────────────────────

// factoryPrefix is bytes 0x00..0x3f of the on-NAND factory image,
// reveng-sds1102cml/firmware/sds1000_fpga.rbf, 368,011 B,
// sha256 fc6acbaeda1aa45af78163c9ef5c20c7846bc642ea99a115940ae558fbb46c00.
var factoryPrefix = []byte{
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0x56, 0xef, 0xef, 0xef, 0xef, 0xef, 0xef, 0xcf, 0xdf, 0xcf, 0xdf, 0xdf, 0x9f, 0x8f, 0x9f, 0x9f,
	0xbf, 0x9f, 0x9f, 0xdf, 0xff, 0x9f, 0xbf, 0x9f, 0xdf, 0xdf, 0xff, 0xff, 0x9f, 0xbf, 0xbf, 0xbf,
}

// ownedPrefix is bytes 0x00..0x3f of this repo's own standard build,
// fpga/standard/acq.rbf, 368,011 B,
// sha256 8fa289f441e00742ea6b578ded479104dbe248bdd8a5e7853ca213f62671e8eb.
var ownedPrefix = []byte{
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0x6a, 0xf7, 0xf7, 0xf7, 0xf7, 0xf7, 0xf7, 0xf3, 0xfb, 0xf3, 0xf8, 0xfa, 0xf1, 0xf1, 0xf8, 0xf8,
	0xfd, 0xf8, 0xf9, 0xfb, 0xfe, 0xf8, 0xfd, 0xf8, 0xfa, 0xfa, 0xff, 0xfe, 0xf8, 0xfc, 0xfd, 0xfc,
}

// alPrefix is bytes 0x00..0x3f of the OTHER vendor image shipped beside the
// bitstream, sds1000cml/work/extracted/config_full/sds1000a_al.bin, 76,361 B,
// sha256 20e04a0d944b0e418308292897bd3d0d177da8b830cd03dcc2ad1bb2da159be7.
// It has the 32-byte 0xff preamble and NOTHING else in common with an EP4CE10
// passive-serial container: it must be refused, not loaded.
var alPrefix = []byte{
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xcc, 0x55, 0xaa, 0x33, 0xf0, 0x00, 0x00, 0x06, 0x08, 0x01, 0x4c, 0x35, 0x0b, 0xbe, 0xc2, 0x00,
	0x00, 0x06, 0x69, 0x01, 0x05, 0x00, 0x09, 0xb2, 0xc3, 0x00, 0x00, 0x06, 0xd1, 0xb0, 0x4b, 0xb0,
}

// optionVariantHeaders are LEGITIMATE Quartus outputs for this device whose
// device header differs from the canonical constant because a global option was
// changed. They prove the 9 bytes are an option field, not a pure magic — and
// they must be REFUSED (safely, with a diagnosis) rather than guessed at.
// From remotework/devfile/re_workflows/out/extract/cfg_work/, 368,011 B each.
var optionVariantHeaders = map[string][]byte{
	"rbf_glob_crc.rbf":         {0x6a, 0xe7, 0xf7, 0xf7, 0xe7, 0xe7, 0xf7, 0xe3, 0xeb},
	"rbf_glob_initdone.rbf":    {0x6a, 0xf7, 0xf7, 0xf7, 0xf5, 0xf7, 0xf7, 0xf3, 0xfb},
	"rbf_glob_autorst_off.rbf": {0x6a, 0xf7, 0xf7, 0xf7, 0xf7, 0xf7, 0xf7, 0xf1, 0xfb},
}

// pad extends a header prefix to n bytes so length policy never masks a header
// verdict. The filler is irrelevant to detection by construction.
func pad(prefix []byte, n int) []byte {
	out := make([]byte, n)
	copy(out, prefix)
	for i := len(prefix); i < n; i++ {
		out[i] = byte(i * 7)
	}
	return out
}

func bitrevAll(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = bitrevTable[v]
	}
	return out
}

// ─── the two constants are each other's bit reversal ─────────────────────────

// TestHeaderConstantsAreBitReversals is the structural reason a one-container
// test is decisive: the two constants cannot be confused, because each is the
// exact per-byte bit reversal of the other and neither is a fixed point.
func TestHeaderConstantsAreBitReversals(t *testing.T) {
	if got := bitrevAll(hdrNative); !bytes.Equal(got, hdrPreReversed) {
		t.Fatalf("bitrev(% x) = % x, want % x", hdrNative, got, hdrPreReversed)
	}
	if bytes.Equal(hdrNative, hdrPreReversed) {
		t.Fatal("the two header constants are equal — detection would be undecidable")
	}
	if len(hdrNative) != rbfHeaderLen || len(hdrPreReversed) != rbfHeaderLen {
		t.Fatalf("header constants are %d/%d bytes, want %d", len(hdrNative), len(hdrPreReversed), rbfHeaderLen)
	}
}

// ─── detection over real container bytes ─────────────────────────────────────

func TestDetectOrderRealBytes(t *testing.T) {
	cases := []struct {
		name string
		img  []byte
		want Order
	}{
		{"factory sds1000_fpga.rbf", pad(factoryPrefix, 4096), OrderPreReversed},
		{"owned acq.rbf", pad(ownedPrefix, 4096), OrderNative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectOrder(tc.img)
			if err != nil {
				t.Fatalf("DetectOrder: %v", err)
			}
			if got != tc.want {
				t.Fatalf("DetectOrder = %v, want %v", got, tc.want)
			}
			if want := tc.want == OrderNative; got.BitRev() != want {
				t.Fatalf("%v.BitRev() = %v, want %v", got, got.BitRev(), want)
			}
		})
	}
}

// TestDetectOrderRefuses covers every input that must NOT produce a load.
func TestDetectOrderRefuses(t *testing.T) {
	cases := []struct {
		name string
		img  []byte
	}{
		{"empty", nil},
		{"truncated to 10 bytes", pad(ownedPrefix, 10)},
		{"truncated mid-header (0x24 bytes)", pad(ownedPrefix, 0x24)},
		{"preamble only, no header", pad(ownedPrefix[:0x20], 0x20)},
		{"sds1000a_al.bin", pad(alPrefix, 4096)},
		{"all zeroes", make([]byte, 4096)},
		{"short preamble (header at 0x1f)", append(pad(ownedPrefix[:0x1f], 0x1f), ownedPrefix[0x20:]...)},
		{"preamble byte corrupted", func() []byte {
			b := pad(ownedPrefix, 4096)
			b[7] = 0xfe
			return b
		}()},
		{"header one bit off", func() []byte {
			b := pad(ownedPrefix, 4096)
			b[0x20] ^= 0x01
			return b
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectOrder(tc.img)
			if err == nil {
				t.Fatalf("DetectOrder accepted %s as %v — it must refuse", tc.name, got)
			}
			if got != OrderUnknown {
				t.Fatalf("DetectOrder returned %v with an error; want OrderUnknown", got)
			}
		})
	}
}

// TestDetectOrderRefusesOptionVariants pins the finding that motivated the
// refuse-rather-than-tolerate rule: these are real, valid Quartus containers for
// this exact device, and detection must decline them out loud (naming the near
// order and the override) rather than accept or misread them.
func TestDetectOrderRefusesOptionVariants(t *testing.T) {
	for name, hdr := range optionVariantHeaders {
		t.Run(name, func(t *testing.T) {
			img := pad(append(bytes.Repeat([]byte{0xff}, 0x20), hdr...), 4096)
			got, err := DetectOrder(img)
			if err == nil {
				t.Fatalf("accepted an option-variant header as %v — must refuse", got)
			}
			for _, want := range []string{"reverse", "ForceBitOrder", "native Quartus order"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not mention %q; got: %v", want, err)
				}
			}
		})
	}
}

// ─── the auto/override policy ────────────────────────────────────────────────

func TestResolveBitOrder(t *testing.T) {
	owned := pad(ownedPrefix, 4096)     // needs bitrev
	factory := pad(factoryPrefix, 4096) // ships raw
	junk := pad(alPrefix, 4096)         // undetectable

	cases := []struct {
		name    string
		img     []byte
		want    BitOrder
		force   bool
		wantErr bool
		wantRev bool
	}{
		{"auto owned => bitrev", owned, BitOrderAuto, false, false, true},
		{"auto factory => raw", factory, BitOrderAuto, false, false, false},
		{"explicit agrees (owned)", owned, BitOrderReverse, false, false, true},
		{"explicit agrees (factory)", factory, BitOrderRaw, false, false, false},
		{"explicit contradicts owned", owned, BitOrderRaw, false, true, false},
		{"explicit contradicts factory", factory, BitOrderReverse, false, true, false},
		{"contradiction + force", owned, BitOrderRaw, true, false, false},
		{"auto on junk refuses", junk, BitOrderAuto, false, true, false},
		{"explicit on junk refuses", junk, BitOrderReverse, false, true, false},
		{"explicit on junk + force", junk, BitOrderReverse, true, false, true},
		{"force alone cannot pick an order", junk, BitOrderAuto, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev, why, err := resolveBitOrder(tc.img, tc.want, tc.force)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected a refusal, got bitrev=%v (%s)", rev, why)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBitOrder: %v", err)
			}
			if rev != tc.wantRev {
				t.Fatalf("bitrev = %v, want %v (%s)", rev, tc.wantRev, why)
			}
			if why == "" {
				t.Fatal("resolveBitOrder returned an empty rationale")
			}
		})
	}
}

// ─── Reload refuses an unreadable container without touching the fabric ──────

// TestReloadRefusesUnknownContainerBeforeTouchingFabric is the safety property
// that matters on live silicon: a container we cannot read must be rejected
// BEFORE Configure and BEFORE the nCONFIG pulse, so a bad embed cannot
// black-screen a working fabric.
func TestReloadRefusesUnknownContainerBeforeTouchingFabric(t *testing.T) {
	o, order := fastOpts()
	cfg := &fakeConfig{order: order, doneAfter: 1}
	ser := &fakeSer{order: order}
	if err := Reload(cfg, ser, pad(alPrefix, 70000), o); err == nil {
		t.Fatal("Reload accepted a non-container image")
	}
	if ser.configured != 0 || cfg.pulses != 0 || len(ser.chunks) != 0 {
		t.Fatalf("unreadable container disturbed the fabric: configured=%d pulses=%d chunks=%d",
			ser.configured, cfg.pulses, len(ser.chunks))
	}
}

// TestReloadRefusesContradictoryOverride: an explicit BitOrder that the
// container disagrees with is an error, not a silent win for the caller.
func TestReloadRefusesContradictoryOverride(t *testing.T) {
	o, order := fastOpts()
	o.BitOrder = BitOrderRaw // wrong: rbf() is a native-order container
	cfg := &fakeConfig{order: order, doneAfter: 1}
	ser := &fakeSer{order: order}
	if err := Reload(cfg, ser, rbf(70000), o); err == nil {
		t.Fatal("Reload honoured a BitOrder that contradicts the container")
	}
	if ser.configured != 0 || cfg.pulses != 0 {
		t.Fatalf("contradictory override disturbed the fabric: configured=%d pulses=%d", ser.configured, cfg.pulses)
	}

	// ... and ForceBitOrder is the documented way through.
	o2, order2 := fastOpts()
	o2.BitOrder, o2.ForceBitOrder = BitOrderRaw, true
	cfg2 := &fakeConfig{order: order2, doneAfter: 1}
	ser2 := &fakeSer{order: order2}
	in := rbf(70000)
	if err := Reload(cfg2, ser2, in, o2); err != nil {
		t.Fatalf("ForceBitOrder did not override: %v", err)
	}
	if got := dataStreamed(ser2); !bytes.Equal(got, in) {
		t.Fatal("forced raw stream was bit-reversed anyway")
	}
}

// ─── full real files, when this workspace has them ───────────────────────────

// findFile returns the first candidate path that exists, relative to this
// package (app/internal/fpgaload). Absent files skip rather than fail: the table
// cases above already carry the real bytes.
func findFile(t *testing.T, candidates ...string) string {
	t.Helper()
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	t.Skipf("none of %v present in this workspace", candidates)
	return ""
}

func TestDetectOrderOnFullFiles(t *testing.T) {
	t.Run("this repo's own acq.rbf", func(t *testing.T) {
		// The exact artefact bitstream_embed.go embeds under -tags withbitstream.
		p := findFile(t,
			"../../../fpga/standard/acq.rbf",
			"acq.rbf",
		)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		got, err := DetectOrder(b)
		if err != nil {
			t.Fatalf("DetectOrder(%s): %v", p, err)
		}
		if got != OrderNative || !got.BitRev() {
			t.Fatalf("%s detected as %v (bitrev=%v), want native/bitrev=true", p, got, got.BitRev())
		}
	})

	t.Run("factory .rbf", func(t *testing.T) {
		p := findFile(t,
			"../../../../reveng-sds1102cml/firmware/sds1000_fpga.rbf",
			"../../../../sds1000cml/work/extracted/config_full/sds1000_fpga.rbf",
		)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		got, err := DetectOrder(b)
		if err != nil {
			t.Fatalf("DetectOrder(%s): %v", p, err)
		}
		if got != OrderPreReversed || got.BitRev() {
			t.Fatalf("%s detected as %v (bitrev=%v), want pre-reversed/bitrev=false", p, got, got.BitRev())
		}
		// Cross-check: bit-reversing the factory image must yield a container
		// detection calls native. Decided offline, no device involved.
		if got, err := DetectOrder(bitrevAll(b)); err != nil || got != OrderNative {
			t.Fatalf("DetectOrder(bitrev(factory)) = %v, %v; want native", got, err)
		}
	})

	// The embedded bitstream, when this build has one, must be readable by the
	// detector — otherwise Bringup would refuse at boot on real hardware.
	t.Run("embedded standard bitstream", func(t *testing.T) {
		b := Standard()
		if len(b) == 0 {
			t.Skip("built without -tags withbitstream")
		}
		got, err := DetectOrder(b)
		if err != nil {
			t.Fatalf("the embedded bitstream is not a readable container: %v", err)
		}
		t.Logf("embedded bitstream: %d bytes, %v, bitrev=%v", len(b), got, got.BitRev())
	})
}
