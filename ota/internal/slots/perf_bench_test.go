// ENGMODEL-OWNER-UNIT: FU-OTA-SLOTS
package slots

import (
	"os"
	"path/filepath"
	"testing"
)

// status reports each slot's sha256; the device's binaries are ~15 MB.
// TRLC-LINKS: REQ-SDS-087
func BenchmarkStatus(b *testing.B) {
	s := New(b.TempDir())
	s.Init()
	for _, slot := range []string{SlotA, SlotB} {
		os.WriteFile(filepath.Join(s.SlotDir(slot), BinName), make([]byte, 4<<20), 0o755)
	}
	for b.Loop() {
		if st := s.Status(); len(st.Binaries) != 2 {
			b.Fatal(st)
		}
	}
}
