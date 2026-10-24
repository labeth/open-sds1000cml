// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package agent

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TRLC-LINKS: REQ-SDS-113
func checkSplitRequest(t *testing.T, raw []byte) {
	got, ok := splitRequest(raw)
	if !ok {
		return
	}
	var want Request
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("%q: split accepted what json rejects: %v", raw, err)
	}
	if got.Cmd != want.Cmd || !bytes.Equal(got.Args, want.Args) {
		t.Fatalf("%q: split %q %q, json %q %q", raw, got.Cmd, got.Args, want.Cmd, want.Args)
	}
}

// TRLC-LINKS: REQ-SDS-113
func TestSplitRequestMatchesJSON(t *testing.T) {
	for _, s := range []string{
		`{"cmd":"ping"}`, `{"cmd":"ping"}` + "\n", `{"cmd":"ping","args":null}`,
		`{"cmd":"put.chunk","args":{"id":"up-1","data":"AAEC","offset":0}}`,
		`{"cmd":"x","args":{},"y":1}`, `{"cmd":"x","args":}`, `{"cmd":"a\"b"}`,
		`{"cmd":"a\\u0041"}`, `{"cmd":"x","args":[1,2]}` + " \r\n", `{"cmd":"x","args":"s"}`,
		`{"cmd":"x","args":{"a":"}"}}`, `{"cmd":"x"`, `{"cmd":"x","args":{}`,
		"{\"cmd\":\"\xff\"}", `{"cmd":"","args":1}`,
	} {
		checkSplitRequest(t, []byte(s))
	}
	if r, ok := splitRequest([]byte(`{"cmd":"put.chunk","args":{"id":"u"}}`)); !ok || r.Cmd != "put.chunk" {
		t.Fatalf("canonical request took the slow path: %+v %v", r, ok)
	}
}

// TRLC-LINKS: REQ-SDS-113
func FuzzSplitRequest(f *testing.F) {
	f.Add([]byte(`{"cmd":"ping","args":{"a":1}}`))
	f.Fuzz(func(t *testing.T, raw []byte) { checkSplitRequest(t, raw) })
}
