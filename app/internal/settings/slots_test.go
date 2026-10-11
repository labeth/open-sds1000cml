// ENGMODEL-OWNER-UNIT: FU-APP-SETTINGS
package settings

import "testing"

// A saved slot recalls the setup it stored; an empty slot errors (bug hunt
// 2026-10-10: *SAV/*RCL were accepted and did nothing).
// TRLC-LINKS: REQ-SDS-072, REQ-SDS-089
func TestSlotsSaveRecall(t *testing.T) {
	cur := Settings{TdivS: 200e-6}
	var applied Settings
	s := Slots{Dir: t.TempDir(), Collect: func() Settings { return cur }, Apply: func(v Settings) { applied = v }}
	if err := s.SaveSetup(1); err != nil {
		t.Fatal(err)
	}
	cur.TdivS = 1e-3
	if err := s.RecallSetup(1); err != nil || applied.TdivS != 200e-6 {
		t.Fatalf("recall: %v, tdiv %g", err, applied.TdivS)
	}
	if err := s.RecallSetup(2); err == nil {
		t.Fatal("recalled an empty slot")
	}
}
