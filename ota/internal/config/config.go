// Package config resolves the agent's runtime configuration from the
// environment exported by the USB boot anchor (startup.sh) and applies the
// device defaults. Every value is overridable so the off-device test harness
// can run the agent against stubs.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Identity + transport.
	NATSURL    string        // OTA_NATS — empty disables the NATS link
	DeviceID   string        // OTA_DEVICE_ID — defaults to sds-<mac tail> or hostname
	NKeyFile   string        // OTA_NKEY
	CredsFile  string        // OTA_CREDS
	CAFile     string        // OTA_CA
	HBInterval time.Duration // OTA_HB_INTERVAL seconds
	TCPListen  string        // OTA_LISTEN — local JSON-RPC fallback, empty disables

	// Layout on the USB stick.
	OTADir   string // OTA_DIR — logs/, state.json, agent.{A,B,active,confirmed}
	SlotRoot string // OTA_SLOT_ROOT — app A/B slots + staging

	// Agent A/B self-update contract (paths from startup.sh).
	AgentA         string
	AgentB         string
	AgentActive    string // pointer file
	AgentConfirmed string // pointer file

	// Devices.
	GpmcDev     string // OTA_GPMC
	FpgaKeyDev  string // OTA_FPGA_KEY
	WatchdogDev string // OTA_WD_DEV

	// Health / supervision contract.
	HealthDir     string        // OTA_HEALTH_DIR — RAM-backed (default /dev)
	HealthTimeout time.Duration // OTA_HEALTH_TIMEOUT — staleness window
	AppGrace      time.Duration // OTA_APP_GRACE — first health report deadline
	StableSecs    time.Duration // OTA_STABLE — healthy runtime that confirms a slot
	MaxFails      int           // OTA_MAXFAILS — failures before rollback
	WdPet         time.Duration // OTA_WD_PET — watchdog pet interval (<60s!)

	// Takeover.
	FactoryNames  []string      // OTA_FACTORY_NAMES — extra exe/comm hints, comma-sep
	TakeoverDelay time.Duration // OTA_TAKEOVER_DELAY — auto-takeover settle after boot
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDurSecs(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	return time.Duration(f * float64(time.Second))
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}

// exeDir returns the directory of the running binary; used to derive the
// OTA dir when startup.sh predates the OTA_DIR export.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func defaultDeviceID() string {
	ifs, err := net.Interfaces()
	if err == nil {
		for _, ifc := range ifs {
			if ifc.Flags&net.FlagLoopback != 0 || len(ifc.HardwareAddr) < 6 {
				continue
			}
			m := ifc.HardwareAddr
			return fmt.Sprintf("sds-%02x%02x%02x", m[3], m[4], m[5])
		}
	}
	if h, err := os.Hostname(); err == nil && h != "" && h != "localhost" {
		return "sds-" + h
	}
	return "sds-unknown"
}

func Load() *Config {
	otaDir := env("OTA_DIR", exeDir())
	usbRoot := filepath.Dir(otaDir) // OTA_DIR is <usb>/ota by convention
	c := &Config{
		NATSURL:    os.Getenv("OTA_NATS"),
		DeviceID:   env("OTA_DEVICE_ID", defaultDeviceID()),
		NKeyFile:   os.Getenv("OTA_NKEY"),
		CredsFile:  os.Getenv("OTA_CREDS"),
		CAFile:     os.Getenv("OTA_CA"),
		HBInterval: envDurSecs("OTA_HB_INTERVAL", 10*time.Second),
		TCPListen:  env("OTA_LISTEN", ":5900"),

		OTADir:   otaDir,
		SlotRoot: env("OTA_SLOT_ROOT", filepath.Join(usbRoot, "agent-slots")),

		AgentA:         env("OTA_AGENT_A", filepath.Join(otaDir, "agent.A")),
		AgentB:         env("OTA_AGENT_B", filepath.Join(otaDir, "agent.B")),
		AgentActive:    env("OTA_AGENT_ACTIVE", filepath.Join(otaDir, "agent.active")),
		AgentConfirmed: env("OTA_AGENT_CONFIRMED", filepath.Join(otaDir, "agent.confirmed")),

		GpmcDev:     env("OTA_GPMC", "/dev/Gpmc"),
		FpgaKeyDev:  env("OTA_FPGA_KEY", "/dev/fpga_key"),
		WatchdogDev: env("OTA_WD_DEV", "/dev/watchdog"),

		HealthDir:     env("OTA_HEALTH_DIR", "/dev"),
		HealthTimeout: envDurSecs("OTA_HEALTH_TIMEOUT", 3*time.Second),
		AppGrace:      envDurSecs("OTA_APP_GRACE", 30*time.Second),
		StableSecs:    envDurSecs("OTA_STABLE", 30*time.Second),
		MaxFails:      envInt("OTA_MAXFAILS", 3),
		WdPet:         envDurSecs("OTA_WD_PET", 15*time.Second),

		TakeoverDelay: envDurSecs("OTA_TAKEOVER_DELAY", 20*time.Second),
	}
	if v := os.Getenv("OTA_FACTORY_NAMES"); v != "" {
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				c.FactoryNames = append(c.FactoryNames, s)
			}
		}
	}
	return c
}

// HealthPath is the agent<->app health-file contract value exported to the
// app as OTA_HEALTH_PATH (spec 01 §2.3/§4.2).
func (c *Config) HealthPath() string {
	return filepath.Join(c.HealthDir, "app.health")
}

// StatePath is the persisted agent state (taken_over etc.) on the stick.
func (c *Config) StatePath() string {
	return filepath.Join(c.OTADir, "state.json")
}

// LogDir is where startup.sh points boot.log/agent.log.
func (c *Config) LogDir() string {
	return filepath.Join(c.OTADir, "logs")
}
