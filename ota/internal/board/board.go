// Package board is the SDS1102CML+'s side of the core update supervisor
// (open-dso-core/supervisor/agent): the boot descriptors the boot chain opens
// (/dev/Gpmc and /dev/fpga_key: a fresh open() faults, closing the last
// holder frees the chip select), the factory app's takeover over its own
// VXI-11 SCPI with the idle landing confirmed on the GPMC fill counter, the
// AM335x watchdog, and the device fingerprint. The supervisor itself (slots,
// health, rollback, RPC, NATS) is the core's.
//
// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package board

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/supervisor/agent"
	"github.com/labeth/open-dso-core/supervisor/config"
	"github.com/labeth/open-dso-core/supervisor/fdinherit"

	"github.com/labeth/open-sds1000cml/ota/internal/gpmc"
	"github.com/labeth/open-sds1000cml/ota/internal/watchdog"
)

// Config is the board's own settings, each named after the variable that
// sets it.
// TRLC-LINKS: REQ-SDS-088
type Config struct {
	GpmcDev      string   // OTA_GPMC
	FpgaKeyDev   string   // OTA_FPGA_KEY
	WatchdogDev  string   // OTA_WD_DEV
	FactoryNames []string // OTA_FACTORY_NAMES — extra exe/comm hints, comma-sep
}

// LoadConfig reads the board's settings from the environment startup.sh
// exports, with the device defaults.
// TRLC-LINKS: REQ-SDS-088
func LoadConfig() Config {
	return Config{
		GpmcDev:      config.Env("OTA_GPMC", "/dev/Gpmc"),
		FpgaKeyDev:   config.Env("OTA_FPGA_KEY", "/dev/fpga_key"),
		WatchdogDev:  config.Env("OTA_WD_DEV", "/dev/watchdog"),
		FactoryNames: config.List("OTA_FACTORY_NAMES"),
	}
}

// gpmcReader is the read-only register surface the takeover needs from the
// inherited /dev/Gpmc descriptor. *gpmc.Reader is the sole production
// implementation (assigned in New); the interface exists so the off-device
// test harness can stand in an idle/busy engine without a real bus.
// TRLC-LINKS: REQ-SDS-122, REQ-SDS-114
type gpmcReader interface {
	OK() bool
	Read(plane uint8, sel uint16) (uint16, error)
	VerifyVersion() (uint16, bool)
	FillFrozen(pairs int, gap time.Duration) (bool, []uint16, error)
}

// Board is the SDS1102CML+ under the core supervisor.
// TRLC-LINKS: REQ-SDS-122
type Board struct {
	cfg *config.Config
	bc  Config
	wd  *watchdog.Device

	// Inherited boot descriptors (raw ints; never closed, never wrapped):
	// the takeover reads them under the agent's takeover lock, status and
	// probe under fdMu.
	fdMu      sync.Mutex
	gpmcFD    int
	fpgaKeyFD int
	gpmc      gpmcReader

	// preIPs is the network as Land found it, for the re-assert after the kill.
	preIPs []string

	agent *agent.Agent // set by Attach
}

var (
	_ agent.Board    = (*Board)(nil)
	_ agent.Attacher = (*Board)(nil)
)

// New finds the inherited boot descriptors named by bc.
// TRLC-LINKS: REQ-SDS-122
func New(cfg *config.Config, bc Config) *Board {
	b := &Board{cfg: cfg, bc: bc, wd: watchdog.New(bc.WatchdogDev)}
	b.gpmcFD = fdinherit.Find(bc.GpmcDev)
	b.fpgaKeyFD = fdinherit.Find(bc.FpgaKeyDev)
	b.gpmc = gpmc.NewReader(b.gpmcFD)
	return b
}

// Attach is the agent the board logs and sends events through.
// TRLC-LINKS: REQ-SDS-122
func (b *Board) Attach(a *agent.Agent) { b.agent = a }

// TRLC-LINKS: REQ-SDS-122
func (b *Board) logf(format string, args ...any) {
	if b.agent != nil {
		b.agent.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// TRLC-LINKS: REQ-SDS-123
func (b *Board) event(kind string, detail map[string]any) {
	if b.agent != nil {
		b.agent.Event(kind, detail)
	}
}

// Watchdog is the AM335x's ~60 s watchdog the factory firmware services.
// TRLC-LINKS: REQ-SDS-086
func (b *Board) Watchdog() (hal.Watchdog, bool) { return b.wd, true }

// LaunchEnv names the bus device for the app. The boot fds (Gpmc, fpga_key)
// carry no CLOEXEC flag — they were opened by the boot chain — so the child
// inherits them without ExtraFiles plumbing.
// TRLC-LINKS: REQ-SDS-028
func (b *Board) LaunchEnv() ([]string, []*os.File) {
	return []string{"SCOPE_GPMC=" + b.bc.GpmcDev}, nil
}

// Alive reports whether pid runs.
// TRLC-LINKS: REQ-SDS-028
func (b *Board) Alive(pid int) bool { return fdinherit.Alive(pid) }

// SlotsDir is the app slots on the stick.
// TRLC-LINKS: REQ-SDS-028
func (b *Board) SlotsDir() string { return b.cfg.SlotRoot }

// Reboot asks the kernel for a reboot: often a no-op on this unit; a wedge
// needs the external power plug.
// TRLC-LINKS: REQ-SDS-121
func (b *Board) Reboot(reason string) error {
	b.logf("reboot: %s", reason)
	_, err := agent.RunShell("sync; (sleep 1; reboot) &", 5*time.Second)
	return err
}

// Heartbeat is nil: nothing on this scope expects one.
// TRLC-LINKS: REQ-SDS-122
func (b *Board) Heartbeat() error { return nil }

// BootFDs reports the inherited descriptor numbers (-1 when missing).
// TRLC-LINKS: REQ-SDS-114
func (b *Board) BootFDs() map[string]int {
	b.fdMu.Lock()
	defer b.fdMu.Unlock()
	return map[string]int{"gpmc": b.gpmcFD, "fpga_key": b.fpgaKeyFD}
}

// reader is the register reader on the inherited Gpmc descriptor.
// TRLC-LINKS: REQ-SDS-114
func (b *Board) reader() gpmcReader {
	b.fdMu.Lock()
	defer b.fdMu.Unlock()
	return b.gpmc
}

// Takeover kills the factory tree (fighting init respawns for a few rounds)
// and re-asserts the LAN address in the background: the kill may take the
// vendor's network management with it, and the address with it the remote
// path.
// TRLC-LINKS: REQ-SDS-027
func (b *Board) Takeover(ctx context.Context) error {
	res := agent.ResultOf(ctx)
	res.Killed = b.killFactory(res)
	res.Step("killed %d factory processes", res.Killed)
	go reassertNetworkFn(b, b.preIPs)
	return nil
}
