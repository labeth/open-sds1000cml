// ENGMODEL-OWNER-UNIT: FU-APP-ANALOG

package analog

import (
	"syscall"
	"testing"
	"unsafe"
)

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
