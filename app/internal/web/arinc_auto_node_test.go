// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import (
	"open-sds/app/internal/testenv"
	"os/exec"
	"testing"
)

// TRLC-LINKS: REQ-SDS-204
func TestARINCAutoParityErrorsJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "arinc_auto.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("ARINC auto: %v\n%s", err, out)
	}
}

// TRLC-LINKS: REQ-SDS-207
func TestSENTAutoAppliesSettingsJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "sent_auto.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("SENT auto: %v\n%s", err, out)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestUARTRepeatNotSENTJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "uart_repeat_auto.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("UART repeat auto: %v\n%s", err, out)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestRingingNotI2CJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "i2c_ringing_auto.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("ringing auto: %v\n%s", err, out)
	}
}

// TRLC-LINKS: REQ-SDS-018
func TestShortCaptureNoBusJS(t *testing.T) {
	testenv.NeedNode(t)
	if out, err := exec.Command("node", "short_capture_auto.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("short capture auto: %v\n%s", err, out)
	}
}
