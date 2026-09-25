// Package rpcproto is the shared request/response envelope for the OTA control
// protocol, imported by both the agent and the otactl host CLI so the wire
// format has a single definition.
// ENGMODEL-OWNER-UNIT: FU-OTA-RPCPROTO
package rpcproto

import "encoding/json"

// TRLC-LINKS: REQ-SDS-101
type Request struct {
	Cmd  string `json:"cmd"`
	Args any    `json:"args,omitempty"`
}

// TRLC-LINKS: REQ-SDS-101
type Response struct {
	OK   bool            `json:"ok"`
	Err  string          `json:"err,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}
