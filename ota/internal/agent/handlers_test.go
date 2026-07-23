package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"open-sds/ota/internal/slots"
)

// mustData re-marshals a handler's Data so tests can decode it into the shape
// the otactl side would see on the wire.
func mustData[T any](t *testing.T, resp Response) T {
	t.Helper()
	b, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode data %s: %v", b, err)
	}
	return v
}

func TestHelpListsEveryRegisteredCommand(t *testing.T) {
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{"cmd":"help"}`))
	if !resp.OK {
		t.Fatalf("help: %s", resp.Err)
	}
	got := mustData[struct {
		Commands []string `json:"commands"`
	}](t, resp)
	want := make([]string, 0, len(handlers))
	for k := range handlers {
		want = append(want, k)
	}
	sort.Strings(want)
	if len(got.Commands) != len(want) {
		t.Fatalf("help lists %d commands, registry has %d:\n got %v\nwant %v",
			len(got.Commands), len(want), got.Commands, want)
	}
	for i := range want {
		if got.Commands[i] != want[i] {
			t.Errorf("commands[%d] = %q, want %q", i, got.Commands[i], want[i])
		}
	}
}

func TestStatusHandlerShape(t *testing.T) {
	a := testAgent(t)
	// Round-trip through DispatchJSON: the exact bytes otactl would parse.
	raw := a.DispatchJSON([]byte(`{"cmd":"status"}`))
	var resp struct {
		OK   bool `json:"ok"`
		Data struct {
			Device    string         `json:"device"`
			TakenOver bool           `json:"taken_over"`
			FDs       map[string]int `json:"inherited_fds"`
			App       struct {
				Running bool `json:"running"`
			} `json:"app"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("bad status wire %s: %v", raw, err)
	}
	if !resp.OK {
		t.Fatalf("status not ok: %s", raw)
	}
	if resp.Data.Device != a.cfg.DeviceID {
		t.Errorf("device = %q, want %q", resp.Data.Device, a.cfg.DeviceID)
	}
	if resp.Data.TakenOver {
		t.Error("fresh agent must not report taken_over")
	}
	if fd, ok := resp.Data.FDs["gpmc"]; !ok || fd != -1 {
		t.Errorf("inherited_fds[gpmc] = %d,%v — want -1 on the dev machine", fd, ok)
	}
}

func TestLogsHandler(t *testing.T) {
	a := testAgent(t)
	logDir := a.cfg.LogDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(logDir, "agent.log"), []byte("agent says hi\n"), 0o644)
	os.WriteFile(filepath.Join(logDir, "boot.log"), []byte("0123456789boot!\n"), 0o644)

	resp := a.Dispatch([]byte(`{"cmd":"logs"}`))
	if !resp.OK {
		t.Fatalf("logs: %s", resp.Err)
	}
	d := mustData[struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}](t, resp)
	if !strings.Contains(d.Text, "agent says hi") {
		t.Errorf("default logs = %q", d.Text)
	}

	resp = a.Dispatch([]byte(`{"cmd":"logs","args":{"file":"boot","tail":6}}`))
	d = mustData[struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}](t, resp)
	if d.Text != "boot!\n" {
		t.Errorf("boot tail = %q, want the last 6 bytes", d.Text)
	}

	resp = a.Dispatch([]byte(`{"cmd":"logs","args":{"file":"/definitely/not/here"}}`))
	if resp.OK {
		t.Error("missing log file must error")
	}
}

func TestExecHandler(t *testing.T) {
	a := testAgent(t)

	resp := a.Dispatch([]byte(`{"cmd":"exec","args":{"argv":["/bin/echo","hi"]}}`))
	if !resp.OK {
		t.Fatalf("exec echo: %s", resp.Err)
	}
	d := mustData[struct {
		Exit   int    `json:"exit"`
		Output string `json:"output"`
	}](t, resp)
	if d.Exit != 0 || d.Output != "hi\n" {
		t.Errorf("exec = %+v", d)
	}

	resp = a.Dispatch([]byte(`{"cmd":"exec","args":{"argv":[]}}`))
	if resp.OK || !strings.Contains(resp.Err, "empty argv") {
		t.Errorf("empty argv: %+v", resp)
	}

	// Failing command: the envelope must carry BOTH the error and the exit
	// data (otactl shows the exit code even on failure).
	resp = a.Dispatch([]byte(`{"cmd":"exec","args":{"argv":["/bin/sh","-c","exit 4"]}}`))
	if resp.OK {
		t.Fatal("exit 4 should not be ok")
	}
	fail := mustData[struct {
		Exit int `json:"exit"`
	}](t, resp)
	if fail.Exit != 4 {
		t.Errorf("exit = %d, want 4 alongside err %q", fail.Exit, resp.Err)
	}
}

func TestShHandler(t *testing.T) {
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{"cmd":"sh","args":{"script":"printf abc"}}`))
	if !resp.OK {
		t.Fatalf("sh: %s", resp.Err)
	}
	d := mustData[struct {
		Output string `json:"output"`
	}](t, resp)
	if d.Output != "abc" {
		t.Errorf("output = %q", d.Output)
	}
	resp = a.Dispatch([]byte(`{"cmd":"sh","args":{"script":"   "}}`))
	if resp.OK || !strings.Contains(resp.Err, "empty script") {
		t.Errorf("empty script: %+v", resp)
	}
}

func TestGetHandler(t *testing.T) {
	a := testAgent(t)
	path := filepath.Join(t.TempDir(), "payload")
	os.WriteFile(path, []byte("0123456789"), 0o644)

	resp := a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"get","args":{"path":%q,"offset":2,"len":4}}`, path)))
	if !resp.OK {
		t.Fatalf("get: %s", resp.Err)
	}
	d := mustData[struct {
		Data   string `json:"data"`
		Offset int64  `json:"offset"`
		EOF    bool   `json:"eof"`
		Size   int64  `json:"size"`
	}](t, resp)
	raw, _ := base64.StdEncoding.DecodeString(d.Data)
	if string(raw) != "2345" || d.EOF || d.Size != 10 || d.Offset != 2 {
		t.Errorf("get mid-file = %+v (%q)", d, raw)
	}

	resp = a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"get","args":{"path":%q,"offset":8,"len":4}}`, path)))
	tail := mustData[struct {
		Data string `json:"data"`
		EOF  bool   `json:"eof"`
	}](t, resp)
	raw, _ = base64.StdEncoding.DecodeString(tail.Data)
	if string(raw) != "89" || !tail.EOF {
		t.Errorf("get tail = %+v (%q)", tail, raw)
	}

	resp = a.Dispatch([]byte(`{"cmd":"get","args":{"path":"/definitely/not/here"}}`))
	if resp.OK {
		t.Error("get of a missing file must error")
	}
}

func TestPutHandlersRejectBadInput(t *testing.T) {
	a := testAgent(t)

	resp := a.Dispatch([]byte(`{"cmd":"put.begin","args":{"dest":"relative/path","size":10}}`))
	if resp.OK || !strings.Contains(resp.Err, "absolute") {
		t.Errorf("relative dest: %+v", resp)
	}
	resp = a.Dispatch([]byte(`{"cmd":"put.begin","args":{}}`))
	if resp.OK || !strings.Contains(resp.Err, "dest required") {
		t.Errorf("missing dest: %+v", resp)
	}
	resp = a.Dispatch([]byte(`{"cmd":"put.chunk","args":{"id":"up-nope","data":"aGk=","offset":0}}`))
	if resp.OK || !strings.Contains(resp.Err, "unknown session") {
		t.Errorf("unknown session: %+v", resp)
	}
	resp = a.Dispatch([]byte(`{"cmd":"put.commit","args":{"id":"up-nope"}}`))
	if resp.OK || !strings.Contains(resp.Err, "unknown session") {
		t.Errorf("commit unknown session: %+v", resp)
	}

	// Bad base64 on a real session must fail the chunk, not the agent.
	dest := filepath.Join(t.TempDir(), "x.bin")
	resp = a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"put.begin","args":{"dest":%q}}`, dest)))
	if !resp.OK {
		t.Fatalf("put.begin: %s", resp.Err)
	}
	id := mustData[struct {
		ID string `json:"id"`
	}](t, resp).ID
	resp = a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"put.chunk","args":{"id":%q,"data":"!!not-base64!!","offset":0}}`, id)))
	if resp.OK || !strings.Contains(resp.Err, "base64") {
		t.Errorf("bad base64: %+v", resp)
	}
}

func TestAppUpdateInstallsIntoInactiveSlot(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	src := filepath.Join(t.TempDir(), "newapp")
	payload := []byte("#!/bin/sh\nexit 0\n")
	os.WriteFile(src, payload, 0o755)
	sum, err := slots.FileSHA256(src)
	if err != nil {
		t.Fatal(err)
	}

	// Active defaults to A, so the update must land in B and flip active.
	resp := a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"app.update","args":{"src":%q,"sha256":%q}}`, src, sum)))
	if !resp.OK {
		t.Fatalf("app.update: %s", resp.Err)
	}
	d := mustData[struct {
		Slot   string `json:"slot"`
		SHA    string `json:"sha256"`
		Active string `json:"active"`
	}](t, resp)
	if d.Slot != slots.SlotB || d.Active != slots.SlotB || d.SHA != sum {
		t.Errorf("app.update = %+v", d)
	}
	if got := a.store.Active(); got != slots.SlotB {
		t.Errorf("active = %s, want B", got)
	}
	b, _ := os.ReadFile(a.store.BinPath(slots.SlotB))
	if string(b) != string(payload) {
		t.Error("installed binary does not match the source")
	}

	// sha mismatch must refuse before touching anything.
	resp = a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"app.update","args":{"src":%q,"sha256":"00"}}`, src)))
	if resp.OK || !strings.Contains(resp.Err, "sha256 mismatch") {
		t.Errorf("sha mismatch: %+v", resp)
	}
	resp = a.Dispatch([]byte(`{"cmd":"app.update","args":{}}`))
	if resp.OK || !strings.Contains(resp.Err, "src required") {
		t.Errorf("missing src: %+v", resp)
	}
}

// A second app.update that arrives before the first has gone stable (active !=
// confirmed) must overwrite the still-un-confirmed slot, NOT the confirmed
// known-good binary the rollback ladder falls back to.
func TestAppUpdateNeverClobbersConfirmedKnownGood(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init() // provisions confirmed = active = A

	// A holds the known-good factory binary; active == confirmed == A.
	knownGood := []byte("#!/bin/sh\necho known-good\nexit 0\n")
	if err := os.WriteFile(a.store.BinPath(slots.SlotA), knownGood, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := a.store.Confirmed(); got != slots.SlotA {
		t.Fatalf("precondition: confirmed = %s, want A", got)
	}

	update := func(content string) string {
		t.Helper()
		src := filepath.Join(t.TempDir(), "app")
		if err := os.WriteFile(src, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		resp := a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"app.update","args":{"src":%q}}`, src)))
		if !resp.OK {
			t.Fatalf("app.update: %s", resp.Err)
		}
		return mustData[struct {
			Slot string `json:"slot"`
		}](t, resp).Slot
	}

	// First update lands in B (Other(confirmed A)); active flips to B, but the
	// slot has not run stably yet so confirmed stays A.
	if slot := update("#!/bin/sh\nexit 0\n# v1\n"); slot != slots.SlotB {
		t.Fatalf("first update slot = %s, want B", slot)
	}
	if got := a.store.Active(); got != slots.SlotB {
		t.Fatalf("active = %s, want B after first update", got)
	}
	if got := a.store.Confirmed(); got != slots.SlotA {
		t.Fatalf("confirmed = %s, want A (first update not yet stable)", got)
	}

	// Second update within the stable window. The naive Other(Active()) choice
	// would target A and destroy the known-good binary; the fix targets B again.
	if slot := update("#!/bin/sh\nexit 0\n# v2\n"); slot != slots.SlotB {
		t.Fatalf("second update slot = %s, want B (must not clobber confirmed A)", slot)
	}

	// The confirmed known-good A binary must be byte-for-byte intact.
	gotA, err := os.ReadFile(a.store.BinPath(slots.SlotA))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotA) != string(knownGood) {
		t.Errorf("confirmed slot A was overwritten by the second update:\n got %q\nwant %q", gotA, knownGood)
	}
	if got := a.store.Confirmed(); got != slots.SlotA {
		t.Errorf("confirmed = %s, want A preserved", got)
	}
}

// On a fresh stick with no confirmed pointer file, Store.Init anchors confirmed
// to the active slot so Confirmed() cannot silently follow active after the
// first update flips it (which would defeat the no-clobber guard above).
func TestAppUpdateConfirmedAnchoredOnFreshStick(t *testing.T) {
	a := testAgent(t)
	// Simulate a truly fresh provisioning: no confirmed file on disk.
	_ = os.Remove(filepath.Join(a.store.Root(), "confirmed"))
	_ = a.store.Init() // must write confirmed = A
	if _, err := os.Stat(filepath.Join(a.store.Root(), "confirmed")); err != nil {
		t.Fatalf("Init must materialize a confirmed pointer on a fresh stick: %v", err)
	}
	if got := a.store.Confirmed(); got != slots.SlotA {
		t.Fatalf("confirmed = %s, want A", got)
	}

	// First update flips active to B. Because confirmed is a real file (A), a
	// follow-up update still targets B and never A.
	src := filepath.Join(t.TempDir(), "app")
	os.WriteFile(src, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	_ = a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"app.update","args":{"src":%q}}`, src)))
	if got := a.store.Active(); got != slots.SlotB {
		t.Fatalf("active = %s, want B", got)
	}
	if got := a.store.Confirmed(); got != slots.SlotA {
		t.Errorf("confirmed = %s, want A (must not follow active once flipped)", got)
	}
}

func TestAppActivate(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()

	resp := a.Dispatch([]byte(`{"cmd":"app.activate","args":{"slot":"B"}}`))
	if resp.OK || !strings.Contains(resp.Err, "no binary") {
		t.Errorf("activating an empty slot: %+v", resp)
	}

	writeSlotBinary(t, a, slots.SlotB, "exit 0")
	// Lower-case + padded input must normalize.
	resp = a.Dispatch([]byte(`{"cmd":"app.activate","args":{"slot":" b "}}`))
	if !resp.OK {
		t.Fatalf("app.activate: %s", resp.Err)
	}
	if got := a.store.Active(); got != slots.SlotB {
		t.Errorf("active = %s, want B", got)
	}
}

func TestAppInstallEmergency(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init()
	resp := a.Dispatch([]byte(`{"cmd":"app.install-emergency","args":{}}`))
	if resp.OK || !strings.Contains(resp.Err, "src required") {
		t.Errorf("missing src: %+v", resp)
	}
	src := filepath.Join(t.TempDir(), "emerg")
	os.WriteFile(src, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	resp = a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"app.install-emergency","args":{"src":%q}}`, src)))
	if !resp.OK {
		t.Fatalf("install-emergency: %s", resp.Err)
	}
	if !a.store.HasBinary(slots.SlotEmergency) {
		t.Error("emergency binary not installed")
	}
}

// agent.update's happy path exits the process by design (startup.sh respawns
// the new slot) — only its refusal gates are testable in-process.
func TestAgentUpdateRefusalGates(t *testing.T) {
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{"cmd":"agent.update","args":{}}`))
	if resp.OK || !strings.Contains(resp.Err, "src required") {
		t.Errorf("missing src: %+v", resp)
	}
	src := filepath.Join(t.TempDir(), "agent-new")
	os.WriteFile(src, []byte("elf-ish"), 0o755)
	resp = a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"agent.update","args":{"src":%q,"sha256":"00"}}`, src)))
	if resp.OK || !strings.Contains(resp.Err, "sha256 mismatch") {
		t.Errorf("sha mismatch: %+v", resp)
	}
	resp = a.Dispatch([]byte(`{"cmd":"agent.update","args":{"src":"/definitely/not/here","sha256":"00"}}`))
	if resp.OK {
		t.Errorf("missing src file: %+v", resp)
	}
}

func TestRebootRequiresConfirm(t *testing.T) {
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{"cmd":"reboot"}`))
	if resp.OK || !strings.Contains(resp.Err, "confirm") {
		t.Errorf("reboot without confirm: %+v", resp)
	}
}

func TestRestoreFactoryGates(t *testing.T) {
	a := testAgent(t)
	if err := a.st.update(func(s *State) { s.TakenOver = true }); err != nil {
		t.Fatal(err)
	}
	resp := a.Dispatch([]byte(`{"cmd":"restore-factory"}`))
	if resp.OK || !strings.Contains(resp.Err, "still taken over") {
		t.Errorf("restore-factory while taken over: %+v", resp)
	}
	if err := a.st.update(func(s *State) { s.TakenOver = false }); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "no-factory-app")
	resp = a.Dispatch([]byte(fmt.Sprintf(`{"cmd":"restore-factory","args":{"path":%q}}`, missing)))
	if resp.OK || !strings.Contains(resp.Err, "not found") {
		t.Errorf("restore-factory with a missing binary: %+v", resp)
	}
}

func TestAppLifecycleHandlersDriveSupervisor(t *testing.T) {
	a := testAgent(t)
	_ = a.store.Init() // no slot binaries: the supervisor only idles
	go a.superviseLoop()
	defer a.Stop()

	// Not taken over: start/restart are refused, stop is always allowed.
	resp := a.Dispatch([]byte(`{"cmd":"app.start"}`))
	if resp.OK || !strings.Contains(resp.Err, "not taken over") {
		t.Errorf("app.start before takeover: %+v", resp)
	}
	resp = a.Dispatch([]byte(`{"cmd":"app.restart"}`))
	if resp.OK || !strings.Contains(resp.Err, "not taken over") {
		t.Errorf("app.restart before takeover: %+v", resp)
	}
	resp = a.Dispatch([]byte(`{"cmd":"app.stop"}`))
	if !resp.OK {
		t.Fatalf("app.stop: %s", resp.Err)
	}
	if !pausedOf(a) {
		t.Error("app.stop must pause the supervisor")
	}

	// Taken over (state only; no factory process involved): start resumes.
	if err := a.st.update(func(s *State) { s.TakenOver = true }); err != nil {
		t.Fatal(err)
	}
	resp = a.Dispatch([]byte(`{"cmd":"app.start"}`))
	if !resp.OK {
		t.Fatalf("app.start: %s", resp.Err)
	}
	if pausedOf(a) {
		t.Error("app.start must resume the supervisor")
	}
	resp = a.Dispatch([]byte(`{"cmd":"app.restart"}`))
	if !resp.OK {
		t.Fatalf("app.restart: %s", resp.Err)
	}
}
