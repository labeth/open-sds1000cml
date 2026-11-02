// Package rpcproto is the shared request/response envelope for the OTA control
// protocol, imported by both the agent and the otactl host CLI so the wire
// format has a single definition.
// ENGMODEL-OWNER-UNIT: FU-OTA-RPCPROTO
package rpcproto

import "encoding/json"

// Request is one call: the command name and its arguments object.
// TRLC-LINKS: REQ-SDS-101
type Request struct {
	Cmd  string `json:"cmd"`
	Args any    `json:"args,omitempty"`
}

// Response is the device's reply, Data left encoded for the caller to decode.
// TRLC-LINKS: REQ-SDS-101
type Response struct {
	OK   bool            `json:"ok"`
	Err  string          `json:"err,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}
