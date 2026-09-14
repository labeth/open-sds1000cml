# Precision configuration capture and short epoch reset

The SHARED_TAIL path now captures the held core-domain decimation setting into
five local clk100 registers. They refresh while enable_s[2] is low, then remain
fixed for the entire filtering epoch. Arithmetic and factor selection operate
from that local setting. The parallel default retains its existing behavior.

Both slower-domain enable chains now assert reset asynchronously when precision
enable falls and release through three destination-clock stages. A disable
shorter than a 100 MHz or 125 MHz clock period therefore cannot be missed. The
old synchronous-only chains could retain filter history after a one-core-cycle
disable. `before/failure.txt` reproduces that: the second capture's first output
was 7d307d1e instead of the clean reference's 7a9d7d47. It includes source hashes
and the relevant old RTL/bench snapshots.

`epoch-tests.txt` verifies a one-core-cycle disable between 100 MHz edges,
immediate destination-reset assertion, new configuration capture, and output
identity with an independently long-reset parallel reference. /256, /512, /16
and /4096 pass. `shared-tests.txt` repeats complete precision arithmetic-path
comparisons at /16 through /8192. FIFO interfaces are ideal simulation models;
these tests cannot prove metastability behavior or physical reset pulse widths.

The configuration bundle is held before enable rises. Its last local capture
occurs on the third clk100 release edge, at least two clk100 periods after the
earliest enable-sampling edge. Filters see the released enable on the next
edge. `acquisition_config_cdc.sdc` bounds the five source-to-destination routes
to 6 ns (minimum 0), checks exact bit coverage 0..4, and leaves local arithmetic
normally timed. The audit checks both ends of every route at all three corners.
No broad clock-group cut, data-path exception or ADC reset exception is added.
Remaining ADC CDC/reset paths and board IO still need their own qualification.

The final build in `integrated-build` fits: 9495/10320 LEs, 7653 registers,
44/46 M9Ks, and all 645 LABs used. All 126 backend/configuration CDC rows pass.
Setup remains -6.894 ns, recovery -5.229 ns; hold +0.144 ns, removal +0.496 ns.
The worst setup now runs from source decim_l through edge detection to
trigger_second; source data also fails through that path. It needs a real
trigger pipeline, not a configuration exception. No board image was generated.

To retain the reset fix within the device, tail output registers are preserved
and normalization uses fixed slices for legal shifts 3/6/9/12. `tail-tests.txt`
checks eleven equivalent epochs through remaining log 8; `queue-tests.txt`
checks shared-memory queue corners. The complete composition test covers
/16 through /8192. The older full log-12 test is in precision-tail and predates
these edits; it is not evidence for this exact source revision. Failed area
attempts are retained in retimed-build and preserved-build.
