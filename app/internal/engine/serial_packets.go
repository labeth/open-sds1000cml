// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "open-sds/app/internal/decode"

// matchPackets keeps a payload pattern inside one decoder-delimited packet.
// Check the whole packet before matching: CRC/parity errors can follow its data.
// This only enforces errors emitted by the decoder; it does not add validation
// for protocol fields that the decoder does not inspect.
// TRLC-LINKS: REQ-SDS-013
func matchPackets(sp []decode.Span, want []int) (bool, int) {
	start := 0
	bad := false
	for i := 0; i <= len(sp); i++ {
		boundary := i == len(sp)
		if !boundary {
			switch sp[i].Kind {
			case "gap", "start", "stop", "sof", "sync":
				boundary = true
			}
		}
		if boundary {
			if !bad {
				if ok, anchor := matchBytes(sp[start:i], want); ok {
					return true, anchor
				}
			}
			start, bad = i+1, false
		} else if sp[i].Kind == "frame-error" || sp[i].Kind == "parity-error" {
			bad = true
		}
	}
	return false, -1
}
