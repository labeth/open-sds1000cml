// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package board

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/labeth/open-dso-core/supervisor/config"
)

// TRLC-LINKS: REQ-SDS-114
func benchBoard(b *testing.B) *Board {
	b.Helper()
	dir := b.TempDir()
	b.Setenv("OTA_DIR", dir+"/ota")
	b.Setenv("OTA_SLOT_ROOT", dir+"/slots")
	b.Setenv("OTA_WD_DEV", dir+"/watchdog")
	b.Setenv("OTA_GPMC", dir+"/Gpmc")
	b.Setenv("OTA_FPGA_KEY", dir+"/fpga_key")
	return New(config.Load(), LoadConfig())
}

// TRLC-LINKS: REQ-SDS-114
func BenchmarkProbe(b *testing.B) {
	bd := benchBoard(b)
	for b.Loop() {
		bd.probe(false, false)
	}
}

// The takeover's search for the vendor firmware among the GPMC holders,
// here a child of ours holding the node.
// TRLC-LINKS: REQ-SDS-027
func BenchmarkFactoryCandidates(b *testing.B) {
	bd := benchBoard(b)
	node := filepath.Join(b.TempDir(), "Gpmc")
	if err := os.WriteFile(node, nil, 0o644); err != nil {
		b.Fatal(err)
	}
	bd.bc.GpmcDev = node
	f, _ := os.Open(node)
	defer func() { _ = f.Close() }()
	child := exec.CommandContext(b.Context(), "sleep", "30")
	child.ExtraFiles = []*os.File{f}
	if err := child.Start(); err != nil {
		b.Skip(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	for b.Loop() {
		if c := bd.factoryCandidates(); len(c) != 0 {
			b.Fatal(c)
		}
	}
}
