package cal

// Checksum computes the word-0 value for a scrambled payload (tooling).
// TRLC-LINKS: REQ-SDS-016
func Checksum(payload []byte) uint32 {
	return -byteSum(payload)
}
