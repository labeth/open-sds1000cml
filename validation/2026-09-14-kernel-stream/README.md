# Experimental kernel stream worker, 2026-09-14

The full module holds a reference to the inherited GPMC descriptor. It uses
kernel-managed EDMA channel 40 and a completion interrupt, with a 32-slot,
16 KiB/slot coherent RAM queue. No normal-app integration or permanent scope
installation is claimed. Deployment was under /dev; FPGA remained revision 11
with the 8192-word host buffer. The module is not a qualified default.

`worker-stream.json` records a successful short /256 counter stream followed
by two longer failures. `worker-timing.json` records two further overruns:

| Reduction | Words delivered before error | Elapsed | Max DMA→IRQ | Max IRQ→worker | RAM queue high-water |
|---|---:|---:|---:|---:|---:|
| /256 | 15,826,944 | 8.114 s | 1,257 us | 3,304 us | 1 block |
| /512 | 14,594,048 | 14.956 s | 3,325 us | 138 us | 1 block |

Both errors occurred checking FPGA status after DMA (stage 5). These results
rule out treating average DMA bandwidth or the kernel queue alone as sufficient
for lossless streaming. The failed bank is retained, acquisition halted, and
completed RAM blocks are delivered before reporting the error.

The eight stats fields are maximum DMA-to-IRQ us, IRQ-to-worker us,
IRQ-to-successful-release us, worker-loop interval us, error stage, last FPGA
flags, maximum RAM queue occupancy, and published blocks. Successful-release
latency excludes a bank that failed its status check.

`worker-timing.ko` SHA256:
6d0d30f0fe3537cfd7e080d3c24741c7a415de4bbb78c9d9193b38a2ffb1ae20.
`pop-only.ko` is the earlier synchronous-drain module, not the worker.
The runtime layout disassembly and build config are archived here. In particular
CONFIG_SECURITY is disabled to match file.private_data offset 104.

Go bus guards now reject direct GPMC access and competing userspace EDMA while
the kernel owns the bus. CloseKernelDMA joins the worker before GPMC timing
restoration. These host guards were tested and cross-built after the archived
hardware runs; they have not yet been deployed in a new helper on the device.
