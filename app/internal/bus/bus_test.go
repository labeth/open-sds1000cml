package bus

import (
	"testing"

	"open-sds/app/internal/iface"
)

func TestEncode(t *testing.T) {
	// Verified encodings (spec 01 §1.2 / ota gpmc_test.go): raw selector,
	// little-endian, never pre-shifted.
	if got := encode(1, 0x12, 0); got != [6]byte{1, 0, 0x12, 0, 0, 0} {
		t.Fatalf("version read encode = %v", got)
	}
	if got := encode(3, 0x0134, 0xBEEF); got != [6]byte{3, 0, 0x34, 0x01, 0xEF, 0xBE} {
		t.Fatalf("cs3 write encode = %v", got)
	}
}

// TestWriteGuardIsSchemaDerived checks that the write guard is exactly
// iface.Writable — read-only registers (CONF_DONE, status/drain/channel ports)
// and unknown selectors are refused; RW/W registers pass. It is a clean-room
// restatement of the old forbidden-write test against the OWNED map.
func TestWriteGuardIsSchemaDerived(t *testing.T) {
	// Read-only / non-writable: must be refused.
	nonWritable := []struct {
		plane iface.Plane
		sel   uint16
	}{
		{iface.CS3, iface.SelCONF_DONE}, // config-status / nCONFIG — a write collapses the engine
		{iface.CS1, iface.SelSTATUS_A},  // level-status port, read-only
		{iface.CS1, iface.SelBURST},     // auto-inc drain port, read-only
		{iface.CS1, iface.SelFILL},      // fill progress, read-only
		{iface.CS1, iface.SelENV_DATA},  // envelope channel DATA, read-only
		{iface.CS1, 0x0100},             // selector the schema does not define
	}
	for _, c := range nonWritable {
		if iface.Writable(c.plane, c.sel) {
			t.Errorf("cs%d sel %#04x wrongly writable", c.plane, c.sel)
		}
	}
	// Writable control / front-end registers: must pass.
	writable := []struct {
		plane iface.Plane
		sel   uint16
	}{
		{iface.CS1, iface.SelOPCODE},
		{iface.CS1, iface.SelRUN},
		{iface.CS1, iface.SelDECIM_LO},
		{iface.CS1, iface.SelPRETRIG_LO},
		{iface.CS1, iface.SelPOSTTRIG_HI},
		{iface.CS1, iface.SelENV_COLS},
		{iface.CS1, iface.SelENV_RESET},
		{iface.CS3, iface.SelLVL_A_LO},
		{iface.CS3, iface.SelLVL_A_HI},
		{iface.CS3, iface.SelOFF_C1_LO},
	}
	for _, c := range writable {
		if !iface.Writable(c.plane, c.sel) {
			t.Errorf("cs%d sel %#04x not writable but should be", c.plane, c.sel)
		}
	}
}
