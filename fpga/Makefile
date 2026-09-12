# open-sds/fpga -- per-design Quartus projects for the SDS1102CML+ acquisition FPGA (Cyclone IV E EP4CE10F17C8).
#
#   make qsf        regenerate default/default.qsf from the acq2 analysis branch (the acq2 analysis branch)
#   make bitstream  headless Quartus flow -> default/out/default.rbf (368011 bytes); NOT for CI:
#                   takes flock /tmp/open-sds-quartus.lock, needs >= 3 GB free RAM and Quartus 21.1 at
#                   $QUARTUS_ROOTDIR (or ~/intelFPGA_lite/21.1/quartus). Exits 1 on any unplaced/unused
#                   QSF pin or negative setup slack even though the rbf was written.
#   make test       go vet + go test (the driver is tested against fake quartus_* scripts) + QSF drift check
#                   + lane-map include drift check (default/lanemap_seed.vh vs the lanecal report) + its unittest
#   make sim        every self-checking iverilog testbench (common/sim, default/sim) -- owned by WP1b
#
# Layout: common/ (shared RTL), default/ (THE default image), internal/quartus (driver), cmd/buildfpga.
# Build products live only under <design>/out/ (git-ignored).

GO ?= go
PYTHON ?= python3
DESIGN ?= default
QSFGEN := ../the acq2 analysis branch
LANEMAPGEN := ../the acq2 analysis branch

.PHONY: qsf bitstream test vet sim

qsf:
	$(PYTHON) $(QSFGEN)

bitstream: qsf
	$(GO) run ./cmd/buildfpga -design $(DESIGN)

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
