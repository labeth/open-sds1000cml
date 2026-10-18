// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

// Oracle cross-checks of the motion decoders against sigrok's graycode,
// stepper_motor and pwm decoders (oracle_test.go).

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func TestOracleQuadrature(t *testing.T) {
	const sr = 1000000
	l := newTwoLine(sr)
	l.add(0, 0, 500e-6)
	var moves []int
	for i := 0; i < 12; i++ {
		moves = append(moves, 1)
	}
	for i := 0; i < 5; i++ {
		moves = append(moves, -1)
	}
	moves = append(moves, 1, -1, 1, 1)
	quadMoves(l, moves, 100e-6, 1e-3)
	quadMoves(l, []int{-1, -1, -1}, 60e-6, 500e-6)
	r := DecodeQuadrature(l.a.codes(), l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	anns := sigrokDecode(t, sr, []string{"A", "B"}, [][]byte{l.a.bits, l.b.bits}, "graycode:d0=A:d1=B", "graycode=count")
	// sigrok annotates each count from the edge that set it to the next
	// change: a unit's count is the one that starts at its edge
	checked := 0
	for _, sp := range r.Spans {
		if sp.Kind != "data" {
			continue
		}
		want := ""
		for _, a := range anns {
			if d := a.I0 - sp.I0; d >= -1 && d <= 1 {
				want = a.Text
			}
		}
		if want == "" {
			continue // the last count: sigrok shows a value once it changes
		}
		if got := sp.Text[1:]; got != want {
			t.Fatalf("quadrature unit at %d: repo count %s, sigrok %s", sp.I0, got, want)
		}
		checked++
	}
	if checked < len(moves) {
		t.Fatalf("quadrature: %d units checked of %d moves", checked, len(moves))
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func TestOracleStepper(t *testing.T) {
	const sr = 1000000
	l := newTwoLine(sr)
	l.add(0, 1, 300e-6)
	dirs := []byte{1, 1, 1, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 1, 1}
	stepperMoves(l, dirs, 200e-6, 2e-3)
	stepperMoves(l, []byte{1, 1, 1, 1}, 100e-6, 1e-3)
	r := DecodeStepper(l.a.codes(), l.b.codes(), 1.0/sr, ClockedCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" {
			repo = append(repo, sp.Text[1:])
		}
	}
	anns := sigrokDecode(t, sr, []string{"STEP", "DIR"}, [][]byte{l.a.bits, l.b.bits}, "stepper_motor:step=STEP:dir=DIR", "stepper_motor=position")
	var orc []string
	for _, a := range sortAnns(anns) {
		orc = append(orc, strings.Fields(a.Text)[0])
	}
	// sigrok shows each position from its step to the next one: all but the last
	if fmt.Sprint(repo[:len(repo)-1]) != fmt.Sprint(orc) {
		t.Fatalf("stepper differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-250
func TestOraclePWM(t *testing.T) {
	const sr = 2000000
	w := newTimeline(sr)
	w.add(0, 30e-6)
	pwmWave(w, 100e-6, []float64{0.25, 0.5, 0.75, 0.1, 0.9, 0.333, 0.5, 0.05, 0.95, 0.5})
	pwmWave(w, 37e-6, []float64{0.2, 0.4, 0.6, 0.8})
	r := DecodePWM(w.codes(), 1.0/sr, LineCfg{})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var repo []string
	for _, sp := range r.Spans {
		if sp.Kind == "data" {
			repo = append(repo, sp.Text)
		}
	}
	anns := sigrokDecode(t, sr, []string{"D"}, [][]byte{w.bits}, "pwm:data=D", "pwm=duty-cycle")
	var orc []string
	for _, a := range sortAnns(anns) {
		p, err := strconv.ParseFloat(strings.TrimSuffix(a.Text, "%"), 64)
		if err != nil {
			t.Fatalf("duty %q", a.Text)
		}
		d10 := int(math.Floor(p*10 + 0.5))
		orc = append(orc, fmt.Sprintf("%d.%d%%", d10/10, d10%10))
	}
	if fmt.Sprint(repo) != fmt.Sprint(orc) {
		t.Fatalf("PWM differs:\n repo   %v\n sigrok %v", repo, orc)
	}
}
