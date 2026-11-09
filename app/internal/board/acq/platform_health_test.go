package acq

import (
	"testing"

	"github.com/labeth/open-dso-core/hal"
)

// The platform reports the converter checks' findings as the HAL's
// converter health.
// TRLC-LINKS: REQ-SDS-231
func TestPlatformConverterHealth(t *testing.T) {
	b := New(Config{})
	b.publishHealth(func(h *ConverterHealth) {
		*h = ConverterHealth{Fault: "ADC fault: converter E2 not converting - restart the scope", Recoveries: 1, Recovery: "an encode restart after the start: E4", CheckMs: 2}
	})
	cm, ok := b.Platform().(hal.ConverterMonitor)
	if !ok {
		t.Fatal("the platform reports no converter health")
	}
	want := hal.ConverterHealth{Fault: "ADC fault: converter E2 not converting - restart the scope", Recoveries: 1, Recovery: "an encode restart after the start: E4", CheckMs: 2}
	if got := cm.ConverterHealth(); got != want {
		t.Fatalf("health %+v", got)
	}
}
