// ENGMODEL-OWNER-UNIT: FU-OTA-AGENT
package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"unicode/utf8"
)

// The RPC envelope shared by the NATS and TCP transports.
//
//	request:  {"cmd": "exec", "args": {...}}
//	response: {"ok": true, "data": {...}} | {"ok": false, "err": "..."}
//
// TRLC-LINKS: REQ-SDS-113
type Request struct {
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args,omitempty"`
}

// TRLC-LINKS: REQ-SDS-113
type Response struct {
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
	Data any    `json:"data,omitempty"`
}

// TRLC-LINKS: REQ-SDS-113
type handlerFn func(a *Agent, args json.RawMessage) (any, error)

// Dispatch runs one request and always returns a marshalable response; a
// panicking handler is contained (the agent must never die to a bad request).
// TRLC-LINKS: REQ-SDS-113
func (a *Agent) Dispatch(raw []byte) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			a.log.Printf("rpc panic: %v\n%s", r, debug.Stack())
			resp = Response{OK: false, Err: fmt.Sprintf("panic: %v", r)}
		}
	}()

	req, ok := splitRequest(raw)
	if !ok {
		if err := json.Unmarshal(raw, &req); err != nil {
			return Response{OK: false, Err: "bad request json: " + err.Error()}
		}
	}
	h, ok := handlers[req.Cmd]
	if !ok {
		return Response{OK: false, Err: "unknown cmd " + req.Cmd + " (try \"help\")"}
	}
	data, err := h(a, req.Args)
	if err != nil {
		return Response{OK: false, Err: err.Error(), Data: data}
	}
	return Response{OK: true, Data: data}
}

// splitRequest takes a request in the form otactl marshals,
// {"cmd":"<name>"} or {"cmd":"<name>","args":<json>}, without decoding the
// args: a file chunk is otherwise scanned twice before its handler parses it
// again. Anything else (escapes, other spacing or key order) reports !ok for
// the full decode, and invalid args still fail as bad request json.
// TRLC-LINKS: REQ-SDS-113
func splitRequest(raw []byte) (Request, bool) {
	const prefix, argsKey = `{"cmd":"`, `,"args":`
	if !bytes.HasPrefix(raw, []byte(prefix)) {
		return Request{}, false
	}
	rest := raw[len(prefix):]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return Request{}, false
	}
	cmd := rest[:end]
	for _, c := range cmd {
		if c < 0x20 || c == '\\' {
			return Request{}, false
		}
	}
	if !utf8.Valid(cmd) {
		return Request{}, false
	}
	rest = bytes.TrimRight(rest[end+1:], " \t\r\n")
	if len(rest) == 1 && rest[0] == '}' {
		return Request{Cmd: string(cmd)}, true
	}
	if !bytes.HasPrefix(rest, []byte(argsKey)) || rest[len(rest)-1] != '}' {
		return Request{}, false
	}
	args := bytes.Trim(rest[len(argsKey):len(rest)-1], " \t\r\n")
	if !json.Valid(args) {
		return Request{}, false
	}
	return Request{Cmd: string(cmd), Args: args}, true
}

// TRLC-LINKS: REQ-SDS-113
func (a *Agent) DispatchJSON(raw []byte) []byte {
	resp := a.Dispatch(raw)
	b, err := json.Marshal(resp)
	if err != nil {
		// Data contained something unmarshalable; degrade rather than drop.
		b, _ = json.Marshal(Response{OK: false, Err: "marshal response: " + err.Error()})
	}
	return b
}

// TRLC-LINKS: REQ-SDS-113
func decodeArgs[T any](args json.RawMessage) (T, error) {
	var v T
	if len(args) == 0 {
		return v, nil
	}
	err := json.Unmarshal(args, &v)
	return v, err
}
