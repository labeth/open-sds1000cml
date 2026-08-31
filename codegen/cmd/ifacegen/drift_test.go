// The drift gate, as a `go test`.
//
// `make drift` (and `ifacegen -check`) already compare the checked-in generated
// files against a fresh in-memory regeneration, but that lane only runs where
// someone remembers to run it: a plain `go test ./...` — what most people run,
// and what the review bots run — was blind to a stale artifact. That blindness is
// what let a HAND EDIT live inside a generated file: the CS1 `0x00` -> `BURST`
// alias sat in regmux.vh for weeks while `make generate` stood ready to delete it
// and silently change bus behaviour.
//
// This test makes regeneration-equality a first-class assertion of the suite.
// Ported from the proving-ground codegen (open-sds1000cml-fpga), widened to all
// four owned artifacts, and driven off the same targets() the tool uses so it
// cannot fall behind when an artifact is added.

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"open-sds/codegen/ifacedef"
)

// testRoot is the codegen module root as seen from this package's directory
// (codegen/cmd/ifacegen), so the module-root-relative default output dirs resolve.
const testRoot = "../.."

// TestGenNotStale regenerates every artifact from ifacedef.Standard() in memory
// and asserts it byte-equals the checked-in file. Same contract as `make drift`,
// always exercised by `go test ./...`.
func TestGenNotStale(t *testing.T) {
	iface := ifacedef.Standard()
	if errs := iface.Validate(); len(errs) > 0 {
		t.Fatalf("Standard schema invalid: %v", errs)
	}

	tgts, err := targets(iface,
		filepath.Join(testRoot, DefaultGoDir),
		filepath.Join(testRoot, DefaultVhDir),
		filepath.Join(testRoot, DefaultDocDir))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(tgts) != 4 {
		t.Fatalf("expected 4 generated artifacts, got %d", len(tgts))
	}

	for _, tg := range tgts {
		got, err := os.ReadFile(tg.path)
		if err != nil {
			t.Errorf("read checked-in %s: %v (run `make -C codegen generate`)", tg.path, err)
			continue
		}
		if string(got) == tg.content {
			continue
		}
		t.Errorf("DRIFT: checked-in %s is stale — run `make -C codegen generate` "+
			"(checked in %d bytes, freshly emitted %d bytes)%s",
			tg.path, len(got), len(tg.content), firstDiff(string(got), tg.content))
	}
}

// firstDiff names the first differing line so the failure says WHAT drifted, not
// just that something did — the difference between a 5-second fix and a bisect.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for n := 0; n < len(g) || n < len(w); n++ {
		var gl, wl string
		if n < len(g) {
			gl = g[n]
		}
		if n < len(w) {
			wl = w[n]
		}
		if gl != wl {
			return "\n  first difference at line " + strconv.Itoa(n+1) +
				"\n    checked in: " + trunc(gl) +
				"\n    emitted:    " + trunc(wl)
		}
	}
	return ""
}

func trunc(s string) string {
	const max = 120
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
