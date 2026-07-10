// Package agent is the on-device OTA supervisor for the open-sds1000cml
// project. It is the "boot-tree supervisor" of spec 01: the permanent holder
// of the inherited /dev/Gpmc and /dev/fpga_key descriptors, the owner of the
// hardware watchdog after takeover, the launcher/supervisor of the clean-room
// app (A/B slots + health rollback), and the remote-orchestration endpoint
// (NATS + local TCP JSON-RPC).
//
// Modes:
//   - coexist (default): the factory app runs the instrument; the agent only
//     provides remote access and never touches the GPMC bus (a stray access
//     during the factory app's capture-halt window black-screens the unit).
//   - taken over: the factory app has been stopped at an idle landing and
//     killed; the agent pets the watchdog and supervises the app slots.
package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"open-sds/ota/internal/buildinfo"
	"open-sds/ota/internal/config"
	"open-sds/ota/internal/fdinherit"
	"open-sds/ota/internal/gpmc"
	"open-sds/ota/internal/slots"
	"open-sds/ota/internal/sysinfo"
	"open-sds/ota/internal/watchdog"
)

const (
	appPidFile   = "ota-app.pid"
	agentPidFile = "ota-agent.pid"
)

// gpmcReader is the read-only register surface the agent needs from the
// inherited /dev/Gpmc descriptor. *gpmc.Reader is the sole production
// implementation (assigned in New); the interface exists so the off-device
// test harness can stand in an idle/busy engine without a real bus.
type gpmcReader interface {
	OK() bool
	Read(plane uint8, sel uint16) (uint16, error)
	VerifyVersion() (uint16, bool)
	FillFrozen(pairs int, gap time.Duration) (bool, []uint16, error)
}

type Agent struct {
	cfg   *config.Config
	st    *stateFile
	store *slots.Store
	wd    *watchdog.Watchdog
	log   *log.Logger

	// Inherited boot descriptors (raw ints; never closed, never wrapped).
	gpmcFD    int
	fpgaKeyFD int
	gpmc      gpmcReader

	started time.Time
	hbSeq   atomic.Int64

	// supervisor control
	ctl     chan ctlMsg
	stopped chan struct{}

	// live app state (guarded)
	appMu        sync.Mutex
	app          appState
	paused       bool
	useEmergency bool // recovery ladder forced the emergency binary

	// upload staging sessions
	upMu sync.Mutex
	ups  map[string]*uploadSession

	// event sink (wired by the NATS link; nil-safe)
	eventMu sync.Mutex
	eventFn func(kind string, detail map[string]any)

	// takeover serialization
	tkMu sync.Mutex

	// TCP control listener bound address (set once serveTCP is listening;
	// observable so tests and diagnostics can find an OTA_LISTEN=:0 port)
	tcpMu   sync.Mutex
	tcpAddr string
}

type appState struct {
	Running   bool   `json:"running"`
	Adopted   bool   `json:"adopted"` // orphan from a previous agent generation
	PID       int    `json:"pid,omitempty"`
	Slot      string `json:"slot,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	Fails     int    `json:"fails"`
	LastExit  string `json:"last_exit,omitempty"`
	Emergency bool   `json:"emergency"`
	Health    any    `json:"health,omitempty"`
}

type ctlMsg struct {
	op    string // "restart" | "stop" | "start"
	reply chan error
}

func New(cfg *config.Config) *Agent {
	a := &Agent{
		cfg:     cfg,
		st:      loadState(cfg.StatePath()),
		store:   slots.New(cfg.SlotRoot),
		wd:      watchdog.New(cfg.WatchdogDev),
		log:     log.New(os.Stdout, "[agent] ", log.LstdFlags),
		started: time.Now(),
		ctl:     make(chan ctlMsg, 4),
		stopped: make(chan struct{}),
		ups:     map[string]*uploadSession{},
	}
	a.gpmcFD = fdinherit.Find(cfg.GpmcDev)
	a.fpgaKeyFD = fdinherit.Find(cfg.FpgaKeyDev)
	a.gpmc = gpmc.NewReader(a.gpmcFD)
	return a
}

// Run starts everything and blocks until Stop.
func (a *Agent) Run() error {
	if err := a.store.Init(); err != nil {
		a.log.Printf("slot store init: %v (continuing)", err)
	}
	_ = os.WriteFile(a.pidPath(agentPidFile), fmt.Appendf(nil, "%d\n", os.Getpid()), 0o644)

	a.log.Printf("open-sds OTA agent %s device=%s slot=%s", buildinfo.String(), a.cfg.DeviceID, a.AgentSlot())
	a.log.Printf("inherited fds: gpmc=%d fpga_key=%d", a.gpmcFD, a.fpgaKeyFD)
	st := a.st.get()
	a.log.Printf("state: taken_over=%v auto_takeover=%v", st.TakenOver, st.AutoTakeover)

	// Already-taken-over restart (agent update / crash respawn): the factory
	// app is long dead; nothing pets the watchdog until we do. Acquire NOW.
	if st.TakenOver {
		go func() {
			if err := a.acquireWatchdogForever(); err != nil {
				a.log.Printf("watchdog: %v", err)
			}
		}()
	} else if st.AutoTakeover || a.cfg.AutoTakeover {
		// Default is COEXIST: the factory app keeps driving the instrument and
		// the agent only provides remote access + probe. Auto-takeover happens
		// only when explicitly armed (OTA_AUTO_TAKEOVER or a persisted flag).
		go func() {
			// The factory app is already up and settled by the time the firmware
			// runs startup.sh (it starts long before), so take over immediately.
			// OTA_TAKEOVER_DELAY can still add a settle wait for an odd unit.
			if a.cfg.TakeoverDelay > 0 {
				a.log.Printf("auto-takeover armed: settling %s", a.cfg.TakeoverDelay)
				time.Sleep(a.cfg.TakeoverDelay)
			} else {
				a.log.Printf("auto-takeover armed: taking over now")
			}
			res := a.Takeover(TakeoverOpts{})
			a.log.Printf("auto-takeover: ok=%v %s", res.OK, res.Summary())
		}()
	} else {
		a.log.Printf("coexist mode: factory app owns the instrument; run `otactl takeover` when ready")
	}

	go a.superviseLoop()

	if a.cfg.TCPListen != "" {
		go a.serveTCP()
	}
	if a.cfg.NATSURL != "" {
		go a.runNATS()
	} else {
		a.log.Printf("OTA_NATS not set — NATS link disabled (local TCP control listener only)")
	}

	<-a.stopped
	return nil
}

// Stop performs the clean-shutdown path: disarm the watchdog with the magic
// byte so the driver doesn't reset while the respawn loop restarts us. The
// app (if any) is left running — the next agent generation adopts it.
func (a *Agent) Stop() {
	select {
	case <-a.stopped:
		return
	default:
	}
	a.wd.Disarm()
	close(a.stopped)
}

func (a *Agent) pidPath(name string) string {
	return filepath.Join(a.cfg.HealthDir, name)
}

// AgentSlot reports which A/B agent binary this process is (or "?" off-slot).
func (a *Agent) AgentSlot() string {
	exe, err := os.Executable()
	if err != nil {
		return "?"
	}
	exe, _ = filepath.EvalSymlinks(exe)
	for slot, p := range map[string]string{"A": a.cfg.AgentA, "B": a.cfg.AgentB} {
		if rp, err := filepath.EvalSymlinks(p); err == nil && rp == exe {
			return slot
		}
	}
	return "?"
}

func (a *Agent) event(kind string, detail map[string]any) {
	a.log.Printf("event %s: %v", kind, detail)
	a.eventMu.Lock()
	fn := a.eventFn
	a.eventMu.Unlock()
	if fn != nil {
		fn(kind, detail)
	}
}

func (a *Agent) setEventFn(fn func(string, map[string]any)) {
	a.eventMu.Lock()
	a.eventFn = fn
	a.eventMu.Unlock()
}

// acquireWatchdogForever keeps trying until the watchdog is ours; used after
// takeover (or on restart in the taken-over state). Failure to acquire while
// the factory app is dead means a warm reset in ~60 s, so never give up.
func (a *Agent) acquireWatchdogForever() error {
	for i := 0; ; i++ {
		err := a.wd.Acquire(15*time.Second, a.cfg.WdPet)
		if err == nil {
			a.log.Printf("watchdog acquired (%s), petting every %s", a.cfg.WatchdogDev, a.cfg.WdPet)
			return nil
		}
		if i%4 == 0 { // don't spam the log
			a.log.Printf("watchdog acquire failed (attempt %d): %v — retrying", i+1, err)
		}
		select {
		case <-a.stopped:
			return err
		case <-time.After(5 * time.Second):
		}
	}
}

type statusDoc struct {
	Device       string          `json:"device"`
	Version      string          `json:"version"`
	AgentSlot    string          `json:"agent_slot"`
	UptimeS      int64           `json:"uptime_s"`
	TakenOver    bool            `json:"taken_over"`
	AutoTakeover bool            `json:"auto_takeover"`
	Paused       bool            `json:"paused"`
	App          appState        `json:"app"`
	Slots        slots.Status    `json:"slots"`
	Watchdog     watchdog.Status `json:"watchdog"`
	FDs          map[string]int  `json:"inherited_fds"`
	Sys          sysinfo.Info    `json:"sys"`
	TimeUnix     int64           `json:"time_unix"`
}

func (a *Agent) status() statusDoc {
	st := a.st.get()
	a.appMu.Lock()
	app := a.app
	paused := a.paused
	a.appMu.Unlock()
	return statusDoc{
		Device:       a.cfg.DeviceID,
		Version:      buildinfo.String(),
		AgentSlot:    a.AgentSlot(),
		UptimeS:      int64(time.Since(a.started).Seconds()),
		TakenOver:    st.TakenOver,
		AutoTakeover: st.AutoTakeover,
		Paused:       paused,
		App:          app,
		Slots:        a.store.Status(),
		Watchdog:     a.wd.Status(),
		FDs:          map[string]int{"gpmc": a.gpmcFD, "fpga_key": a.fpgaKeyFD},
		Sys:          sysinfo.Collect(a.cfg.OTADir),
		TimeUnix:     time.Now().Unix(),
	}
}
