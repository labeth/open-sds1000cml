// Cross-check the owned register map against the selectors the FABRIC actually
// decodes by hand.
//
// The codegen exists so the selector decode can never drift. It only delivers
// that for selectors it KNOWS about — and acq.v hand-decodes nine CS1 selectors
// that appear nowhere in the schema (the serial-decode config/result ports and
// the front-panel read window). Nothing connected the two sides, so the schema
// believed it owned selector space that was already spoken for. These tests are
// that connection: they read acq.v and fail if a schema selector and a
// hand-decoded selector ever land on each other unreviewed.
//
// Read-only: this test never edits the RTL. If acq.v is not present (the codegen
// module vendored on its own), it skips rather than fails.

package ifacedef_test

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"open-sds/codegen/ifacedef"
	"open-sds/codegen/schema"
)

// acqPath is the fabric top-level, relative to this package's directory.
const acqPath = "../../fpga/standard/acq.v"

// reviewedOverlaps are selectors that BOTH the schema and hand RTL claim, each
// with the reason it is tolerated. Anything not listed here is a defect.
var reviewedOverlaps = map[uint16]string{
	// acq.v's front-panel read window (PANEL_SEL0 = 0x64) covers ENV_COLS, so the
	// ENV_COLS READBACK is intentionally dead (the write path is unaffected).
	// acq.v records that unshadowing it needs a schema relocation, which moves the
	// build-ID and therefore has to ride a re-flash — it is not a desk change.
	0x64: "front-panel read window shadows the ENV_COLS readback (write path intact)",
}

var (
	reSelCmp   = regexp.MustCompile(`(?:wr_sel|rd_sel|sel_q2_masked)\s*==\s*8'h([0-9a-fA-F]{2})`)
	rePanelSel = regexp.MustCompile(`PANEL_SEL[01]\s*=\s*8'h([0-9a-fA-F]{2})`)
)

// rtlSelectors returns every CS1 selector acq.v compares against a literal.
func rtlSelectors(t *testing.T) map[uint16]bool {
	t.Helper()
	src, err := os.ReadFile(acqPath)
	if err != nil {
		t.Skipf("acq.v not readable (%v) — codegen checked out without the fabric tree", err)
	}
	out := map[uint16]bool{}
	for _, re := range []*regexp.Regexp{reSelCmp, rePanelSel} {
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			v, err := strconv.ParseUint(m[1], 16, 16)
			if err != nil {
				t.Fatalf("unparsable selector literal %q", m[1])
			}
			out[uint16(v)] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("found no hand-decoded selector literals in acq.v — the scan regexes have gone stale")
	}
	return out
}

func cs1Regs() map[uint16]string {
	out := map[uint16]string{}
	for _, r := range ifacedef.Standard().AllRegs() {
		if r.Plane == schema.CS1 {
			out[r.Sel] = r.Name
		}
	}
	return out
}

// A hand-decoded selector that also names a schema register is two decoders on
// one address: at best one of them is dead, at worst both drive. Every such
// overlap must be listed and justified above.
func TestNoUnreviewedRTLSelectorOverlap(t *testing.T) {
	regs, rtl := cs1Regs(), rtlSelectors(t)
	for sel := range rtl {
		name, isReg := regs[sel]
		if !isReg {
			continue
		}
		if why, ok := reviewedOverlaps[sel]; !ok {
			t.Errorf("acq.v hand-decodes CS1 0x%02x, which the schema assigns to %s — "+
				"two decoders on one selector. Move one, or add it to reviewedOverlaps with a reason.", sel, name)
		} else {
			t.Logf("reviewed overlap 0x%02x (%s): %s", sel, name, why)
		}
	}
	// And the reverse: a reviewed overlap that is no longer an overlap is stale
	// documentation, which is how the last one survived.
	for sel := range reviewedOverlaps {
		if !rtl[sel] {
			t.Errorf("reviewedOverlaps lists 0x%02x but acq.v no longer decodes it — delete the entry", sel)
		}
	}
}

// A schema read alias only works if the fabric agrees on BOTH halves: regmux.vh
// returns the aliased register's data, and hand RTL pops the port. If acq.v stops
// decoding the alias, reads at that selector return data without advancing —
// a silently repeating record, not an error.
func TestSchemaAliasesAreDecodedByRTL(t *testing.T) {
	rtl := rtlSelectors(t)
	found := 0
	for _, r := range ifacedef.Standard().AllRegs() {
		if r.Plane != schema.CS1 || r.Sem&schema.SemAutoIncPort == 0 {
			continue
		}
		for _, al := range r.ReadAliases {
			found++
			if !rtl[al.Sel] {
				t.Errorf("schema aliases CS1 0x%02x onto the auto-inc port %s, but acq.v does not decode "+
					"0x%02x — reads there would return data WITHOUT popping", al.Sel, r.Name, al.Sel)
			}
		}
	}
	if found == 0 {
		t.Error("no auto-inc read alias found; the 0x00 -> BURST alias should be one")
	}
}

// A census of CS1 decodable selector space, so the campaign knows what is left
// before it designs a register it cannot address. Only 32 selectors are decodable
// at all (multiples of 4 below 0x80 — see schema.Validate). This fails only on
// over-subscription; a full map is reported, loudly, not failed.
func TestCS1SelectorCensus(t *testing.T) {
	regs, rtl := cs1Regs(), rtlSelectors(t)
	var free []string
	used := 0
	for sel := uint16(0); sel < 0x80; sel += 4 {
		_, isReg := regs[sel]
		if isReg || rtl[sel] {
			used++
			continue
		}
		free = append(free, fmt.Sprintf("0x%02x", sel))
	}
	sort.Strings(free)
	t.Logf("CS1 decodable selectors: 32 total, %d claimed (%d schema registers + hand-decoded RTL), %d free: %v",
		used, len(regs), len(free), free)
	if used > 32 {
		t.Fatalf("CS1 selector space over-subscribed: %d claims for 32 decodable selectors", used)
	}
	if len(free) == 0 {
		t.Log("WARNING: CS1 decodable selector space is FULL — a new register needs a wider decode " +
			"(A8/A2/A1 are unusable) or must reclaim a hand-decoded selector.")
	}
}
