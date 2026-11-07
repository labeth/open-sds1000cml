// ENGMODEL-OWNER-UNIT: FU-OTA-OTACTL

package main

import (
	"bytes"
	"testing"

	"github.com/labeth/open-dso-core/supervisor/config"
	"github.com/labeth/open-dso-core/supervisor/otactl"
	"github.com/labeth/open-dso-core/supervisor/slots"
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

// usageText is otactl's usage as this scope's operators and scripts know it.
const usageText = `otactl — open-sds OTA host controller

USAGE
  otactl [global flags] <command> [args]

GLOBAL FLAGS
  -tcp host:port    device TCP control address (agent OTA_LISTEN, e.g. 192.168.1.209:5900)
  -nats url         NATS broker URL (alternative transport)
  -device id        device id (with -nats)
  -shelly host      Shelly power plug host (for the power command)
  -stage dir        device staging dir for uploads (default the stick's
                    /usr/bin/siglent/usr/media/U-disk0/agent-slots/staging)
  -timeout n        RPC timeout seconds (default 30)

COMMANDS
  serve                     run an embedded NATS broker for the lab
  discover                  (nats) list devices that answer
  watch                     (nats) stream events + heartbeats
  status                    device + app + slot + watchdog status
  probe [--gpmc]            read-only device fingerprint
  ping
  logs [agent|boot]         tail a device log
  exec <argv...>            run a command on the device
  sh <script...>            run a /bin/sh script on the device
  takeover [--dry-run|--force]   inherit-then-kill the factory app
  untakeover                release control (clear taken_over + disarm wd)
  scpi <cmd>                raw VXI-11 SCPI to the instrument (e.g. scpi "*IDN?")
  app start|stop|restart    app lifecycle (after takeover)
  activate <A|B>            set active app slot + restart
  update-app <bin>          OTA the app (upload -> inactive slot -> restart)
  update-agent <bin>        OTA the agent itself (A/B agent slot)
  put <local> <devpath>     upload a file
  get <devpath> <local>     download a file
  reboot [--confirm]        soft reboot (often a no-op on this unit)
  power on|off|cycle|state  Shelly mains control (hard power-cycle)
  version
`

// The usage is this scope's, byte for byte.
// TRLC-LINKS: REQ-SDS-175
func TestUsageIsThisScopes(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := otactl.Run(nil, &out, &errOut, options()); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if errOut.String() != usageText {
		t.Errorf("usage changed:\n%s", errOut.String())
	}
}
