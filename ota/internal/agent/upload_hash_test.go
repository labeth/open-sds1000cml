// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package agent

import (
	"path/filepath"
	"testing"
)

// A chunk rewritten after the in-order hash passed it must not let the
// running hash vouch for bytes that are no longer in the file.
// TRLC-LINKS: REQ-SDS-106
func TestUploadRewrittenChunkHashesTheFile(t *testing.T) {
	a := testAgent(t)
	final := []byte("HELLO world payload")
	sum, _ := fileSHAOfBytes(final)
	for _, rewrite := range []bool{false, true} {
		dest := filepath.Join(t.TempDir(), "r.bin")
		id, err := a.newUpload(dest, int64(len(final)), sum, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		first := final[:5]
		if rewrite {
			first = []byte("hello")
		}
		a.writeUpload(id, 0, first)
		a.writeUpload(id, 5, final[5:])
		if rewrite {
			a.writeUpload(id, 0, final[:5])
		}
		if _, got, err := a.commitUpload(id); err != nil || got != sum {
			t.Fatalf("rewrite=%v: sum %s err %v, want %s", rewrite, got, err, sum)
		}
	}
}
