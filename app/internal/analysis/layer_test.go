// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLayerNotesGolden reads the curated records of every layer and holds
// the notes to testdata/layer_notes.golden: what each layer writes for each
// transaction, and which it flags. LAYER_GOLDEN_WRITE=1 writes the file.
// TRLC-LINKS: REQ-SDS-241, REQ-SDS-242, REQ-SDS-243, REQ-SDS-244, REQ-SDS-245, REQ-SDS-246, REQ-SDS-249
func TestLayerNotesGolden(t *testing.T) {
	var sb strings.Builder
	for _, v := range layerCurated() {
		ps := ApplyLayer(v.ID, v.Proto, clonePackets(v.Packets), v.Args)
		fmt.Fprintf(&sb, "== %s (%s on %s)\n", v.Name, v.ID, v.Proto)
		for i, p := range ps {
			flag := " "
			if p.NoteErr {
				flag = "!"
			}
			fmt.Fprintf(&sb, "%2d %s % X\n     %s\n", i, flag, p.Bytes, p.Note)
		}
	}
	got := sb.String()
	path := filepath.Join("testdata", "layer_notes.golden")
	if os.Getenv("LAYER_GOLDEN_WRITE") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("line %d differs:\n got  %q\n want %q\n(LAYER_GOLDEN_WRITE=1 rewrites %s)", i+1, gl[i], wl[i], path)
			}
		}
		t.Fatalf("%d lines, golden %d (LAYER_GOLDEN_WRITE=1 rewrites %s)", len(gl), len(wl), path)
	}
}

// TestLayersOfferedPerBus: each bus offers its layers and none of another's,
// none first; the order is the list's; unknown buses offer none only.
// TRLC-LINKS: REQ-SDS-241
func TestLayersOfferedPerBus(t *testing.T) {
	want := map[string]int{"spi": 22, "i2c": 18, "uart": 6, "onewire": 4, "mdio": 1, "usbls": 1, "usb": 1, "canfd": 0, "lin": 0}
	for bus, n := range want {
		ls := LayersOn(bus)
		if ls[0].ID != "" || len(ls)-1 != n {
			t.Errorf("%s: %d layers, want %d", bus, len(ls)-1, n)
		}
		for _, l := range ls[1:] {
			if l.Bus != busOf(bus) {
				t.Errorf("%s offers %s of %s", bus, l.ID, l.Bus)
			}
			if _, ok := layerImpls[l.ID]; !ok {
				t.Errorf("%s has no implementation", l.ID)
			}
		}
	}
	seen := map[string]bool{}
	for _, l := range Layers() {
		if seen[l.ID] {
			t.Errorf("layer %q listed twice", l.ID)
		}
		seen[l.ID] = true
		if l.Bus == "spi" && l.Line != "MOSI" && l.Line != "MISO" {
			t.Errorf("%s: SPI layer without its line", l.ID)
		}
	}
	// a layer of another bus writes no note
	ps := []Packet{mkSPI(0x03, 0, 0, 0, 1)}
	if out := ApplyLayer("eeprom24xx", "spi", ps, LayerArgs{Addr: -1}); out[0].Note != "" {
		t.Fatalf("an I2C layer on SPI: %q", out[0].Note)
	}
	if out := ApplyLayer("", "spi", ps, LayerArgs{Addr: -1}); out[0].Note != "" {
		t.Fatalf("no layer: %q", out[0].Note)
	}
	if out := ApplyLayer("nosuch", "spi", ps, LayerArgs{Addr: -1}); out[0].Note != "" {
		t.Fatalf("an unknown layer: %q", out[0].Note)
	}
}

// TestLayerFixed holds the integer formatting the two surfaces share:
// halves round away from zero, negatives keep their sign, a zero
// denominator writes "?".
// TRLC-LINKS: REQ-SDS-241
func TestLayerFixed(t *testing.T) {
	for _, c := range []struct {
		num, den int64
		places   int
		want     string
	}{{1, 8, 2, "0.13"}, {-1, 8, 2, "-0.13"}, {1, 3, 4, "0.3333"}, {2, 3, 0, "1"}, {-1, 1000, 2, "0.00"},
		{25, 2, 1, "12.5"}, {401, 16, 4, "25.0625"}, {-7, 2, 0, "-4"}, {5, 0, 2, "?"}, {5, -2, 1, "-2.5"}} {
		if got := fixed(c.num, c.den, c.places); got != c.want {
			t.Errorf("fixed(%d, %d, %d) = %q, want %q", c.num, c.den, c.places, got, c.want)
		}
	}
}
