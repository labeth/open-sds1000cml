// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-134
func TestKernelStreamOwnsGPMC(t *testing.T) {
	// Nil module file and invalid GPMC fd ensure guards precede device access.
	d := &Dev{fd: -1, kernelDMA: &kernelDMADrainer{streaming: true, bytes: make([]byte, 16)}}
	checks := map[string]func() error{
		"read":      func() error { _, e := d.Read(PlaneCS1, 0); return e },
		"write":     func() error { return d.RawWrite(17, 0) },
		"pop bytes": func() error { return d.PopBytesChecked(25, make([]byte, 8)) },
		"pop words": func() error { return d.PopWordsChecked(25, make([]uint16, 4)) },
	}
	for name, f := range checks {
		if e := f(); e == nil || !strings.Contains(e.Error(), "owns GPMC") {
			t.Errorf("%s: %v", name, e)
		}
	}
	if d.EnableEDMA(8192, nil) {
		t.Fatal("enabled competing EDMA owner")
	}
	if e := d.StartKernelStream(8, 1, 4096, 1); e == nil {
		t.Fatal("allowed second stream")
	}
}

// TRLC-LINKS: REQ-SDS-134
func TestKernelStreamReadRequiresStart(t *testing.T) {
	d := &Dev{kernelDMA: &kernelDMADrainer{}}
	if _, e := d.ReadKernelStream(make([]byte, 16400)); e == nil {
		t.Fatal("read before start")
	}
	if d.EnableEDMA(8192, nil) {
		t.Fatal("enabled EDMA alongside synchronous kernel DMA")
	}
}
