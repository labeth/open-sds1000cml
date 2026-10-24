// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "testing"

// The table names every protocol the map does, and no other.
// TRLC-LINKS: REQ-SDS-018
func TestSoftProtoName(t *testing.T) {
	for id := -3; id < 200; id++ {
		want, wok := softProtoNames[id]
		got, ok := softProtoName(id)
		if got != want || ok != wok {
			t.Fatalf("id %d: %q %v, want %q %v", id, got, ok, want, wok)
		}
	}
}
