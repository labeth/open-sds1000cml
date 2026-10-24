// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG
package analog

import (
	"math/rand"
	"syscall"
	"testing"
	"unsafe"
)

// benchRecord is a captured-looking record: a square wave with noise.
// TRLC-LINKS: REQ-SDS-096
func benchRecord(n int) []uint8 {
	r := rand.New(rand.NewSource(1))
	s := make([]uint8, n)
	for i := range s {
		v := 60
		if i/500%2 == 1 {
			v = 200
		}
		s[i] = uint8(v + r.Intn(9) - 4)
	}
	return s
}

// TRLC-LINKS: REQ-SDS-096
func BenchmarkRemoveDC(b *testing.B) {
	s := benchRecord(1 << 16)
	b.SetBytes(int64(len(s)))
	for b.Loop() {
		RemoveDC(s)
	}
}

// TRLC-LINKS: REQ-SDS-098
func BenchmarkTrigVolts(b *testing.B) {
	f := New(nil, nil, nil)
	var sum float64
	for b.Loop() {
		sum += f.TrigVolts(30000, 0)
	}
	_ = sum
}

// TRLC-LINKS: REQ-SDS-095
func BenchmarkSetZeroTrim(b *testing.B) {
	f := New(nil, nil, nil)
	t, err := DefaultZeroTrim()
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		f.SetZeroTrim(t)
	}
}

// WriteGain against a gain node that answers as the scope's does: each
// ioctl a syscall that succeeds (getpid stands in for the transfer).
// TRLC-LINKS: REQ-SDS-015
func BenchmarkWriteGain(b *testing.B) {
	calls := 0
	ioctlPtr = func(int, uintptr, unsafe.Pointer) error { calls++; syscall.Getpid(); return nil }
	defer func() { ioctlPtr = ioctlSys }()
	d := &Dev{relayFD: -1, gainFD: -1}
	for b.Loop() {
		_ = d.WriteGain(1, 2)
	}
	b.ReportMetric(float64(calls)/float64(b.N), "ioctls/op")
}

// RemoveDC and CoupleDisplayBy keep the clamp's result for every code and
// shift.
// TRLC-LINKS: REQ-SDS-096
func TestShiftedIsTheClamp(t *testing.T) {
	sig := make([]uint8, 256)
	for i := range sig {
		sig[i] = uint8(i)
	}
	for shift := -300; shift <= 300; shift++ {
		got := shifted(sig, shift)
		for v, g := range got {
			if want := uint8(min(max(v+shift, 0), 255)); g != want {
				t.Fatalf("shift %d code %d: %d, want %d", shift, v, g, want)
			}
		}
	}
	s := benchRecord(5000)
	got := RemoveDC(s)
	var sum int
	for _, v := range s {
		sum += int(v)
	}
	if mean := float64(sum) / float64(len(s)); got[0] != uint8(int(s[0])+128-int(mean+0.5)) {
		t.Fatalf("RemoveDC moved code %d to %d (mean %.2f)", s[0], got[0], mean)
	}
}
