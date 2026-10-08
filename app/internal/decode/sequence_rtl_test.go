// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"math/rand"
	"open-sds/app/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type seqElem struct {
	kind        uint8 // 0 any, 1 START, 2 DATA, 3 END, 4 ERROR
	mask, value uint32
}

type seqEvent struct {
	kind  uint8
	value uint32
	gap   int
}

// seqReference is the event-sequence trigger's contract
// (ADR-PROTOCOL-SEQUENCE-TRIGGER): the events after which a match fires.
// TRLC-LINKS: REQ-SDS-013
func seqReference(events []seqEvent, pattern []seqElem, qualify, endBad bool) []int {
	state := make([]bool, len(pattern))
	pending := false
	var fired []int
	for i, e := range events {
		for k := len(pattern) - 1; k >= 0; k-- {
			p := pattern[k]
			ok := (p.kind == 0 || p.kind == e.kind) && (e.value^p.value)&p.mask == 0
			state[k] = (k == 0 || state[k-1]) && ok
		}
		fire := false
		if pending {
			switch e.kind {
			case 3:
				pending, fire = false, !(endBad && e.value != 0)
			case 1, 4, 5:
				pending = false
			}
		}
		if state[len(pattern)-1] {
			if qualify {
				pending = true
			} else {
				fire = true
			}
		}
		if fire {
			fired = append(fired, i+1)
		}
	}
	return fired
}

// Compare the event-sequence RTL with its reference on random streams.
// TRLC-LINKS: REQ-SDS-013
func TestEventSequenceRTL(t *testing.T) {
	for _, tool := range []string{"iverilog", "vvp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("required RTL tool %s: %v", tool, err)
		}
	}
	dir := t.TempDir()
	rtl := testenv.RTLDir(t)
	image := filepath.Join(dir, "seq.vvp")
	if out, err := exec.Command("iverilog", "-g2012", "-s", "tb_event_sequence", "-o", image,
		filepath.Join(rtl, "event_sequence.v"), filepath.Join(rtl, "sim", "tb_event_sequence.v")).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := func(t *testing.T, events []seqEvent, pattern []seqElem, qualify, endBad bool) ([]int, int) {
		t.Helper()
		var ev, el strings.Builder
		for _, e := range events {
			fmt.Fprintf(&ev, "%x\n%x\n%x\n", e.kind, e.value, e.gap)
		}
		for _, p := range pattern {
			fmt.Fprintf(&el, "%x\n%x\n%x\n", p.kind, p.mask, p.value)
		}
		ep, pp := filepath.Join(dir, "events.mem"), filepath.Join(dir, "elements.mem")
		if err := os.WriteFile(ep, []byte(ev.String()), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pp, []byte(el.String()), 0600); err != nil {
			t.Fatal(err)
		}
		q, b := 0, 0
		if qualify {
			q = 1
		}
		if endBad {
			b = 1
		}
		out, err := exec.Command("vvp", image, "+events="+ep, fmt.Sprintf("+n=%d", len(events)), "+elements="+pp,
			fmt.Sprintf("+m=%d", len(pattern)), fmt.Sprintf("+qualify=%d", q), fmt.Sprintf("+endbad=%d", b)).CombinedOutput()
		if err != nil {
			t.Fatalf("simulate: %v\n%s", err, out)
		}
		var fired []int
		overflows := -1
		for _, line := range strings.Split(string(out), "\n") {
			var v int
			if _, err := fmt.Sscanf(line, "M %d", &v); err == nil {
				fired = append(fired, v)
			}
			fmt.Sscanf(line, "O %d", &overflows)
		}
		return fired, overflows
	}
	rng := rand.New(rand.NewSource(7))
	randomEvents := func(n, gap int) []seqEvent {
		ev := make([]seqEvent, n)
		for i := range ev {
			kind := uint8(2)
			switch r := rng.Intn(20); {
			case r == 0:
				kind = 1
			case r == 1:
				kind = 3
			case r == 2:
				kind = 4
			}
			ev[i] = seqEvent{kind, uint32(rng.Intn(4)) | uint32(rng.Intn(2))<<31, gap}
		}
		return ev
	}
	// Count reference matches so the random trials cannot pass vacuously.
	total := 0
	defer func() {
		if total < 60 {
			t.Errorf("random trials produced only %d matches", total)
		}
	}()
	for trial := 0; trial < 60; trial++ {
		events := randomEvents(300, 40)
		m := 1 + rng.Intn(32)
		if trial%3 == 0 {
			m = 1 + rng.Intn(4)
		}
		// Most patterns are cut from the stream so that long ones still match.
		start := rng.Intn(len(events) - m)
		pattern := make([]seqElem, m)
		for k := range pattern {
			e := events[start+k]
			pattern[k] = seqElem{e.kind, 0xffffffff, e.value}
			switch rng.Intn(6) {
			case 0:
				pattern[k].kind = 0
			case 1:
				pattern[k].mask = 0x3
			case 2:
				pattern[k].mask = 0
			}
		}
		qualify, endBad := trial%2 == 1, trial%4 == 3
		t.Run(fmt.Sprintf("trial%d/m%d/q%v", trial, m, qualify), func(t *testing.T) {
			want := seqReference(events, pattern, qualify, endBad)
			total += len(want)
			got, overflows := run(t, events, pattern, qualify, endBad)
			if !slices.Equal(got, want) || overflows != 0 {
				t.Fatalf("matches %v want %v (overflows %d)", got, want, overflows)
			}
		})
	}
	t.Run("error-trigger", func(t *testing.T) {
		events := []seqEvent{{1, 0, 40}, {2, 0x55, 40}, {4, 0x124, 40}, {1, 0, 40}, {2, 0x55, 40}, {3, 0x123, 40}}
		got, _ := run(t, events, []seqElem{{4, 0, 0}}, false, false)
		if !slices.Equal(got, []int{3}) {
			t.Fatalf("error trigger fired after %v", got)
		}
	})
	t.Run("qualified", func(t *testing.T) {
		// The pattern appears in a frame that ends in ERROR, then in one that ends in END.
		events := []seqEvent{{1, 0, 40}, {2, 0xaa, 40}, {2, 0x0f, 40}, {4, 1, 40}, {1, 0, 40}, {2, 0xaa, 40}, {2, 0x0f, 40}, {3, 0x123, 40}}
		got, _ := run(t, events, []seqElem{{2, 0xff, 0xaa}, {2, 0xff, 0x0f}}, true, false)
		if !slices.Equal(got, []int{8}) {
			t.Fatalf("qualified trigger fired after %v", got)
		}
		// A bad END (nonzero value, as Manchester and ARINC report) drops it.
		events[7].value = 1
		if got, _ := run(t, events, []seqElem{{2, 0xff, 0xaa}, {2, 0xff, 0x0f}}, true, true); len(got) != 0 {
			t.Fatalf("bad END fired %v", got)
		}
	})
	t.Run("burst", func(t *testing.T) {
		// Back-to-back events queue; the decisions match the reference.
		events := randomEvents(200, 0)
		pattern := []seqElem{{0, 0x3, events[50].value}, {0, 0x3, events[51].value}, {0, 0x3, events[52].value}}
		want := seqReference(events, pattern, false, false)
		got, overflows := run(t, events, pattern, false, false)
		if len(got) != len(want) || overflows != 0 {
			t.Fatalf("burst: %d matches want %d, overflows %d", len(got), len(want), overflows)
		}
	})
	t.Run("overflow", func(t *testing.T) {
		// 400 back-to-back events overrun the 256-entry queue: they are
		// counted, and no match spans the dropped events.
		events := make([]seqEvent, 400)
		for i := range events {
			events[i] = seqEvent{2, uint32(i & 0xff), 0}
		}
		got, overflows := run(t, events, []seqElem{{2, 0xff, 0x01}, {2, 0xff, 0x02}}, false, false)
		if overflows == 0 || len(got) > 2 {
			t.Fatalf("overflow: %d dropped, matches %v", overflows, got)
		}
	})
}
