// ENGMODEL-OWNER-UNIT: FU-APP-BUILDINFO
package buildinfo

import "testing"

// TRLC-LINKS: REQ-SDS-174
func BenchmarkString(b *testing.B) {
	Version, Commit, Built = "v1.1.0-12-gabcdef0", "abcdef0", "2026-10-24T00:00:00Z"
	for b.Loop() {
		_ = String()
	}
}

// The string follows a change of the stamped values.
// TRLC-LINKS: REQ-SDS-174
func TestStringFollowsTheStamp(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "a"
	if s := String(); s != "a ("+Commit+", "+Built+")" {
		t.Fatal(s)
	}
	Version = "b"
	if s := String(); s != "b ("+Commit+", "+Built+")" {
		t.Fatal(s)
	}
}
