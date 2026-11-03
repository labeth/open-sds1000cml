# dcinv — D-cache invalidate module for the EDMA drain

The AM3352 EDMA writes DRAM as a bus master, not through the Cortex-A8 data cache. A drain buffer
whose lines are cached (a freshly mlocked, zero-filled buffer is cached too: records showed
32-word = 64-byte zero runs, up to 8448 of 20478 samples a frame) reads back stale lines. Linux 3.2
has no user-space D-cache invalidate, so this 70-line module exposes `/dev/dcinv`, ioctl
`DCINV_INV = 0x40084401` `{addr, len}` = DCIMVAC over the buffer. It only invalidates cache lines; it
cannot write memory.

The app embeds the prebuilt `dcinv.ko` (its sha256 is checked in `../dcinv_test.go`), writes it next
to its own binary on the stick and `insmod`s it at start when `/dev/dcinv` is absent
(`../dcinv.go`, `EnsureDcinv`), before the EDMA drain is enabled. Without it the drain uses a fresh
buffer per transfer. Loading it is volatile (RAM only); a power cycle clears it.

`dcinv.ko` was built against the unit's 3.2.0+ kernel: Bootlin armv7 gcc 7.3, linux-3.2
`omap2plus_defconfig` with `MODVERSIONS`, vermagic `3.2.0+ mod_unload modversions ARMv7 p2v8`, and
the symbol CRCs (`Module.symvers`) harvested from the running kernel. The Makefile builds it with
`make KDIR=<linux-3.2> CROSS_COMPILE=<prefix>`.
