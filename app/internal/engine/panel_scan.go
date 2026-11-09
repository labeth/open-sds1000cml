// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import "github.com/labeth/open-sds1000cml/app/internal/sramcapture"

// The panel scan is the board's (acq.Board.ReadMatrix); the engine passes
// its callers through until they hold the board.

// SetPanelScan stores the front-panel scan settings and applies them.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) SetPanelScan(p sramcapture.PanelConfig) error { return e.hw().SetPanelScan(p) }

// applyPanelScan writes the scan settings again after an image load.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) applyPanelScan() error { return e.hw().ApplyPanelScan() }

// PanelScan reads the last panel frame and its counters.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) PanelScan() (sramcapture.PanelFrame, error) { return e.hw().PanelScan() }

// MatrixSynthesized reports that ReadMatrix builds knob state from counts.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) MatrixSynthesized() bool { return e.hw().MatrixSynthesized() }

// ReadMatrix is the panel's key-matrix request, rebuilt from the scan.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) ReadMatrix() ([5]uint16, bool) { return e.hw().ReadMatrix() }
