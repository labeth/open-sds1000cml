# open-sds acq2 interface codegen — the FPGA<->app register contract tool.
#
# One schema (codegen/ifacedef, transcribed from docs/acq2/05-WORKPLAN.md §2)
# generates the fabric's Verilog header + decode include, the app's Go bindings
# and the register-map reference. `make generate` moves every side in lockstep;
# `make drift` (the CI gate) regenerates into a temp dir and fails if any
# checked-in artifact differs.
#
# Outputs (relative to the repo root):
#   fpga/default/regs.vh
#   fpga/default/regmux.vh
#   app/internal/iface/iface.go
#   fpga/default/docs/REGISTER-MAP.md

GO   ?= go
ROOT ?= ..

.PHONY: generate drift test vet

generate:
	$(GO) run ./cmd/ifacegen -root $(ROOT)

drift:
	$(GO) run ./cmd/ifacegen -root $(ROOT) -check

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
