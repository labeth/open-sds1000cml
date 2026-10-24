// ENGMODEL-OWNER-UNIT: FU-OTA-FDINHERIT
package fdinherit

import (
	"os"
	"reflect"
	"testing"
)

// TRLC-LINKS: REQ-SDS-085
func TestHoldersOfEachMatchesHoldersOf(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "held")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	paths := []string{f.Name(), "/dev/null", "/nonexistent/x"}
	each := HoldersOfEach(paths...)
	for i, p := range paths {
		if one := HoldersOf(p); i != 1 && !reflect.DeepEqual(one, each[i]) {
			t.Errorf("%s: each %v, one %v", p, each[i], one)
		}
	}
	if len(each[0]) == 0 {
		t.Errorf("own temp file not found held")
	}
}

// TRLC-LINKS: REQ-SDS-085
func BenchmarkHolders3(b *testing.B) {
	for b.Loop() {
		HoldersOfEach("/dev/null", "/dev/zero", "/dev/urandom")
	}
}

// TRLC-LINKS: REQ-SDS-085
func BenchmarkHolders3Ref(b *testing.B) {
	for b.Loop() {
		HoldersOf("/dev/null")
		HoldersOf("/dev/zero")
		HoldersOf("/dev/urandom")
	}
}
