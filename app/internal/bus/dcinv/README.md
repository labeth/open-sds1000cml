# dcinv — D-cache invalidate helper module for the EDMA drain (vendored)

The AM3352 EDMA writes DRAM as a bus master, not through the Cortex-A8 data cache. A drain buffer
whose lines are cached (a freshly mlocked, zero-filled buffer is cached too — measured on 2026-09-05:
records showed 32-word = 64-byte zero runs, up to 8448 of 20478 samples per frame) reads back stale
lines. Linux 3.2 has no user-space D-cache invalidate, so this ~70-line module exposes
`/dev/dcinv` ioctl `DCINV_INV = 0x40084401` `{addr, len}` = DCIMVAC over the buffer.

Origin: reference tree `open-sds1000cml-fpga/tools/dcinv` (built remotely against the unit's
3.2.0+ kernel with harvested symbol CRCs; see the reference BUILD.md for the exact recipe —
Bootlin armv7 gcc 7.3, linux-3.2 omap2plus_defconfig, MODVERSIONS, vermagic
`3.2.0+ mod_unload modversions ARMv7 p2v8`). The prebuilt `dcinv.ko` here is that build, unchanged
(sha256 in dcinv_test.go). The module only invalidates cache lines; it cannot write memory.
Loading it is volatile (RAM only) and a power cycle clears it — allowed by the device rules.

The app embeds `dcinv.ko`, writes it next to its own binary on the U-disk and `insmod`s it at boot
when `/dev/dcinv` is absent (`app/internal/bus/dcinv.go`), before the EDMA drain is enabled.
