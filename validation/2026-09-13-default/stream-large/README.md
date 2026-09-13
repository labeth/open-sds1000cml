# Doubled shared transfer buffer — experimental device checkpoint

Same 500 MS/s/channel precision source; two 4096-word banks (16 KiB each).
ADC encode clock remains 100 MHz. Counter tests substitute the SRAM-format
word source; they do not verify ADC signal quality or effective resolution.

Build: --interleave --precision --hostfix --stream --experimental
--stream-buffer-words=8192 --seed=14.
RBF SHA256: a8edf2305271b238a0b4be66529d8a8a0dfc698e9d10d3ff76ab91babde70797.
ARM helper SHA256: 6ccc1629cee9b5c2f5117f6e08d487cdef0028786f4b4dd7a8bf0c7e6ef9fb53.

Seed13 failed internal timing (-0.040 ns), never loaded. Seed14 passed
(+0.005 ns), with 266752/423936 RAM bits and all CDC bounds passing. Both
4096/8192-word integration simulations passed ownership, stop tail, rearm,
legacy capture, SRAM history and trigger decode checks.

Cold mains cycle preceded seed14 load; stopped app supervisor and confirmed
no competing GPMC owner. Loaded only through inherited CS3 selector07, reverse
bit order, CONF_DONE=00c0 and identity=5a52. Helper and RBF staged in /dev RAM.

Nine full 2 MiB recalls passed exact counter hashes. Two normal-scheduled /256
streams passed ~16.79 MB each. Longer runs overran after 19,759,104 and
29,204,480 checked words. /512 passed one 16.78 MB run. Real-time polling with
native 50 us sleeps did not qualify /256. Temporary CS1 cycle10/access8/gap5
was restored by diagnostics. No continuous rate is newly qualified here.

The host reads total capacity at selector26. Frozen recall stays within the
old 4096-word transfer limit but checks pointer wrap against actual RAM size.
A unit test caught and verified the correction to that distinction.
