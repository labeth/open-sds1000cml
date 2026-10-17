package buildinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestThirdPartyLicensesCoverLinkedModules checks that THIRD_PARTY_LICENSES.txt,
// which ships with every release, carries the license of each module the
// agent, otactl and the emergency backstop link: a dependency added or
// bumped must have its license added there.
// TRLC-LINKS: REQ-SDS-178
func TestThirdPartyLicensesCoverLinkedModules(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	text, err := os.ReadFile(filepath.Join("..", "..", "..", "THIRD_PARTY_LICENSES.txt"))
	if err != nil {
		t.Fatalf("THIRD_PARTY_LICENSES.txt: %v", err)
	}
	cmd := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{end}}{{end}}",
		"./cmd/otactl", "./cmd/agent", "./cmd/stubapp")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	headers := map[string]bool{}
	for _, line := range strings.Split(string(text), "\n") {
		headers[strings.TrimSpace(line)] = true
	}
	seen := map[string]bool{}
	for _, m := range strings.Fields(string(out)) {
		if seen[m] {
			continue
		}
		seen[m] = true
		if !headers[m] {
			t.Errorf("THIRD_PARTY_LICENSES.txt has no section for %s", m)
		}
	}
	if len(seen) == 0 {
		t.Fatal("go list named no modules")
	}
	if !strings.Contains(string(text), "The Go programming language") {
		t.Error("THIRD_PARTY_LICENSES.txt lacks the Go standard library's license")
	}
}
