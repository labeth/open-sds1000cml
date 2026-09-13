# Streaming buffer ownership — simulation checkpoint only

`fpga/acq_sram/stream_banks.v` implements two-bank ownership and descriptors for
reusing the 4096-word host read RAM. It is not connected to top.v or the app yet,
and is not part of a generated/device-loaded image. Existing qualified revision
10 acquisition remains running. No device writes were made for this checkpoint.

The prior unconnected `stream.v` draft allocates an additional 4096-word FIFO
and limits its proposed rate to /8192. This controller instead targets reuse of
the existing host buffer and exposes explicit block ownership, word positions,
partial counts and loss detection. Neither draft constitutes working streaming.

## Contract implemented

- Producer clock accepts 64-bit packets of two consecutive SRAM-format words.
  Default banks each hold 1024 packets = 2048 words = 8192 bytes.
- An alternating publication token transfers each bank to the host. A matching
  release returns ownership. Duplicate/currently unowned and wrong-token
  releases do not reclaim a live descriptor. Tokens are not unlimited-lifetime
  request IDs: software must finish a descriptor before reusing its bank.
- Descriptor first_word is a 64-bit stream word ordinal, independent of SRAM's
  wrapping physical counter. This allows the receiver to detect missing blocks.
- A partial final bank can be sealed. An odd final word uses packet_single,
  which seals the bank and marks the high word invalid. No padding is counted as
  a captured sample: words = 2*packets - last_single.
- Input arriving without a free bank latches overrun and stops buffer writes.
  Published unread data remains intact. The host gets a synchronized fault flag.
  Recovery requires an explicit reset/new epoch; samples are never silently
  overwritten or represented as a contiguous acquisition after a fault.
- Reset asserts asynchronously and releases synchronously per clock domain.
  Metadata is held while owned by the host and crosses ahead of the publication
  token. Physical integration still needs CDC constraints and timing review.

## Verification

`simulation.json` contains Icarus commands/results at bank sizes 8 and 1024
packets, with unrelated 100 MHz producer and ~71.4 MHz host clocks. Tests check
1003/10243 ordered packets, changing host delays, both banks pending, partial
and empty sealing, odd final-word sealing, wrong-token release, overrun without
unread-data overwrite, sticky failure and reset with an unread old bank.

This verifies digital protocol behavior, not physical clock-domain timing,
250/125 MHz timing closure, ADC alignment, GPMC first-word timing or ARM
scheduling capacity.

## Integration still required

1. Tap the qualified precision sample stream, pair words at the memory clock,
   and replace the read-RAM write mux with mutually exclusive stream/recall
   ownership. Preserve SRAM recording and its trigger history.
2. Add revisioned enable/stop/release/status/descriptor registers, with no
   simultaneous frozen-recall writes to host-owned streaming banks. Keep the
   qualified capture ABI working when streaming is disabled.
3. Define continuous-run versus trigger-freeze behavior and an epoch/stop
   handshake. Drain/seal partial data before switching RAM back to recall.
4. Implement inherited-fd ARM block draining, descriptor/count/sequence checks,
   a bounded ARM history ring and explicit overflow reporting. Preserve Q8.8;
   process display/measurement windows without requiring every incoming sample
   to pass the full ARM FIR before the next bank is released.
5. Simulate integrated capture/trigger boundaries, close timing, cold-load a
   candidate and verify long counter streams, stalls, odd stops and restarts.
   Only then enable streaming in timebase planning.

At /256, two 16-bit channels require 7.8125 MB/s; one bank fills in 1.048576 ms.
At /512 they require 3.90625 MB/s and a bank fills in 2.097152 ms. These are exact
format/rate calculations, not demonstrated device streaming capabilities.
The measured 10.9 MB/s DMA burst rate alone does not establish sustainability.
