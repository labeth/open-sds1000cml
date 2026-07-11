package panel

import (
	"testing"

	"open-sds/app/internal/engine"
)

// fireEng: AcqLog reports SawTrig only while the committed level code sits
// inside [fireLo, fireHi] — a comparator with a band the volts fit misses.
type fireEng struct {
	fakeEng
	level          uint16
	fireLo, fireHi uint16
}

func (f *fireEng) SetTrigLevelCode(c uint16) uint16 { f.level = c; return c }
func (f *fireEng) AcqLog(n int) ([]engine.AcqSample, float64) {
	fired := f.level >= f.fireLo && f.level <= f.fireHi
	return []engine.AcqSample{{SawTrig: fired}, {SawTrig: fired}, {SawTrig: fired}}, 0
}

// The computed code misses the band → the scan must land the level inside it.
func TestAutosetVerifyTrigLevelScansIntoBand(t *testing.T) {
	fe := &fireEng{fireLo: 30200, fireHi: 33400}
	c := &Controller{eng: fe}
	fe.level = 29000 // the (wrong) computed commit
	stop := make(chan struct{})
	if !c.verifyTrigLevel(stop, 29000) {
		t.Fatal("verify reported cancelled")
	}
	if fe.level < fe.fireLo || fe.level > fe.fireHi {
		t.Fatalf("scan left level %d outside firing band [%d,%d]", fe.level, fe.fireLo, fe.fireHi)
	}
}

// No band fires anywhere (quiet input) → the computed code must be kept.
func TestAutosetVerifyTrigLevelKeepsComputedWhenNothingFires(t *testing.T) {
	fe := &fireEng{fireLo: 1, fireHi: 2} // outside the DAC range → never fires
	c := &Controller{eng: fe}
	fe.level = 31000
	stop := make(chan struct{})
	if !c.verifyTrigLevel(stop, 31000) {
		t.Fatal("verify reported cancelled")
	}
	if fe.level != 31000 {
		t.Fatalf("quiet input must keep the computed code, got %d", fe.level)
	}
}
