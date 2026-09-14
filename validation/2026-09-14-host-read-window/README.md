# Owned-bank host prefetch window

`acq_host_read_window` selects a published bank by bank/token and halfword
offset. It snapshots the published length, prefetches one halfword, holds data
stable until GPMC's completed-read pulse, then advances once. It never releases
a bank automatically. Explicit release carries the selected bank and token.
It may abandon a partly read bank only when no RAM response is outstanding.

Ready/token mismatch or upstream host_fault immediately masks data-ready and subsequently invalidates
the selection. Outstanding responses drain without becoming new data. Invalid
selections, reads without ready data, RAM errors, and release/reselection while
a RAM response is pending set a sticky fault. Clearing the fault does not
change the cursor or silently select another bank. Reset also resets the RAM
response pipeline; the caller must supply a common epoch reset.

Caller contract: begin the first bus data read only after data-ready. Subsequent
bus cycles must provide time for GPMC pop synchronization and RAM prefetch.
This interface has no WAIT pin. It returns zero when not ready and faults on an
invalid pop. Board timing qualification must establish the usable GPMC timing.

Test uses actual `gpmc_slave` and `sram_host_ram`, with modeled bank publication
and tokens. It verifies 5120 halfwords from bank 0, an odd three-word record in
bank 1, addresses 10237..10239, overread rejection, stale tokens, reuse during a
pending read, upstream fault invalidation of prefetched data, and explicit
release identity. Data is checked throughout each
read assertion, and bus tri-state release is checked after reads. Test cycles
are 160 ns (12.5 MB/s equivalent bus cadence), not a hardware benchmark.

Run `python3 validation/2026-09-14-host-read-window/test_window.py`.
Optional `--vendor-library /path/to/altera_mf.v` uses the Intel M9K model.
Source hashes accompany results. This window has not yet been connected to the
profile board register map, acquisition status or kernel driver, and it does
not qualify an FPGA image or end-to-end streaming throughput.
