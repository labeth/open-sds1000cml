//go:build !withstackbitstream

// ENGMODEL-OWNER-UNIT: FU-APP-FPGALOAD

package fpgaload

// Stack is the embedded stack image: none in a build without it.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-141
func Stack() []byte { return nil }
