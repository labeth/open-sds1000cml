package main

import (
	"errors"
	"open-sds/app/internal/bus"
	"testing"
)

type timingProbe struct {
	current              bus.CS1Timing
	applyErr, restoreErr error
	restored             bool
}

func (p *timingProbe) Read() (bus.CS1Timing, error) { return p.current, nil }
func (p *timingProbe) Apply(v bus.CS1Timing) error  { p.current = v; return p.applyErr }
func (p *timingProbe) Restore(v bus.CS1Timing) error {
	p.restored = true
	p.current = v
	return p.restoreErr
}

func TestSRAMTimingQualificationAndRollback(t *testing.T) {
	for _, mode := range []string{"fast", "counter-fails", "apply-fails", "restore-fails", "both-fail"} {
		t.Run(mode, func(t *testing.T) {
			p := &timingProbe{current: bus.FactoryCS1Timing}
			fail := errors.New("injected failure")
			if mode == "apply-fails" {
				p.applyErr = fail
			}
			if mode == "restore-fails" {
				p.restoreErr = fail
			}
			checks := 0
			fast, err := applySRAMReadTiming(p, func() error {
				checks++
				if mode != "fast" && (!p.restored || mode == "both-fail") {
					return fail
				}
				return nil
			})
			if mode == "fast" {
				if !fast || err != nil || checks != 1 || p.restored {
					t.Fatalf("fast=%v err=%v checks=%d", fast, err, checks)
				}
				if p.current.RdCycle() != 10 || p.current.RdAccess() != 8 || p.current.Gap() != 5 {
					t.Fatal(p.current)
				}
				if p.current.WrCycle() != bus.FactoryCS1Timing.WrCycle() || p.current.WEOff() != bus.FactoryCS1Timing.WEOff() {
					t.Fatal("changed write timing")
				}
			} else {
				if fast || !p.restored || p.current != bus.FactoryCS1Timing {
					t.Fatal("did not restore baseline")
				}
				wantErr := mode == "restore-fails" || mode == "both-fail"
				if (err != nil) != wantErr {
					t.Fatalf("unexpected error %v", err)
				}
				if mode == "counter-fails" && checks != 2 {
					t.Fatal("baseline not verified")
				}
			}
		})
	}
}
