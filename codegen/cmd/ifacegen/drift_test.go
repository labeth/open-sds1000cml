// ENGMODEL-OWNER-UNIT: FU-CODEGEN-IFACEGEN
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-sds/codegen/ifacedef"
)

// The checked-in artifacts must equal a fresh render of the schema. This is the
// same gate as `make drift`, run as a Go test so `go test ./...` catches a stale
// artifact without make.
// TRLC-LINKS: REQ-SDS-160, REQ-SDS-031
func TestNoDrift(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	i := ifacedef.Default()
	d, files, err := SourceDigest(root, DefaultRTL)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no RTL sources under %s matched %q", root, DefaultRTL)
	}
	i.SourceDigest = d
	stale, err := Drift(i, root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) > 0 {
		t.Fatalf("generated artifacts are stale (run `make generate` in codegen/): %s", strings.Join(stale, ", "))
	}
}

// The RTL digest must follow the sources: a one-byte edit to a digested file moves it.
// TRLC-LINKS: REQ-SDS-160
func TestSourceDigestFollowsRTL(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "fpga", "default"), 0o755)
	f := filepath.Join(root, "fpga", "default", "x.v")
	os.WriteFile(f, []byte("module x; endmodule\n"), 0o644)
	a, files, err := SourceDigest(root, DefaultRTL)
	if err != nil || len(files) != 1 {
		t.Fatalf("digest: %v files %v", err, files)
	}
	os.WriteFile(f, []byte("module x; endmodule \n"), 0o644)
	b, _, _ := SourceDigest(root, DefaultRTL)
	if a == b {
		t.Fatal("digest did not move with the source")
	}
	c, _, _ := SourceDigest(root, DefaultRTL)
	if b != c {
		t.Fatal("digest is not deterministic")
	}
}

// Drift must report a modified artifact and must not touch the tree.
// TRLC-LINKS: REQ-SDS-160, REQ-SDS-031
func TestDriftDetectsChange(t *testing.T) {
	i := ifacedef.Default()
	fake := t.TempDir()
	ts, err := Targets(i)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range ts {
		p := filepath.Join(fake, x.Path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(x.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if stale, _ := Drift(i, fake, t.TempDir()); len(stale) != 0 {
		t.Fatalf("fresh tree reported stale: %v", stale)
	}
	p := filepath.Join(fake, "fpga/default/regmux.vh")
	os.WriteFile(p, []byte("// hand edit\n"), 0o644)
	stale, _ := Drift(i, fake, t.TempDir())
	if len(stale) != 1 || stale[0] != "fpga/default/regmux.vh" {
		t.Fatalf("stale = %v, want the hand-edited regmux.vh only", stale)
	}
	if b, _ := os.ReadFile(p); string(b) != "// hand edit\n" {
		t.Fatal("drift check overwrote the tree")
	}
	os.Remove(filepath.Join(fake, "app/internal/iface/iface.go"))
	if stale, _ = Drift(i, fake, t.TempDir()); len(stale) != 2 {
		t.Fatalf("missing artifact not reported: %v", stale)
	}
}
