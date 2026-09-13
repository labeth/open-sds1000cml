# Stream byte-copy and latency diagnostics

Same experimental revision 11 seed13 image as stream-device. RAM helper SHA256
42e4223989c0723fc7a146e8724be7104e7f2f3cbe6e563319a2ce917d6d3fec.
Temporary CS1 cycle=10, access=8, gap=5 restored on every diagnostic exit.

Byte-copy DMA avoids two per-halfword conversions. Stream buffers are prepared
before arm, and diagnostic counter validation follows safe copy/release. Byte
order, page splits, coherent persistent-buffer reuse, partial tails, and retaining
faulted banks have host model tests. Hardware logs include all attempted repeated
runs, including failures. A missing counter break is not a pass if an overrun
terminated the run. /512 and /256 both remain unqualified for reliable service.
