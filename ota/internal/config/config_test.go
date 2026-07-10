package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// allKeys is every environment variable Load consults. Tests blank them all
// (t.Setenv restores afterwards) so ambient OTA_* values on the dev machine
// can't leak in; the helpers all treat "" as unset.
var allKeys = []string{
	"OTA_NATS", "OTA_DEVICE_ID", "OTA_NKEY", "OTA_CREDS", "OTA_CA",
	"OTA_HB_INTERVAL", "OTA_LISTEN", "OTA_DIR", "OTA_SLOT_ROOT",
	"OTA_AGENT_A", "OTA_AGENT_B", "OTA_AGENT_ACTIVE", "OTA_AGENT_CONFIRMED",
	"OTA_GPMC", "OTA_FPGA_KEY", "OTA_WD_DEV",
	"OTA_HEALTH_DIR", "OTA_HEALTH_TIMEOUT", "OTA_APP_GRACE", "OTA_STABLE",
	"OTA_MAXFAILS", "OTA_WD_PET", "OTA_FACTORY_NAMES",
	"OTA_TAKEOVER_DELAY", "OTA_AUTO_TAKEOVER",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range allKeys {
		t.Setenv(k, "")
	}
}

func TestLoadDeviceDefaults(t *testing.T) {
	clearEnv(t)
	usb := t.TempDir()
	otaDir := filepath.Join(usb, "ota")
	t.Setenv("OTA_DIR", otaDir)

	c := Load()

	if c.OTADir != otaDir {
		t.Errorf("OTADir = %q, want %q", c.OTADir, otaDir)
	}
	// SlotRoot derives from the USB root (parent of OTA_DIR) by convention.
	if want := filepath.Join(usb, "agent-slots"); c.SlotRoot != want {
		t.Errorf("SlotRoot = %q, want %q", c.SlotRoot, want)
	}
	for name, got := range map[string]struct{ got, want string }{
		"AgentA":         {c.AgentA, filepath.Join(otaDir, "agent.A")},
		"AgentB":         {c.AgentB, filepath.Join(otaDir, "agent.B")},
		"AgentActive":    {c.AgentActive, filepath.Join(otaDir, "agent.active")},
		"AgentConfirmed": {c.AgentConfirmed, filepath.Join(otaDir, "agent.confirmed")},
		"GpmcDev":        {c.GpmcDev, "/dev/Gpmc"},
		"FpgaKeyDev":     {c.FpgaKeyDev, "/dev/fpga_key"},
		"WatchdogDev":    {c.WatchdogDev, "/dev/watchdog"},
		"HealthDir":      {c.HealthDir, "/dev"},
		"TCPListen":      {c.TCPListen, ":5900"},
		"NATSURL":        {c.NATSURL, ""},
	} {
		if got.got != got.want {
			t.Errorf("%s = %q, want %q", name, got.got, got.want)
		}
	}
	for name, got := range map[string]struct {
		got, want time.Duration
	}{
		"HBInterval":    {c.HBInterval, 10 * time.Second},
		"HealthTimeout": {c.HealthTimeout, 3 * time.Second},
		"AppGrace":      {c.AppGrace, 30 * time.Second},
		"StableSecs":    {c.StableSecs, 30 * time.Second},
		"WdPet":         {c.WdPet, 15 * time.Second},
		"TakeoverDelay": {c.TakeoverDelay, 0}, // instant auto-takeover by default
	} {
		if got.got != got.want {
			t.Errorf("%s = %v, want %v", name, got.got, got.want)
		}
	}
	if c.MaxFails != 3 {
		t.Errorf("MaxFails = %d, want 3", c.MaxFails)
	}
	if c.AutoTakeover {
		t.Error("AutoTakeover should default to false (coexist)")
	}
	if c.FactoryNames != nil {
		t.Errorf("FactoryNames = %v, want nil", c.FactoryNames)
	}
	if !strings.HasPrefix(c.DeviceID, "sds-") {
		t.Errorf("DeviceID = %q, want an sds- prefixed default", c.DeviceID)
	}

	// Derived path contract (spec 01 §2.3/§4.2).
	if want := "/dev/app.health"; c.HealthPath() != want {
		t.Errorf("HealthPath = %q, want %q", c.HealthPath(), want)
	}
	if want := filepath.Join(otaDir, "state.json"); c.StatePath() != want {
		t.Errorf("StatePath = %q, want %q", c.StatePath(), want)
	}
	if want := filepath.Join(otaDir, "logs"); c.LogDir() != want {
		t.Errorf("LogDir = %q, want %q", c.LogDir(), want)
	}
}

func TestLoadEveryOverride(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	for k, v := range map[string]string{
		"OTA_NATS":            "nats://10.0.0.1:4222",
		"OTA_DEVICE_ID":       "sds-test",
		"OTA_NKEY":            "/k/nkey",
		"OTA_CREDS":           "/k/creds",
		"OTA_CA":              "/k/ca.pem",
		"OTA_HB_INTERVAL":     "2.5",
		"OTA_LISTEN":          "127.0.0.1:5999",
		"OTA_DIR":             dir + "/o",
		"OTA_SLOT_ROOT":       dir + "/s",
		"OTA_AGENT_A":         dir + "/aA",
		"OTA_AGENT_B":         dir + "/aB",
		"OTA_AGENT_ACTIVE":    dir + "/act",
		"OTA_AGENT_CONFIRMED": dir + "/conf",
		"OTA_GPMC":            dir + "/gpmc",
		"OTA_FPGA_KEY":        dir + "/key",
		"OTA_WD_DEV":          dir + "/wd",
		"OTA_HEALTH_DIR":      dir + "/h",
		"OTA_HEALTH_TIMEOUT":  "1",
		"OTA_APP_GRACE":       "7",
		"OTA_STABLE":          "0.5",
		"OTA_MAXFAILS":        "9",
		"OTA_WD_PET":          "20",
		"OTA_FACTORY_NAMES":   "SDS1000,phoenix",
		"OTA_TAKEOVER_DELAY":  "3",
		"OTA_AUTO_TAKEOVER":   "yes",
	} {
		t.Setenv(k, v)
	}
	c := Load()
	if c.NATSURL != "nats://10.0.0.1:4222" || c.DeviceID != "sds-test" ||
		c.NKeyFile != "/k/nkey" || c.CredsFile != "/k/creds" || c.CAFile != "/k/ca.pem" {
		t.Errorf("identity/transport overrides not honored: %+v", c)
	}
	if c.HBInterval != 2500*time.Millisecond {
		t.Errorf("HBInterval = %v, want 2.5s (fractional seconds must parse)", c.HBInterval)
	}
	if c.TCPListen != "127.0.0.1:5999" {
		t.Errorf("TCPListen = %q", c.TCPListen)
	}
	if c.OTADir != dir+"/o" || c.SlotRoot != dir+"/s" {
		t.Errorf("layout overrides not honored: %+v", c)
	}
	if c.AgentA != dir+"/aA" || c.AgentB != dir+"/aB" || c.AgentActive != dir+"/act" || c.AgentConfirmed != dir+"/conf" {
		t.Errorf("agent slot overrides not honored: %+v", c)
	}
	if c.GpmcDev != dir+"/gpmc" || c.FpgaKeyDev != dir+"/key" || c.WatchdogDev != dir+"/wd" {
		t.Errorf("device overrides not honored: %+v", c)
	}
	if c.HealthDir != dir+"/h" || c.HealthTimeout != time.Second || c.AppGrace != 7*time.Second ||
		c.StableSecs != 500*time.Millisecond || c.MaxFails != 9 || c.WdPet != 20*time.Second {
		t.Errorf("health/supervision overrides not honored: %+v", c)
	}
	if c.TakeoverDelay != 3*time.Second || !c.AutoTakeover {
		t.Errorf("takeover overrides not honored: %+v", c)
	}
	if len(c.FactoryNames) != 2 || c.FactoryNames[0] != "SDS1000" || c.FactoryNames[1] != "phoenix" {
		t.Errorf("FactoryNames = %v", c.FactoryNames)
	}
	if want := dir + "/h/app.health"; c.HealthPath() != want {
		t.Errorf("HealthPath = %q, want %q", c.HealthPath(), want)
	}
}

func TestEnvDurSecs(t *testing.T) {
	const key = "OTA_TEST_DUR"
	def := 10 * time.Second
	cases := []struct {
		val  string
		want time.Duration
	}{
		{"", def}, // unset -> default
		{"2", 2 * time.Second},
		{"0.5", 500 * time.Millisecond},
		{"0", def},   // zero is not a valid interval -> default
		{"-3", def},  // negative -> default
		{"abc", def}, // malformed -> default
		{"1e-3", time.Millisecond},
		{" 5", def}, // ParseFloat rejects leading space -> default
	}
	for _, tc := range cases {
		t.Setenv(key, tc.val)
		if got := envDurSecs(key, def); got != tc.want {
			t.Errorf("envDurSecs(%q) = %v, want %v", tc.val, got, tc.want)
		}
	}
}

func TestEnvInt(t *testing.T) {
	const key = "OTA_TEST_INT"
	cases := []struct {
		val  string
		want int
	}{
		{"", 3}, // unset -> default
		{"5", 5},
		{"0", 3}, // non-positive -> default (0 retries would disable rollback)
		{"-1", 3},
		{"2.5", 3}, // not an int -> default
		{"junk", 3},
	}
	for _, tc := range cases {
		t.Setenv(key, tc.val)
		if got := envInt(key, 3); got != tc.want {
			t.Errorf("envInt(%q) = %d, want %d", tc.val, got, tc.want)
		}
	}
}

func TestEnvBool(t *testing.T) {
	const key = "OTA_TEST_BOOL"
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{"on", true},
		{" on ", true}, // trimmed
		{"0", false},
		{"no", false},
		{"off", false},
		{"false", false},
		{"junk", false},
	}
	for _, tc := range cases {
		t.Setenv(key, tc.val)
		if got := envBool(key); got != tc.want {
			t.Errorf("envBool(%q) = %v, want %v", tc.val, got, tc.want)
		}
	}
}

func TestFactoryNamesParsing(t *testing.T) {
	clearEnv(t)
	t.Setenv("OTA_DIR", t.TempDir())
	t.Setenv("OTA_FACTORY_NAMES", " sds , phoenix ,, SDS1000_arm.app ,")
	c := Load()
	want := []string{"sds", "phoenix", "SDS1000_arm.app"}
	if len(c.FactoryNames) != len(want) {
		t.Fatalf("FactoryNames = %v, want %v", c.FactoryNames, want)
	}
	for i := range want {
		if c.FactoryNames[i] != want[i] {
			t.Errorf("FactoryNames[%d] = %q, want %q", i, c.FactoryNames[i], want[i])
		}
	}
}

func TestEnvHelper(t *testing.T) {
	const key = "OTA_TEST_STR"
	t.Setenv(key, "")
	if got := env(key, "fallback"); got != "fallback" {
		t.Errorf("env empty = %q, want fallback", got)
	}
	t.Setenv(key, "set")
	if got := env(key, "fallback"); got != "set" {
		t.Errorf("env set = %q, want set", got)
	}
}
