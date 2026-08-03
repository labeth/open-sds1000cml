// noop — holds the app slot without touching the FPGA, so a takeover preserves
// whatever fabric + analog config is already loaded (for the SRAM co-opt test).
package main

import "time"

func main() {
	for {
		time.Sleep(time.Hour)
	}
}
