package agent

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
)

// The RPC envelope shared by the NATS and TCP transports.
//
//	request:  {"cmd": "exec", "args": {...}}
//	response: {"ok": true, "data": {...}} | {"ok": false, "err": "..."}
type Request struct {
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args,omitempty"`
}

type Response struct {
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
	Data any    `json:"data,omitempty"`
}

type handlerFn func(a *Agent, args json.RawMessage) (any, error)

// Dispatch runs one request and always returns a marshalable response; a
// panicking handler is contained (the agent must never die to a bad request).
func (a *Agent) Dispatch(raw []byte) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			a.log.Printf("rpc panic: %v\n%s", r, debug.Stack())
			resp = Response{OK: false, Err: fmt.Sprintf("panic: %v", r)}
		}
	}()

	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Response{OK: false, Err: "bad request json: " + err.Error()}
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

func (a *Agent) DispatchJSON(raw []byte) []byte {
	resp := a.Dispatch(raw)
	b, err := json.Marshal(resp)
	if err != nil {
		// Data contained something unmarshalable; degrade rather than drop.
		b, _ = json.Marshal(Response{OK: false, Err: "marshal response: " + err.Error()})
	}
	return b
}

func decodeArgs[T any](args json.RawMessage) (T, error) {
	var v T
	if len(args) == 0 {
		return v, nil
	}
	err := json.Unmarshal(args, &v)
	return v, err
}
