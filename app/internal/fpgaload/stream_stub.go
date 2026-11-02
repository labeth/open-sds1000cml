//go:build !withstreambitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// Stream is the embedded stream image: none in a build without it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-035
func Stream() []byte { return nil }
