# open-sds/fpga — the owned acquisition bitstream module.
#
# The generated interface (regs.vh / regmux.vh / REGISTER-MAP.md) is produced by
# the sibling open-sds/codegen module (`make -C ../codegen generate`), not here.
# This module owns the RTL (standard/) and the headless build tooling
# (internal/quartus, cmd/buildacq).

GO ?= go

.PHONY: build test vet bitstream

build:
	$(GO) build ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# Compile standard/*.v + acq.qsf + the generated regs.vh/regmux.vh into
# standard/acq.rbf (exactly 368011 bytes). NOT run in CI: it launches headless
# Quartus 21.1 Lite and is memory-gated (the 3.8 GiB bench box runs one flow at a
# time). Run this on the bench, with Quartus installed and the pins supplied.
bitstream:
	$(GO) run ./cmd/buildacq
