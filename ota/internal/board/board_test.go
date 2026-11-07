// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT

package board

import (
	"os"
	"strings"
	"testing"

	"github.com/labeth/open-dso-core/supervisor/agent"
	"github.com/labeth/open-dso-core/supervisor/config"
)

// testBoard is the core agent on this board, kept away from the host's real
// devices: the watchdog is a plain temp file (arming the host watchdog would
// be a real reset risk) and the GPMC/fpga-key are paths no process holds.
// TRLC-LINKS: REQ-SDS-027, REQ-SDS-113
func testBoard(t *testing.T) (*agent.Agent, *Board) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OTA_DIR", dir+"/ota")
	t.Setenv("OTA_SLOT_ROOT", dir+"/slots")
	t.Setenv("OTA_HEALTH_DIR", dir)
	t.Setenv("OTA_LISTEN", "") // don't bind in tests
	t.Setenv("OTA_NATS", "")
	wd := dir + "/watchdog"
	if err := os.WriteFile(wd, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTA_WD_DEV", wd)
	t.Setenv("OTA_GPMC", dir+"/Gpmc")
	t.Setenv("OTA_FPGA_KEY", dir+"/fpga_key")
	t.Setenv("OTA_FACTORY_NAMES", "")
	if err := os.MkdirAll(dir+"/ota", 0o755); err != nil {
		t.Fatal(err) // state.json needs the dir to exist
	}
	b := New(config.Load(), LoadConfig())
	a := agent.New(b.cfg, b, "test")
	t.Cleanup(a.DisarmWatchdog)
	return a, b
}

// TRLC-LINKS: REQ-SDS-088
func TestLoadConfig(t *testing.T) {
	for _, k := range []string{"OTA_GPMC", "OTA_FPGA_KEY", "OTA_WD_DEV", "OTA_FACTORY_NAMES"} {
		t.Setenv(k, "")
	}
	if c := LoadConfig(); c.GpmcDev != "/dev/Gpmc" || c.FpgaKeyDev != "/dev/fpga_key" || c.WatchdogDev != "/dev/watchdog" || c.FactoryNames != nil {
		t.Errorf("defaults %+v", c)
	}
	t.Setenv("OTA_GPMC", "/x/gpmc")
	t.Setenv("OTA_FPGA_KEY", "/x/key")
	t.Setenv("OTA_WD_DEV", "/x/wd")
	t.Setenv("OTA_FACTORY_NAMES", " sds , phoenix ,, SDS1000_arm.app ,")
	c := LoadConfig()
	if c.GpmcDev != "/x/gpmc" || c.FpgaKeyDev != "/x/key" || c.WatchdogDev != "/x/wd" ||
		strings.Join(c.FactoryNames, "|") != "sds|phoenix|SDS1000_arm.app" {
		t.Errorf("overrides %+v", c)
	}
}

// The app inherits the boot fds and is told the bus device; the status
// reports the inherited fds by their names.
// TRLC-LINKS: REQ-SDS-028, REQ-SDS-114
func TestLaunchEnvAndBootFDs(t *testing.T) {
	_, b := testBoard(t)
	env, files := b.LaunchEnv()
	if len(env) != 1 || env[0] != "SCOPE_GPMC="+b.bc.GpmcDev || files != nil {
		t.Errorf("LaunchEnv = %v, %v", env, files)
	}
	if fds := b.BootFDs(); len(fds) != 2 || fds["gpmc"] != -1 || fds["fpga_key"] != -1 {
		t.Errorf("BootFDs = %v, want gpmc and fpga_key missing (-1) on the dev machine", fds)
	}
}

// TRLC-LINKS: REQ-SDS-027
func TestTakeoverRefusedWithoutInheritedFD(t *testing.T) {
	a, _ := testBoard(t)
	// In the test environment there is no inherited /dev/Gpmc fd, so takeover
	// must refuse rather than drop the (nonexistent) chip select.
	resp := a.Dispatch([]byte(`{"cmd":"takeover"}`))
	if resp.OK {
		t.Error("takeover without inherited fd must not succeed")
	}
	if !strings.Contains(resp.Err, "inherited") {
		t.Errorf("err = %q, want it to mention the missing inherited fd", resp.Err)
	}
}

// TRLC-LINKS: REQ-SDS-027
func TestTakeoverDryRunReportsCandidates(t *testing.T) {
	a, b := testBoard(t)
	// Force an inherited fd so the dry-run passes the first gate; -1 would
	// refuse. Dry-run reads nothing through it.
	b.gpmcFD = 0 // stdin: present, harmless
	resp := a.Dispatch([]byte(`{"cmd":"takeover","args":{"dry_run":true}}`))
	if !resp.OK {
		t.Fatalf("dry-run should succeed: %s", resp.Err)
	}
	if res, ok := resp.Data.(*agent.TakeoverResult); !ok || !strings.Contains(res.Summary(), "dry-run") {
		t.Errorf("dry-run result missing marker: %+v", resp.Data)
	}
}

// The probe RPC is the board's fingerprint, in its wire field names.
// TRLC-LINKS: REQ-SDS-114
func TestProbeReportShape(t *testing.T) {
	a, _ := testBoard(t)
	raw := string(a.DispatchJSON([]byte(`{"cmd":"probe"}`)))
	for _, k := range []string{`"inherited_fds":{"fpga_key":-1,"gpmc":-1}`, `"gpmc_holders"`, `"fpga_key_holders"`, `"watchdog_holders"`, `"factory_candidates"`, `"cmds"`, `"taken_over":false`} {
		if !strings.Contains(raw, k) {
			t.Errorf("probe reply lacks %s: %.300s", k, raw)
		}
	}
}
