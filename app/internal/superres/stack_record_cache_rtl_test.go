// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES
package superres

import (
	"fmt"
	"open-sds/app/internal/testenv"
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
		aw, cache, full, delay int
	}{{"direct", 8, 3, 0, 0}, {"registered-commands", 8, 3, 0, 1}, {"sixteen-word-pages", 8, 4, 0, 1}, {"large-pages", 10, 8, 0, 0},
		{"two-word-pages", 8, 1, 1, 1}, {"largest-pages", 12, 11, 1, 0}, {"physical-depth", 19, 8, 1, 1}} {
		t.Run(cfg.name, func(t *testing.T) {
			dir := t.TempDir()
			image := filepath.Join(dir, "cache.vvp")
			root := testenv.RTLDir(t)
			args := []string{"-g2012", "-s", "tb_stack_record_cache", fmt.Sprintf("-Ptb_stack_record_cache.AW=%d", cfg.aw), fmt.Sprintf("-Ptb_stack_record_cache.CACHE_AW=%d", cfg.cache), fmt.Sprintf("-Ptb_stack_record_cache.FULL_ONLY=%d", cfg.full), fmt.Sprintf("-Ptb_stack_record_cache.COMMAND_DELAY=%d", cfg.delay), "-o", image}
			for _, file := range []string{"stack_record_cache.v", "transport.v", "sim/ddr_model.v", "sim/tb_stack_record_cache.v"} {
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
