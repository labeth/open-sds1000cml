package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"open-sds/ota/internal/slots"
)

var handlers = map[string]handlerFn{}

// osExit is os.Exit behind a var so the agent.update/agent.restart happy
// paths are testable in-process; production behavior is identical.
var osExit = os.Exit

func register(cmd string, fn handlerFn) { handlers[cmd] = fn }

func init() {
	register("help", hHelp)
	register("ping", hPing)
	register("status", hStatus)
	register("logs", hLogs)
	register("exec", hExec)
	register("sh", hSh)

	// file transfer (chunked)
	register("put.begin", hPutBegin)
	register("put.chunk", hPutChunk)
	register("put.commit", hPutCommit)
	register("get", hGet)

	// app lifecycle + OTA
	register("app.start", hAppStart)
	register("app.stop", hAppStop)
	register("app.restart", hAppRestart)
	register("app.update", hAppUpdate)
	register("app.activate", hAppActivate)
	register("app.install-emergency", hAppInstallEmergency)

	// agent lifecycle + OTA
	register("agent.update", hAgentUpdate)
	register("agent.restart", hAgentRestart)

	// takeover / recovery
	register("takeover", hTakeover)
	register("untakeover", hUntakeover)
	register("restore-factory", hRestoreFactory)
	register("probe", hProbe)
	register("reboot", hReboot)
}

func hHelp(a *Agent, _ json.RawMessage) (any, error) {
	cmds := make([]string, 0, len(handlers))
	for k := range handlers {
		cmds = append(cmds, k)
	}
	sort.Strings(cmds)
	return map[string]any{"commands": cmds, "device": a.cfg.DeviceID}, nil
}

func hPing(a *Agent, _ json.RawMessage) (any, error) {
	return map[string]any{"device": a.cfg.DeviceID, "time_unix": time.Now().Unix(), "agent_slot": a.AgentSlot()}, nil
}

func hStatus(a *Agent, _ json.RawMessage) (any, error) { return a.status(), nil }

func hLogs(a *Agent, args json.RawMessage) (any, error) {
	p, _ := decodeArgs[struct {
		File string `json:"file"` // "agent" | "boot" | absolute path
		Tail int    `json:"tail"` // last N bytes (default 8192)
	}](args)
	path := ""
	switch p.File {
	case "", "agent":
		path = filepath.Join(a.cfg.LogDir(), "agent.log")
	case "boot":
		path = filepath.Join(a.cfg.LogDir(), "boot.log")
	default:
		path = p.File
	}
	n := p.Tail
	if n <= 0 {
		n = 8192
	}
	b, err := tailFile(path, n)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "text": string(b)}, nil
}

func hExec(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		Argv    []string `json:"argv"`
		Dir     string   `json:"dir"`
		Timeout int      `json:"timeout_s"`
	}](args)
	if err != nil {
		return nil, err
	}
	if len(p.Argv) == 0 {
		return nil, fmt.Errorf("exec: empty argv")
	}
	to := time.Duration(p.Timeout) * time.Second
	if to <= 0 {
		to = 30 * time.Second
	}
	out, code, err := a.runArgv(p.Argv, p.Dir, to)
	return map[string]any{"exit": code, "output": string(out)}, err
}

func hSh(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		Script  string `json:"script"`
		Timeout int    `json:"timeout_s"`
	}](args)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Script) == "" {
		return nil, fmt.Errorf("sh: empty script")
	}
	to := time.Duration(p.Timeout) * time.Second
	if to <= 0 {
		to = 30 * time.Second
	}
	out, err := a.runShell(p.Script, to)
	return map[string]any{"output": string(out)}, err
}

// ---- file transfer ---------------------------------------------------------

func hPutBegin(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		Dest string `json:"dest"`
		Size int64  `json:"size"`
		SHA  string `json:"sha256"`
		Mode uint32 `json:"mode"`
	}](args)
	if err != nil {
		return nil, err
	}
	if p.Dest == "" {
		return nil, fmt.Errorf("put.begin: dest required")
	}
	id, err := a.newUpload(p.Dest, p.Size, p.SHA, p.Mode)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

func hPutChunk(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		ID     string `json:"id"`
		Data   string `json:"data"` // base64
		Offset int64  `json:"offset"`
	}](args)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, fmt.Errorf("put.chunk: base64: %w", err)
	}
	n, err := a.writeUpload(p.ID, p.Offset, raw)
	return map[string]any{"written": n}, err
}

func hPutCommit(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		ID string `json:"id"`
	}](args)
	if err != nil {
		return nil, err
	}
	path, sum, err := a.commitUpload(p.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "sha256": sum}, nil
}

func hGet(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		Path   string `json:"path"`
		Offset int64  `json:"offset"`
		Len    int    `json:"len"`
	}](args)
	if err != nil {
		return nil, err
	}
	n := p.Len
	if n <= 0 || n > 512*1024 {
		n = 256 * 1024
	}
	f, err := os.Open(p.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, _ := f.Stat()
	buf := make([]byte, n)
	m, rerr := f.ReadAt(buf, p.Offset)
	eof := rerr != nil
	return map[string]any{
		"data":   base64.StdEncoding.EncodeToString(buf[:m]),
		"offset": p.Offset,
		"eof":    eof,
		"size":   fi.Size(),
	}, nil
}

// ---- app lifecycle ---------------------------------------------------------

func hAppStart(a *Agent, _ json.RawMessage) (any, error) {
	if !a.st.get().TakenOver {
		return nil, fmt.Errorf("not taken over — the factory app owns the instrument; run takeover first")
	}
	a.clearEmergency()
	return map[string]any{"ok": true}, a.ctlRequest("start", 5*time.Second)
}

func hAppStop(a *Agent, _ json.RawMessage) (any, error) {
	return map[string]any{"ok": true}, a.ctlRequest("stop", 8*time.Second)
}

func hAppRestart(a *Agent, _ json.RawMessage) (any, error) {
	if !a.st.get().TakenOver {
		return nil, fmt.Errorf("not taken over")
	}
	return map[string]any{"ok": true}, a.ctlRequest("restart", 8*time.Second)
}

// hAppUpdate installs a staged/uploaded binary into the INACTIVE slot, points
// active at it, and restarts. Stable run confirms it; crash-loop rolls back.
func hAppUpdate(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		Src string `json:"src"` // path on device (usually a committed upload)
		SHA string `json:"sha256"`
	}](args)
	if err != nil {
		return nil, err
	}
	if p.Src == "" {
		return nil, fmt.Errorf("app.update: src required")
	}
	if p.SHA != "" {
		got, err := slots.FileSHA256(p.Src)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(got, p.SHA) {
			return nil, fmt.Errorf("app.update: sha256 mismatch (got %s)", got)
		}
	}
	target := slots.Other(a.store.Active())
	sum, err := a.store.Install(target, p.Src)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetActive(target); err != nil {
		return nil, err
	}
	a.clearEmergency()
	a.event("app.update", map[string]any{"slot": target, "sha256": sum})
	// Restart if running/taken over so the new slot launches now.
	if a.st.get().TakenOver {
		_ = a.ctlRequest("restart", 8*time.Second)
	}
	return map[string]any{"slot": target, "sha256": sum, "active": target}, nil
}

func hAppActivate(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		Slot string `json:"slot"`
	}](args)
	if err != nil {
		return nil, err
	}
	p.Slot = strings.ToUpper(strings.TrimSpace(p.Slot))
	if !a.store.HasBinary(p.Slot) {
		return nil, fmt.Errorf("app.activate: slot %q has no binary", p.Slot)
	}
	if err := a.store.SetActive(p.Slot); err != nil {
		return nil, err
	}
	a.clearEmergency()
	if a.st.get().TakenOver {
		_ = a.ctlRequest("restart", 8*time.Second)
	}
	return map[string]any{"active": p.Slot}, nil
}

func hAppInstallEmergency(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		Src string `json:"src"`
	}](args)
	if err != nil {
		return nil, err
	}
	if p.Src == "" {
		return nil, fmt.Errorf("app.install-emergency: src required")
	}
	sum, err := a.store.Install(slots.SlotEmergency, p.Src)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sha256": sum}, nil
}

// ---- agent self-update -----------------------------------------------------

// hAgentUpdate writes the new agent binary into the INACTIVE agent slot,
// flips agent.active, and re-execs the boot loop by exiting cleanly. The
// startup.sh A/B agent loop then launches the new slot; if it crash-loops
// under STABLE seconds it reverts to the confirmed slot (spec/startup.sh).
// The running app is left alive and re-adopted by the new agent.
func hAgentUpdate(a *Agent, args json.RawMessage) (any, error) {
	p, err := decodeArgs[struct {
		Src string `json:"src"`
		SHA string `json:"sha256"`
	}](args)
	if err != nil {
		return nil, err
	}
	if p.Src == "" {
		return nil, fmt.Errorf("agent.update: src required")
	}
	if p.SHA != "" {
		got, err := slots.FileSHA256(p.Src)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(got, p.SHA) {
			return nil, fmt.Errorf("agent.update: sha256 mismatch (got %s)", got)
		}
	}
	cur := a.AgentSlot()
	target, targetPath := "B", a.cfg.AgentB
	if cur == "B" {
		target, targetPath = "A", a.cfg.AgentA
	}
	sum, err := copyExecFile(p.Src, targetPath)
	if err != nil {
		return nil, err
	}
	if err := writeText(a.cfg.AgentActive, target+"\n"); err != nil {
		return nil, err
	}
	a.event("agent.update", map[string]any{"from": cur, "to": target, "sha256": sum, "path": targetPath})
	// Exit cleanly after replying; the startup.sh loop respawns the new slot.
	go func() {
		time.Sleep(750 * time.Millisecond)
		a.log.Printf("agent.update: exiting to hand off to slot %s", target)
		a.markIntent("update") // deliberate exit — not a crash for the respawn loop
		a.Stop()
		osExit(0)
	}()
	return map[string]any{"active_agent": target, "sha256": sum, "note": "agent exiting; startup.sh will launch the new slot"}, nil
}

func hAgentRestart(a *Agent, _ json.RawMessage) (any, error) {
	go func() {
		time.Sleep(500 * time.Millisecond)
		a.log.Printf("agent.restart requested")
		a.markIntent("restart") // deliberate exit — the respawn loop must not count it as a crash
		a.Stop()
		osExit(0)
	}()
	return map[string]any{"ok": true, "note": "agent exiting; startup.sh will respawn"}, nil
}

// ---- takeover / recovery ---------------------------------------------------

func hTakeover(a *Agent, args json.RawMessage) (any, error) {
	opts, err := decodeArgs[TakeoverOpts](args)
	if err != nil {
		return nil, err
	}
	res := a.Takeover(opts)
	if !res.OK {
		return res, fmt.Errorf("%s", res.Err)
	}
	return res, nil
}

// hProbe is the read-only first-session diagnostic: it never touches the bus
// unless asked, and never kills anything. It reports the device environment
// (via otactl probe) so the operator can confirm it before takeover.
// hUntakeover hands control back: stop+kill the supervised app, clear the
// persisted taken_over flag, and disarm the watchdog — so a subsequent reboot
// boots the factory app normally. It does NOT itself restore the factory app
// (use restore-factory or reboot). This is what makes a takeover test fully
// reversible.
func hUntakeover(a *Agent, _ json.RawMessage) (any, error) {
	_ = a.ctlRequest("stop", 8*time.Second) // pause supervisor + terminate the app
	if err := a.st.update(func(s *State) { s.TakenOver = false; s.AutoTakeover = false }); err != nil {
		return nil, err
	}
	a.clearEmergency()
	a.wd.Disarm()
	a.event("untakeover", map[string]any{"note": "control released; reboot or restore-factory to bring the vendor app back"})
	return map[string]any{"taken_over": false, "watchdog_disarmed": true}, nil
}

// hRestoreFactory re-launches the vendor app in place (best-effort), so the
// scope can be recovered without a reboot after a takeover test. It execs the
// factory binary as a child of the agent (inheriting the still-open boot fds)
// from its normal working directory. Reboot is the reliable fallback if the
// vendor app does not cleanly re-drive from a mid-session restart.
func hRestoreFactory(a *Agent, args json.RawMessage) (any, error) {
	p, _ := decodeArgs[struct {
		Path string `json:"path"`
		Dir  string `json:"dir"`
	}](args)
	if p.Path == "" {
		p.Path = "/usr/bin/siglent/SDS1000_arm.app"
	}
	if p.Dir == "" {
		p.Dir = "/usr/bin/siglent"
	}
	if st := a.st.get(); st.TakenOver {
		return nil, fmt.Errorf("still taken over — run untakeover first so the supervisor doesn't fight the factory app for the bus")
	}
	if _, err := os.Stat(p.Path); err != nil {
		return nil, fmt.Errorf("restore-factory: %s not found: %w", p.Path, err)
	}
	pid, err := a.launchDetached(p.Path, p.Dir)
	if err != nil {
		return nil, err
	}
	a.event("restore-factory", map[string]any{"path": p.Path, "pid": pid})
	return map[string]any{"launched": p.Path, "pid": pid, "note": "if the display does not return, reboot"}, nil
}

func hProbe(a *Agent, args json.RawMessage) (any, error) {
	p, _ := decodeArgs[struct {
		ReadGpmc bool `json:"read_gpmc"` // opt-in: read version+fill (safe, plain regs)
	}](args)
	return a.probe(p.ReadGpmc), nil
}

func hReboot(a *Agent, args json.RawMessage) (any, error) {
	p, _ := decodeArgs[struct {
		Confirm bool `json:"confirm"`
	}](args)
	if !p.Confirm {
		return nil, fmt.Errorf("reboot: pass confirm=true — note reboot is often a no-op on this unit; a wedge needs the external power plug")
	}
	out, err := a.runShell("sync; (sleep 1; reboot) &", 5*time.Second)
	return map[string]any{"output": string(out)}, err
}

func writeText(path, s string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(s), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// markIntent drops a one-shot marker the boot respawn loop consumes so a
// deliberate agent exit (restart/update) is not mis-counted as a crash and
// does not trigger a spurious A/B revert of a freshly-activated slot.
func (a *Agent) markIntent(kind string) {
	_ = os.WriteFile(filepath.Join(a.cfg.OTADir, "agent.intent"), []byte(kind+"\n"), 0o644)
}
