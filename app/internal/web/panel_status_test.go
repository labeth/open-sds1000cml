// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/panel"
	"testing"
)

// TRLC-LINKS: REQ-SDS-164
type panelStatusFixture struct {
	view   panel.MenuView
	writes int
}

// TRLC-LINKS: REQ-SDS-162
func (p *panelStatusFixture) InjectButton(string) bool { p.writes++; return true }

// TRLC-LINKS: REQ-SDS-162
func (p *panelStatusFixture) InjectKnob(string, int, int) bool { p.writes++; return true }

// TRLC-LINKS: REQ-SDS-164
func (p *panelStatusFixture) MenuView() panel.MenuView { return p.view }

// TRLC-LINKS: REQ-SDS-164
func TestStatusReportsCurrentLCDPanelSnapshotWithoutControlWrites(t *testing.T) {
	p := &panelStatusFixture{view: panel.MenuView{Open: true, Title: "CURSOR", CurOn: true, CurX: [2]float64{0.2, 0.8}, Items: []panel.MenuItem{{Label: "Cursors", Value: "On"}}}}
	s := New(&fakeScope{}, nil, p, nil)
	got := getStatus(t, s)["panel"].(map[string]any)
	if got["Title"] != "CURSOR" || got["CurOn"] != true || got["CurX"].([]any)[0] != 0.2 {
		t.Fatalf("missing LCD state: %v", got)
	}
	p.view.Title = "DISPLAY"
	if getStatus(t, s)["panel"].(map[string]any)["Title"] != "DISPLAY" {
		t.Fatal("panel state was cached")
	}
	if p.writes != 0 {
		t.Fatal("status read injected a control event")
	}
	if _, exists := getStatus(t, New(&fakeScope{}, nil, nil, nil))["panel"]; exists {
		t.Fatal("absent panel should be omitted")
	}
}
