// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"os"
	"testing"

	"open-sds/app/internal/analysis"
)

// TestLayerTablesJS keeps layer_tables.js equal to the scope's layer tables,
// so the web page's layers name every register and command as the LCD does.
// LAYER_TABLES_WRITE=1 writes the file from them.
// TRLC-LINKS: REQ-SDS-241
func TestLayerTablesJS(t *testing.T) {
	want := analysis.LayerTablesJS()
	if os.Getenv("LAYER_TABLES_WRITE") == "1" {
		if err := os.WriteFile("layer_tables.js", []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile("layer_tables.js")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatal("layer_tables.js differs from analysis.LayerTablesJS(): run LAYER_TABLES_WRITE=1 go test -run TestLayerTablesJS ./internal/web/")
	}
}
