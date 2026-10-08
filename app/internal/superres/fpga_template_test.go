// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import "testing"

// TRLC-LINKS: REQ-SDS-141
func TestBuildFPGATemplate(t *testing.T) {
	sig := make([]float64, 400)
	for i := range sig {
		sig[i] = 40
		if i >= 200 && i < 260 {
			sig[i] = 200
		}
	}
	sig[199] = 120 // crossing of 128 between 199 and 200
	tpl, err := BuildFPGATemplate(sig, 32e-9, 205, 128, false, 64, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Strict: 6 codes per point plus a quarter of each local slope; the step
	// 40 -> 120 -> 200 at points 15..17 adds (80+160+80)/4 (the fall is
	// outside this window).
	if tpl.Stride != 16 || tpl.Pre != 16*16 || len(tpl.Values) != 64 || tpl.Threshold != 64*6+(80+160+80)/4 {
		t.Fatalf("template %+v", tpl)
	}
	if tpl.Values[16] != 120 || tpl.Values[17] != 200 || tpl.Values[0] != 40 { // crossing 199.1 rounds to 199
		t.Fatalf("window misplaced: %v", tpl.Values[:20])
	}
	if loose, err := BuildFPGATemplate(sig, 32e-9, 205, 128, false, 64, FPGATemplateLoose); err != nil || loose.Threshold != 64*20 {
		t.Fatalf("loose threshold %d, %v", loose.Threshold, err)
	}
	if _, err := BuildFPGATemplate(sig, 2e-9, 205, 250, false, 64, 0); err == nil {
		t.Fatal("level without crossing accepted")
	}
	if _, err := BuildFPGATemplate(sig, 1e-6, 205, 128, false, 64, 0); err == nil {
		t.Fatal("too coarse frame accepted")
	}
	if _, err := BuildFPGATemplate(sig, 2e-9, 205, 128, false, 1000, 0); err == nil {
		t.Fatal("window outside the frame accepted")
	}
	if f, err := BuildFPGATemplate(sig, 2e-9, 250, 128, true, 16, 3); err != nil || f.Values[4] != 200 || f.Values[5] != 40 || f.Threshold != 48 {
		t.Fatalf("falling template %+v %v", f, err)
	}
}
