# General SRAM/triggering image for Cyclone IV E EP4CE10F17C8.
# make bitstream publishes acq_sram/out/general/current/bench.rbf only after timing passes.
# qsf/test/sim below retain legacy source-verification commands; they do not select release images.

GO ?= go
NODE ?= node
PYTHON ?= python3
DESIGN ?= default
QSFGEN := ../the acq2 analysis branch
LANEMAPGEN := ../the acq2 analysis branch

.PHONY: qsf bitstream test vet sim

qsf:
	$(PYTHON) $(QSFGEN)

bitstream:
	$(NODE) --experimental-strip-types acq_sram/build_general.ts

vet:
	$(GO) vet ./...

test: vet
	$(GO) test ./...
	$(PYTHON) $(QSFGEN) --check --quiet
	$(PYTHON) $(LANEMAPGEN) --check --quiet
	$(PYTHON) $(LANEMAPGEN:.py=_test.py)

sim:
	@for d in common/sim $(DESIGN)/sim; do \
	  if [ -x $$d/run.sh ]; then echo "== $$d"; (cd $$d && ./run.sh) || exit 1; else echo "== $$d: no run.sh (not built yet)"; fi; \
	done
