// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"open-sds/app/internal/testenv"
)

// TestJSLayerParity runs every layer of the web page (layers.js) and of the
// scope on the same records, the curated ones and seeded random ones: the
// same packets listed with the same notes, the same flagged, and the same
// layers offered per bus. Skips without node (fails under
// CI_REQUIRE_BROWSER=1).
// TRLC-LINKS: REQ-SDS-241, REQ-SDS-242, REQ-SDS-243, REQ-SDS-244, REQ-SDS-245, REQ-SDS-246
func TestJSLayerParity(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")
	type jp struct {
		Bytes    []int  `json:"bytes"`
		ByteErr  []bool `json:"byteErr"`
		Addr     int    `json:"addr"`
		HasAddr  bool   `json:"hasAddr"`
		Read     bool   `json:"read"`
		Complete bool   `json:"complete"`
		Err      string `json:"err"`
	}
	type jv struct {
		ID      string `json:"id"`
		Proto   string `json:"proto"`
		Addr    int    `json:"addr"`
		Dir     int    `json:"dir"`
		Packets []jp   `json:"packets"`
	}
	vs := append(layerCurated(), layerRandom(1)...)
	vs = append(vs, layerRandom(2)...)
	var in []jv
	for _, v := range vs {
		x := jv{ID: v.ID, Proto: v.Proto, Addr: v.Args.Addr, Dir: v.Args.Dir}
		for _, p := range v.Packets {
			b := make([]int, len(p.Bytes))
			for i, c := range p.Bytes {
				b[i] = int(c)
			}
			e := append([]bool{}, p.ByteErr...)
			x.Packets = append(x.Packets, jp{Bytes: b, ByteErr: e, Addr: p.Addr, HasAddr: p.HasAddr, Read: p.Read, Complete: p.Complete, Err: p.Err})
		}
		in = append(in, x)
	}
	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	vf := filepath.Join(t.TempDir(), "vectors.json")
	if err := os.WriteFile(vf, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join("testdata", "layerparity.cjs"), vf, filepath.Join("..", "web"))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("layers.js run: %v", err)
	}
	var js []struct {
		Notes  []string `json:"notes"`
		Errs   []bool   `json:"errs"`
		N      int      `json:"n"`
		Layers []string `json:"layers"`
	}
	if err := json.Unmarshal(out, &js); err != nil {
		t.Fatal(err)
	}
	if len(js) != len(vs) {
		t.Fatalf("layers.js answered %d of %d records", len(js), len(vs))
	}
	bad := 0
	for i, v := range vs {
		ps := ApplyLayer(v.ID, v.Proto, clonePackets(v.Packets), v.Args)
		w := js[i]
		var ids []string
		for _, l := range LayersOn(v.Proto) {
			ids = append(ids, l.ID)
		}
		if len(ids) != len(w.Layers) {
			t.Errorf("%s: layers on %s: Go %v, JS %v", v.Name, v.Proto, ids, w.Layers)
		}
		if len(ps) != w.N {
			t.Errorf("%s: Go lists %d packets, JS %d", v.Name, len(ps), w.N)
			continue
		}
		for k, p := range ps {
			if p.Note != w.Notes[k] || p.NoteErr != w.Errs[k] {
				bad++
				if bad <= 40 {
					t.Errorf("%s packet %d (% X):\n  Go %q %v\n  JS %q %v", v.Name, k, p.Bytes, p.Note, p.NoteErr, w.Notes[k], w.Errs[k])
				}
			}
		}
	}
	t.Logf("%d records, %d differing notes", len(vs), bad)
}

// clonePackets copies packets (their byte slices too), as a layer writes notes.
// TRLC-LINKS: REQ-SDS-241
func clonePackets(ps []Packet) []Packet {
	out := make([]Packet, len(ps))
	for i, p := range ps {
		p.Bytes = append([]byte(nil), p.Bytes...)
		p.ByteErr = append([]bool(nil), p.ByteErr...)
		out[i] = p
	}
	return out
}
