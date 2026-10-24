// ENGMODEL-OWNER-UNIT: FU-OTA-VXI11
package vxi11

import (
	"net"
	"testing"
	"time"
)

// One RPC through a pipe to an echoing peer: the takeover's SCPI round trip.
// TRLC-LINKS: REQ-SDS-100
func BenchmarkRPCCall(b *testing.B) {
	c, s := net.Pipe()
	defer c.Close()
	go func() {
		for {
			body, err := readRecord(s)
			if err != nil {
				return
			}
			writeRecord(s, acceptedReply(callXID(body), make([]byte, 64)))
		}
	}()
	args := make([]byte, 48)
	for b.Loop() {
		if _, err := rpcCall(c, time.Second, coreProg, coreVers, procDeviceWrite, args); err != nil {
			b.Fatal(err)
		}
	}
}
