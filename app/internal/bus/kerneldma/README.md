# Experimental kernel EDMA foundation

This is not installed or used by the normal application. Only `acq_dma_test.ko`
has been loaded on the scope. `acq_dma.c` is a draft fixed-port drain; do not load
it yet. Its file/device ABI assumptions still need inspection, and it does not
yet implement the background stream/ring needed for reliable continuous capture.

## Verified foundation

The one-shot test allocates two coherent RAM buffers, requests EDMA channel 40
through the kernel API, transfers a nontrivial 16 KiB pattern, waits for the
completion interrupt, compares every byte, then frees both channel and buffers.
A successful insmod proves the test passed; any allocation, transfer, timeout,
status or comparison error fails module initialization. It has no GPMC accesses
and never opens or closes a GPMC descriptor. Three subsequent unload/reload
cycles also passed, followed by a successful final unload.

Test module SHA256:
`896b2c298937b9843ade81005b59c10ba3fb0335a3d6f3ccdccf3fbdf303b062`.
All device staging was under `/dev` (RAM). The currently loaded FPGA remains the
experimental 8192-word-buffer revision 11 image. No persistent internal writes.

## Build compatibility

Source headers: Linux 3.2 from
https://cdn.kernel.org/pub/linux/kernel/v3.x/linux-3.2.tar.xz.
The copied `edma-3.2.h` is the GPLv2 TI DaVinci EDMA header from that release.
Compiler: existing Android ARM GCC 4.9 at
`/home/labeth/ws/lineageOS/prebuilts/gcc/linux-x86/arm/arm-linux-androideabi-4.9/bin/arm-linux-androideabi-gcc`.
Kernel work directory: `/tmp/acq-kernel-build/linux-3.2`.

Use the archived `kernel.config` and `Module.symvers` in
`validation/2026-09-14-kernel-dma/`. After oldconfig/modules_prepare, set
`include/generated/utsrelease.h` to `#define UTS_RELEASE "3.2.0+"` and set
`MODULE_ARCH_VERMAGIC_ARMVSN` in `arch/arm/include/asm/module.h` to `"ARMv7 "`.
Build host tools with `HOSTCFLAGS="-fcommon -Wno-error"`.
Build modules using the normal kernel `make -C KDIR M=THIS_DIRECTORY ARCH=arm
CROSS_COMPILE=... modules` command. Default builds only the RAM self-test.
`EXPERIMENTAL_GPMC_DMA=1` additionally builds the unqualified draft.

`-fno-pic -fno-pie` is essential: the Android compiler otherwise emits
R_ARM_REL32, which Linux 3.2's ARM loader does not handle. The initial test
module was rejected as invalid format before initialization. The corrected
module uses only supported relocations. Its vermagic, 328-byte module structure,
init offset 0xbc and exit offset 0x140 match the saved vendor module. The older
dcinv module's exit offset differs; it remains marked permanent on the device.

## CRC evidence and limits

The saved compressed kernel image did not match running symbol addresses and
was rejected as a CRC source. The running `/proc/kallsyms` locates its CRC table
at virtual c037a40c..c037e8dc. A read-only RAM helper mapped physical
8037a000..8037f000. It extracted 4404 CRCs; all 174 comparable saved-module CRCs
matched. Fourteen other saved CRCs refer to other loadable modules.

Fresh genksyms probes match the kernel's completion, mutex and five used EDMA
API signatures. The device/file-related probes differ, so a matching imported
CRC alone must not be taken as proof of those structure layouts. The loaded
self-test uses a null device pointer and no file/device structures. Its used
synchronization and EDMA structures have matching probe CRCs. The full drain
has not been qualified or loaded.
