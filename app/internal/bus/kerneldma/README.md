# Experimental kernel EDMA driver

Experimental; the app does not load or use it (it drains the FPGA with the user-space EDMA path and
[`../dcinv`](../dcinv/README.md)). `../kernel_dma_linux.go` (`EnableKernelDMA`, `StartKernelStream`)
is its Go side.

* `acq_dma_test.c` (`acq_dma_test.ko`, prebuilt): a one-shot RAM-only check of the running kernel's
  EDMA API. It allocates two coherent buffers, requests EDMA channel 40, transfers a 16 KiB pattern,
  waits for the completion interrupt, compares every byte and frees everything; a successful `insmod`
  means it passed. It touches no GPMC descriptor and no FPGA.
* `acq_dma.c` (built only with `EXPERIMENTAL_GPMC_DMA=1`): the `/dev/acq_dma` drain. It binds a
  reference to the app's inherited GPMC fd (it never opens or closes GPMC) and offers synchronous
  SRAM recall and a background coherent-RAM stream queue. Long streams at decimation 256 and 512
  overran the FPGA's transfer banks: it is not a qualified continuous capture path.

## Build

Against Linux 3.2 headers (linux-3.2 from kernel.org; `edma-3.2.h` is the GPLv2 TI DaVinci EDMA
header of that release), with an ARM GCC 4.9 such as the Android NDK/AOSP prebuilt
`arm-linux-androideabi-4.9`:

```sh
make -C <linux-3.2> M=$PWD ARCH=arm CROSS_COMPILE=<prefix> modules   # EXPERIMENTAL_GPMC_DMA=1 adds acq_dma.ko
```

The kernel tree must match the unit's: after `oldconfig`/`modules_prepare`, set `UTS_RELEASE` in
`include/generated/utsrelease.h` to `"3.2.0+"` and `MODULE_ARCH_VERMAGIC_ARMVSN` in
`arch/arm/include/asm/module.h` to `"ARMv7 "`, build the host tools with
`HOSTCFLAGS="-fcommon -Wno-error"`, and disable `CONFIG_SECURITY` so `file.private_data` sits at
offset 104 (`acq_dma.c` checks the file and misc-device offsets it uses with `BUILD_BUG_ON`). The
symbol CRCs come from the running kernel's CRC table (the saved compressed kernel image does not
match the running one).

`-fno-pic -fno-pie` (in the Makefile) is essential: without it the compiler emits `R_ARM_REL32`,
which Linux 3.2's ARM module loader rejects.

Loading a module is volatile (RAM only); a power cycle clears it.
