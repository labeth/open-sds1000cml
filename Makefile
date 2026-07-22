# Repo-level conveniences over the Go modules (app/, ota/, codegen/, fpga/).
#
#   make test      — run every module's full test suite (browser/node tests
#                    self-skip unless node + Playwright are installed; set
#                    CI_REQUIRE_BROWSER=1 to turn those skips into failures).
#   make generate  — regenerate the FPGA<->app interface (delegates to codegen).
#   make drift     — fail if any checked-in generated file is stale (CI gate).

.PHONY: test generate drift

test:
	cd app && go test ./...
	cd ota && go test ./...
	cd codegen && go test ./...
	cd fpga && go test ./...

generate:
	$(MAKE) -C codegen generate

drift:
	$(MAKE) -C codegen drift
