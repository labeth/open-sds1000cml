# Narrow host RAM read registers — RAM test passes, build pending

Select the requested 16-bit quarter of each section's 64-bit RAM word before
its read register. Section outputs and their selection mux are 16 bits instead
of 64. Writes, capacity, bank layout and two-edge read response stay unchanged.
The behavioral RAM test passes 30757 checked responses: full coverage, separate
bank concurrency, invalid addresses, reset cancellation and retained contents.

Balanced seed-2 build session 72643: /tmp/acq-host-narrow-build.txt.
It is running; re-poll before restarting. Quartus must establish whether this
maps efficiently to M9Ks. No resource/timing improvement or deployment claimed.

## Explicit mixed-width RAM follow-up

The inferred variable-quarter experiment was deliberately terminated after more
than ten minutes of active synthesis. No host RAM inference appeared in its log;
the ingress and precision memories did infer. This is not a completed fit result.
Its exact source is preserved as `inferred-host-ram.v`.

The replacement uses five explicit altsyncram blocks, each 512 x 64 write /
2048 x 16 read, with the read address registered on the host clock and no extra
output register. Capacity remains 10240 halfwords, with the existing two-edge
request/response interface. Same-location read/write remains unspecified.
The portable simulation model is selected only outside ALTERA_RESERVED_QIS.

Both portable and Intel altera_mf.v primitive simulations passed all 30757
responses: full range, disjoint bank concurrency, invalid addresses and resets.
The test runner now accepts --vendor-library to repeat the primitive check.
Intel's model warns that mixed-port collision behavior is modeled as OLD_DATA;
the design forbids such collisions, so that behavior is not relied on.

The acquisition build and composed simulation were launched; their final results
are still pending. No device deployment or throughput claim follows from these
RAM simulations.

The composed acquisition simulation completed successfully: raw 3017 words,
precision 48 words, streaming 6001 words, recall ownership, stale-record
rejection and frontend faults. Its recorded hash precedes the macro correction.
Quartus did not define SYNTHESIS in this flow, so that first explicit-block build
was deliberately stopped after elaboration showed no host primitive. The guard
now uses Quartus ALTERA_RESERVED_QIS; the vendor test defines the same guard and
again passes 30757 responses. The corrected build is pending.

## Corrected fit result

Quartus elaborates the explicit host altsyncram. The core uses 9222 LEs,
7724 registers, 44 M9Ks and 643/645 LABs. This saves 118 LEs relative to the
9340-LE mailbox-fixed checkpoint, without reducing host capacity. Setup
-4.720 ns, hold +0.107 ns, recovery -6.220 ns, removal +0.493 ns and minimum
pulse +1.513 ns. CDC audit fails; build exit 3. This is not a qualified image.
Exact sources and reports are in explicit-build.

The acquisition test runner now supports --vendor-library. In that mode it
renames the temporary transport DDIO module and its instantiation to avoid a
name collision with Intel's library. The ideal DDR transport and ADC mock are
unchanged; only host RAM is substituted with the vendor model. Its full run
is pending; standalone vendor RAM and portable acquisition checks already pass.

The Intel-host-RAM composed run completed successfully (exit 0), including
6001-word streaming and all final ownership/recall/fault assertions. See
explicit-vendor-integration.txt. The retained candidate saves logic but does
not save LABs or close timing. Worst setup is launch_stream through backend
selection into transport legal_command (8.629 ns data delay).
