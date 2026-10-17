// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"

	"open-sds/app/internal/decode"
)

// loadScopeCapture reads a scope record of the decode testdata (sample
// interval and both channels' codes).
// TRLC-LINKS: REQ-SDS-214
func loadScopeCapture(t *testing.T, name string) (c1, c2 []uint8, sampleS float64) {
	t.Helper()
	f, err := os.Open("../decode/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		SampleS float64 `json:"sample_s"`
		C1, C2  []int
	}
	if err := json.NewDecoder(z).Decode(&v); err != nil {
		t.Fatal(err)
	}
	c1, c2 = make([]uint8, len(v.C1)), make([]uint8, len(v.C2))
	for i := range v.C1 {
		c1[i], c2[i] = uint8(v.C1[i]), uint8(v.C2[i])
	}
	return c1, c2, v.SampleS
}

// The bench buses found on the decimated records the BUS search reads (the
// look on screen, or the slow look's search records, LeaseSearchView) stand
// there: the records resolve them as decimatedStands asks, so the search
// names them where it looks and goes no further. A 1-Wire slot's 15 us at
// 500us/div and a Wiegand pulse at 20ms/div span six samples, enough for
// buses found by their checks or handshake.
// TRLC-LINKS: REQ-SDS-214
func TestScopeCapturesStandDecimated(t *testing.T) {
	for _, c := range []struct{ file, proto string }{
		{"scope_lin_search_5ms.json.gz", "lin"},
		{"scope_modbus_search_5ms.json.gz", "modbus"},
		{"scope_j1850_samples_500us.json.gz", "j1850"},
		{"scope_onewire_samples_500us.json.gz", "onewire"},
		{"scope_onewire_uartlike_500us.json.gz", "onewire"},
		{"scope_dht_search_5ms.json.gz", "dht"},
		{"scope_cec_search_20ms.json.gz", "cec"},
		{"scope_irnec_search_20ms.json.gz", "irnec"},
		{"scope_wiegand_search_20ms.json.gz", "wiegand"},
		{"scope_wiegand_samples_5ms.json.gz", "wiegand"},
	} {
		c1, c2, sampleS := loadScopeCapture(t, c.file)
		r, _ := decode.AutodetectFast(c1, c2, sampleS, "hex")
		if r.Proto != c.proto || !decimatedStands(r) {
			t.Errorf("%s: %q, %.1f samples its shortest feature: stands %v", c.file, r.Proto, decode.FeatureSamples(r), decimatedStands(r))
		}
	}
}
