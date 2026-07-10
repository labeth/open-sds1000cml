package rpcproto

import (
	"bytes"
	"encoding/json"
	"testing"
)

// The wire format is load-bearing: the agent keeps mirrored Request/Response
// structs (internal/agent/rpc.go) rather than importing this package, so these
// golden strings pin the canonical encoding both sides must keep speaking.
// If a tag or field changes here, every deployment path breaks — this test is
// meant to fail loudly first.
func TestRequestGoldenWire(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want string
	}{
		{"bare", Request{Cmd: "ping"}, `{"cmd":"ping"}`},
		{"empty cmd", Request{}, `{"cmd":""}`},
		{"object args", Request{Cmd: "logs", Args: map[string]any{"file": "boot", "tail": 4096}},
			`{"cmd":"logs","args":{"file":"boot","tail":4096}}`},
		{"nested array args", Request{Cmd: "exec", Args: map[string]any{"argv": []string{"/bin/echo", "hi"}, "timeout_s": 5}},
			`{"cmd":"exec","args":{"argv":["/bin/echo","hi"],"timeout_s":5}}`},
		{"bool args", Request{Cmd: "takeover", Args: map[string]any{"dry_run": true}},
			`{"cmd":"takeover","args":{"dry_run":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.req)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Errorf("wire = %s, want %s", b, tc.want)
			}
			var back Request
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("unmarshal own encoding: %v", err)
			}
			if back.Cmd != tc.req.Cmd {
				t.Errorf("cmd round-trip: got %q, want %q", back.Cmd, tc.req.Cmd)
			}
		})
	}
}

func TestResponseGoldenWire(t *testing.T) {
	cases := []struct {
		name string
		resp Response
		want string
	}{
		{"ok bare", Response{OK: true}, `{"ok":true}`},
		{"ok with data", Response{OK: true, Data: json.RawMessage(`{"x":1}`)}, `{"ok":true,"data":{"x":1}}`},
		{"error", Response{OK: false, Err: "boom"}, `{"ok":false,"err":"boom"}`},
		// The agent's Dispatch keeps handler Data alongside Err (e.g. exec exit
		// codes on failure) — the envelope must carry both.
		{"error with data", Response{OK: false, Err: "exit status 4", Data: json.RawMessage(`{"exit":4}`)},
			`{"ok":false,"err":"exit status 4","data":{"exit":4}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.resp)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Errorf("wire = %s, want %s", b, tc.want)
			}
			var back Response
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("unmarshal own encoding: %v", err)
			}
			if back.OK != tc.resp.OK || back.Err != tc.resp.Err {
				t.Errorf("round-trip: got %+v, want %+v", back, tc.resp)
			}
			if !bytes.Equal(compact(t, back.Data), compact(t, tc.resp.Data)) {
				t.Errorf("data round-trip: got %s, want %s", back.Data, tc.resp.Data)
			}
		})
	}
}

// TestFullCommandSurfaceRoundTrip walks one realistic request per registered
// agent command (the full deployment surface) through encode -> decode ->
// re-encode and requires a stable fixed point, so any envelope drift that
// would corrupt a command in flight shows up here.
func TestFullCommandSurfaceRoundTrip(t *testing.T) {
	wire := []string{
		`{"cmd":"help"}`,
		`{"cmd":"ping"}`,
		`{"cmd":"status"}`,
		`{"cmd":"logs","args":{"file":"boot","tail":4096}}`,
		`{"cmd":"exec","args":{"argv":["/bin/echo","hi"],"dir":"/","timeout_s":5}}`,
		`{"cmd":"sh","args":{"script":"echo hi","timeout_s":5}}`,
		`{"cmd":"put.begin","args":{"dest":"/x/app.upload","mode":493,"sha256":"ab12","size":1000}}`,
		`{"cmd":"put.chunk","args":{"data":"aGVsbG8=","id":"up-1","offset":0}}`,
		`{"cmd":"put.commit","args":{"id":"up-1"}}`,
		`{"cmd":"get","args":{"len":65536,"offset":0,"path":"/etc/hosts"}}`,
		`{"cmd":"app.start"}`,
		`{"cmd":"app.stop"}`,
		`{"cmd":"app.restart"}`,
		`{"cmd":"app.update","args":{"sha256":"deadbeef","src":"/stage/app.upload"}}`,
		`{"cmd":"app.activate","args":{"slot":"B"}}`,
		`{"cmd":"app.install-emergency","args":{"src":"/stage/app.upload"}}`,
		`{"cmd":"agent.update","args":{"sha256":"deadbeef","src":"/stage/agent.upload"}}`,
		`{"cmd":"agent.restart"}`,
		`{"cmd":"takeover","args":{"dry_run":true,"force":false}}`,
		`{"cmd":"untakeover"}`,
		`{"cmd":"restore-factory","args":{"dir":"/usr/bin/siglent","path":"/usr/bin/siglent/SDS1000_arm.app"}}`,
		`{"cmd":"probe","args":{"read_gpmc":false}}`,
		`{"cmd":"reboot","args":{"confirm":true}}`,
	}
	for _, w := range wire {
		var req Request
		if err := json.Unmarshal([]byte(w), &req); err != nil {
			t.Errorf("decode %s: %v", w, err)
			continue
		}
		b, err := json.Marshal(req)
		if err != nil {
			t.Errorf("re-encode %s: %v", w, err)
			continue
		}
		// The literals above are written in canonical form (compact, keys
		// sorted), so re-encoding must reproduce them byte for byte.
		if string(b) != w {
			t.Errorf("wire drift:\n got %s\nwant %s", b, w)
		}
	}
}

func TestResponseDecodeAgentShapes(t *testing.T) {
	// Literal agent-produced responses (DispatchJSON output shapes).
	var ok Response
	if err := json.Unmarshal([]byte(`{"ok":true,"data":{"commands":["ping"],"device":"sds-x"}}`), &ok); err != nil {
		t.Fatal(err)
	}
	if !ok.OK || ok.Err != "" || len(ok.Data) == 0 {
		t.Errorf("bad decode: %+v", ok)
	}
	var bad Response
	if err := json.Unmarshal([]byte(`{"ok":false,"err":"unknown cmd nope (try \"help\")"}`), &bad); err != nil {
		t.Fatal(err)
	}
	if bad.OK || bad.Err == "" || bad.Data != nil {
		t.Errorf("bad decode: %+v", bad)
	}
}

func compact(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compact %s: %v", raw, err)
	}
	return buf.Bytes()
}

// FuzzRequestDecode hammers the request side of the envelope: anything that
// decodes must re-encode, and the encoding must be a fixed point (decode ->
// encode -> decode -> encode yields identical bytes with Cmd preserved). A
// violation means the same request can mean two different things depending on
// how many hops it took — protocol drift between otactl and the agent.
func FuzzRequestDecode(f *testing.F) {
	f.Add([]byte(`{"cmd":"ping"}`))
	f.Add([]byte(`{"cmd":"logs","args":{"file":"boot","tail":4096}}`))
	f.Add([]byte(`{"cmd":"exec","args":{"argv":["/bin/echo","hi"],"timeout_s":5}}`))
	f.Add([]byte(`{"cmd":"put.chunk","args":{"id":"up-1","offset":131072,"data":"aGVsbG8gd29ybGQ="}}`))
	f.Add([]byte(`{"cmd":"put.begin","args":{"dest":"/x","size":9007199254740993,"sha256":"ff","mode":493}}`))
	f.Add([]byte(`{"cmd":"takeover","args":{"dry_run":true,"force":false}}`))
	f.Add([]byte(`{"cmd":"app.activate","args":{"slot":"B"}}`))
	f.Add([]byte(`{"cmd":"x","args":[1,2.5,-3e7,"s",null,true,{"k":[]}]}`))
	f.Add([]byte(`{"cmd":"x","args":"just a string"}`))
	f.Add([]byte(`{"cmd":"x","args":null}`))
	f.Add([]byte(`{"cmd":""}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte("{\"cmd\":\"\\u0000\\uffff\u2603\",\"args\":{\"\xff\":\"\xfe\"}}"))
	f.Add([]byte(`{"args":{"dup":1,"dup":2},"cmd":"dup"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var req Request
		if err := json.Unmarshal(data, &req); err != nil {
			return // invalid JSON is allowed to fail; it must not crash
		}
		b1, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("decodable request failed to re-encode: %v (input %q)", err, data)
		}
		var req2 Request
		if err := json.Unmarshal(b1, &req2); err != nil {
			t.Fatalf("own encoding does not decode: %v (wire %s)", err, b1)
		}
		if req2.Cmd != req.Cmd {
			t.Fatalf("cmd drift: %q -> %q (input %q)", req.Cmd, req2.Cmd, data)
		}
		b2, err := json.Marshal(req2)
		if err != nil {
			t.Fatalf("second encode failed: %v", err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("encoding not a fixed point:\n b1=%s\n b2=%s\n input=%q", b1, b2, data)
		}
	})
}

// FuzzResponseDecode is the same fixed-point property for the response side,
// including the json.RawMessage Data passthrough.
func FuzzResponseDecode(f *testing.F) {
	f.Add([]byte(`{"ok":true}`))
	f.Add([]byte(`{"ok":true,"data":{"device":"sds-1","time_unix":1751600000,"agent_slot":"A"}}`))
	f.Add([]byte(`{"ok":false,"err":"unknown cmd nope (try \"help\")"}`))
	f.Add([]byte(`{"ok":false,"err":"exit status 4","data":{"exit":4,"output":"..."}}`))
	f.Add([]byte(`{"ok":true,"data":{"data":"aGVsbG8=","eof":true,"offset":0,"size":5}}`))
	f.Add([]byte(`{"ok":true,"data":[{"pid":1,"comm":"init","exe":""}]}`))
	f.Add([]byte(`{"ok":true,"data": {  "spaced" : [ 1 , 2 ] } }`))
	f.Add([]byte(`{"ok":true,"data":null}`))
	f.Add([]byte(`{"data":{"first":true},"ok":true,"err":""}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var resp Response
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}
		b1, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("decodable response failed to re-encode: %v (input %q)", err, data)
		}
		var resp2 Response
		if err := json.Unmarshal(b1, &resp2); err != nil {
			t.Fatalf("own encoding does not decode: %v (wire %s)", err, b1)
		}
		if resp2.OK != resp.OK || resp2.Err != resp.Err {
			t.Fatalf("field drift: %+v -> %+v (input %q)", resp, resp2, data)
		}
		b2, err := json.Marshal(resp2)
		if err != nil {
			t.Fatalf("second encode failed: %v", err)
		}
		if !bytes.Equal(b1, b2) {
			t.Fatalf("encoding not a fixed point:\n b1=%s\n b2=%s\n input=%q", b1, b2, data)
		}
	})
}
