# open-sds interface codegen — the shared FPGA<->app contract tool.
#
# One schema (codegen/ifacedef, written from specs/03) generates BOTH the app's
# Go bindings AND the FPGA's Verilog register header + decode include, plus the
# register-map reference. Editing the interface is a schema edit; `make generate`
# moves every side in lockstep and `make drift` (the CI gate) fails if any
# checked-in generated file is stale.
#
# Outputs (paths relative to this module):
#   ../app/internal/iface/iface.go        the app's Go bindings
#   ../fpga/standard/regs.vh              Verilog `define register header
#   ../fpga/standard/regmux.vh           Verilog write-strobe + read-mux decode
#   ../fpga/standard/docs/REGISTER-MAP.md the register-map reference

GO ?= go

GEN_FLAGS := -go ../app/internal/iface -vh ../fpga/standard -doc ../fpga/standard/docs

.PHONY: generate drift test vet

# Regenerate all four checked-in artifacts from the schema.
generate:
	$(GO) run ./cmd/ifacegen $(GEN_FLAGS)

# Fail (exit 1) if any checked-in generated file is stale — the drift gate.
drift:
	$(GO) run ./cmd/ifacegen -check $(GEN_FLAGS)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
