// Package boot holds the USB boot anchor (startup.sh) and its test harness.
// startup.sh is the never-OTA'd anchor that launches the agent; it is deployed
// to the USB stick verbatim. The Go here is tests only (see boot_test.go).
package boot
