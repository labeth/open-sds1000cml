package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"open-sds/ota/internal/config"
)

func testAgent(t *testing.T) *Agent {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OTA_DIR", dir+"/ota")
	t.Setenv("OTA_SLOT_ROOT", dir+"/slots")
	t.Setenv("OTA_HEALTH_DIR", dir)
	t.Setenv("OTA_LISTEN", "") // don't bind in tests
	t.Setenv("OTA_NATS", "")
	return New(config.Load())
}

func TestDispatchUnknownCmd(t *testing.T) {
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{"cmd":"nope"}`))
	if resp.OK {
		t.Error("unknown cmd should not be OK")
	}
	if !strings.Contains(resp.Err, "unknown cmd") {
		t.Errorf("err = %q", resp.Err)
	}
}

func TestDispatchBadJSON(t *testing.T) {
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{not json`))
	if resp.OK || !strings.Contains(resp.Err, "bad request json") {
		t.Errorf("expected bad-json error, got %+v", resp)
	}
}

func TestDispatchPing(t *testing.T) {
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{"cmd":"ping"}`))
	if !resp.OK {
		t.Fatalf("ping failed: %s", resp.Err)
	}
	b, _ := json.Marshal(resp.Data)
	if !strings.Contains(string(b), "device") {
		t.Errorf("ping data missing device: %s", b)
	}
}

func TestDispatchPanicContained(t *testing.T) {
	// Register a handler that panics; Dispatch must contain it (the agent must
	// never die to a bad request — it holds the inherited fds).
	handlers["_boom"] = func(a *Agent, _ json.RawMessage) (any, error) { panic("kaboom") }
	defer delete(handlers, "_boom")
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{"cmd":"_boom"}`))
	if resp.OK || !strings.Contains(resp.Err, "panic") {
		t.Errorf("panic not contained: %+v", resp)
	}
}

func TestAppStartRequiresTakeover(t *testing.T) {
	a := testAgent(t)
	resp := a.Dispatch([]byte(`{"cmd":"app.start"}`))
	if resp.OK || !strings.Contains(resp.Err, "not taken over") {
		t.Errorf("app.start before takeover should be refused: %+v", resp)
	}
}

func TestTakeoverRefusedWithoutInheritedFD(t *testing.T) {
	a := testAgent(t)
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

func TestTakeoverDryRunReportsCandidates(t *testing.T) {
	a := testAgent(t)
	// Force an inherited fd so the dry-run passes the first gate; -1 would
	// refuse. We can't fake a real Gpmc fd, so only assert the refuse path
	// above; here we check dry-run plumbing when a fd is present.
	a.gpmcFD = 0 // stdin: present, harmless (dry-run reads nothing)
	resp := a.Dispatch([]byte(`{"cmd":"takeover","args":{"dry_run":true}}`))
	if !resp.OK {
		t.Fatalf("dry-run should succeed: %s", resp.Err)
	}
	b, _ := json.Marshal(resp.Data)
	if !strings.Contains(string(b), "dry-run") {
		t.Errorf("dry-run result missing marker: %s", b)
	}
}
