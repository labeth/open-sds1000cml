//go:build race

// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

// raceBuild: the race detector's sync.Pool drops a share of what it is
// given on purpose, so the buffer-reuse figures do not hold under it.
const raceBuild = true
