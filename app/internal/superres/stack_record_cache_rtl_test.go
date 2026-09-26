// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real seek/warmup controller and DDR transport against the
// two-stage sequential-counter SRAM model, rather than a random-read stub.
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-043, REQ-SDS-044
func TestStackRecordCacheRTL(t *testing.T) {
	for _, cfg := range []struct {
		name                   string
		aw, cache, full, phase int
	}{{"phase-0", 8, 3, 0, 0}, {"phase-1", 8, 3, 0, 1}, {"phase-2", 8, 3, 0, 2}, {"phase-3", 8, 3, 0, 3}, {"phase-5", 8, 3, 0, 5}, {"phase-7", 8, 3, 0, 7}, {"transfer-overflow", 10, 8, 0, 1}, {"two-word-pages", 8, 1, 1, 3}, {"largest-pages", 12, 11, 1, 2}, {"physical-depth", 19, 8, 1, 1}} {
		t.Run(cfg.name, func(t *testing.T) {
			dir := t.TempDir()
			image := filepath.Join(dir, "cache.vvp")
			root := filepath.Join("..", "..", "..", "fpga", "acq_sram")
			args := []string{"-g2012", "-s", "tb_stack_record_cache", fmt.Sprintf("-Ptb_stack_record_cache.AW=%d", cfg.aw), fmt.Sprintf("-Ptb_stack_record_cache.CACHE_AW=%d", cfg.cache), fmt.Sprintf("-Ptb_stack_record_cache.FULL_ONLY=%d", cfg.full), fmt.Sprintf("-Ptb_stack_record_cache.PHASE=%d", cfg.phase), "-o", image}
			for _, file := range []string{"stack_record_cache.v", "stack_cache_memory.v", "stack_page_transfer.v", "host_packer.v", "host_fifo.v", "host_sink.v", "finite_recall.v", "ordinal_counter.v", "transport.v", "sim/ddr_model.v", "sim/tb_stack_record_cache.v"} {
				args = append(args, filepath.Join(root, file))
			}
			if out, err := exec.Command("iverilog", args...).CombinedOutput(); err != nil {
				t.Fatalf("compile %v\n%s", err, out)
			}
			out, err := exec.Command("vvp", image).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "PASS frozen SRAM cache") {
				t.Fatalf("simulate %v\n%s", err, out)
			}
			t.Log(strings.TrimSpace(string(out)))
		})
	}
}
