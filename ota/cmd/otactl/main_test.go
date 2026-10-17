package main

import (
	"testing"

	"open-sds/ota/internal/config"
	"open-sds/ota/internal/slots"
)

// TestDefaultStageIsTheAgentsStaging checks that uploads stage by default
// where the agent on a stick keeps its upload staging: the scope's root file
// system, /tmp included, is read-only, and the stick mounts at U-disk0.
// TRLC-LINKS: REQ-SDS-175
func TestDefaultStageIsTheAgentsStaging(t *testing.T) {
	t.Setenv("OTA_DIR", "/usr/bin/siglent/usr/media/U-disk0/ota")
	t.Setenv("OTA_SLOT_ROOT", "")
	cfg := config.Load()
	if want := slots.New(cfg.SlotRoot).StagingDir(); defaultStageDir != want {
		t.Fatalf("default -stage %q, want the agent's staging %q", defaultStageDir, want)
	}
}
