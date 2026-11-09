package engine

import (
	"testing"

	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// noPanelBus is a fabric without the panel scan block.
type noPanelBus struct{ *decodedTestBus }

// TRLC-LINKS: REQ-SDS-022
func (b noPanelBus) Read(cs uint8, s uint16) (uint16, error) {
	if s == 31 {
		return 0, nil
	}
	return b.decodedTestBus.Read(cs, s)
}

// A fabric without the panel scan is refused in plain words, with no empty
// cause appended.
// TRLC-LINKS: REQ-SDS-022
func TestPanelScanWithoutTheBlock(t *testing.T) {
	capture, err := sramcapture.New(noPanelBus{&decodedTestBus{}})
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{sram: capture}
	_, err = e.PanelScan()
	if err == nil || err.Error() != "engine: fabric lacks the panel scan" {
		t.Fatalf("PanelScan without the block: %v", err)
	}
}
