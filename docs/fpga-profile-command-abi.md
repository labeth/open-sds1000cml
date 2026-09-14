# Profile command window (draft ABI)

Implemented by `acq_command_port` and `acq_host_read_port`, now combined with
GPMC and acquisition RTL in `acq_profile_core`. This is a new profile interface,
not legacy rev11 compatibility. The combined simulation covers commands,
results, status, capture and recall; the real board PLL/pin wrapper, physical
constraints, driver support and device qualification remain unfinished.
Do not use this as a device API yet.

Selectors are the existing GPMC slave's logical 8-bit selectors. All transfers
are 16 bits. Configuration registers are host-domain shadows, readable and
writable at any time. A command snapshots all twelve words coherently into the
acquisition clock domain. Later edits apply only to later commands.

| Selector | Meaning |
| --- | --- |
| 0x10 write | Command opcode below; not a bit mask |
| 0x11 read | Bit 0: command/result pending. Bit 1: sticky busy-command rejection. Bit 2: result valid |
| 0x11 write | Write bit 1 to clear sticky rejection; a new rejection wins |
| 0x12 read | Result: 0 accepted, 1 decoder/configuration error, 2 acquisition request rejected |
| 0x13 read | Last accepted opcode (busy-rejected writes do not replace it) |
| 0x14 read | Accepted-command sequence, modulo 65536; reset to zero on epoch reset |
| 0x20 / 0x21 | Pre-trigger word count, low 16 / high 4 bits |
| 0x22 / 0x23 | Post-trigger word count, low 16 / high 4 bits |
| 0x24 / 0x25 | Recall offset, low 16 / high 4 bits |
| 0x26 / 0x27 | Recall length, low 16 / high 4 bits |
| 0x28 | Read-bias low 16 bits |
| 0x29 | Bits 2:0 bias high; 8:4 decimation log; 10:9 trigger mode; 11 channel; 12 falling |
| 0x2a | Trigger level, 16 bits |
| 0x2b | ADC encode-enable mask, low 10 bits |

Reserved bits must be zero. Starts with nonzero reserved bits produce
`core_error`, not truncated operands. The acquisition core separately validates
counts, decimation, trigger mode, frozen geometry and current readiness.
Counts and offsets are SRAM words: raw words contain two samples per channel;
precision words contain one Q8.8 sample per channel. A zero recall length is
an empty request, not an abbreviation for the full record.

| Opcode | Action |
| --- | --- |
| 1 | Start finite capture (acquisition operation 0) |
| 2 | Start frozen recall (operation 2) |
| 3 | Start streaming (operation 1); rejected when ENABLE_STREAM=0 |
| 4 | Halt pulse |
| 5 | Force-trigger pulse |
| 6 | Raw ADC snapshot service; rejected in the base profile, which has no snapshot adapter |
| 7 | Query acquisition status without starting or stopping acquisition |

Zero, unknown opcodes and nonzero upper opcode bits reject. Halt, force and
snapshot do not consume shadow geometry, so malformed geometry does not block
these controls. Mailbox busy still applies to every opcode. Software must wait
for an available slot before submitting one; a busy submission is discarded
and sets sticky rejection.

The command slot remains busy until a coherent result has returned to the host.
The decoder captures the acquisition core's registered `request_error` on the
clock after `core_start` is consumed. Wire that signal directly to
`acquisition_request_error`; a differently delayed response violates this
interface. Decoder errors take priority. Halt/force/snapshot do not consume the
start-request error signal.

Software waits for busy=0 and result-valid=1, then reads the result and accepted
sequence/opcode. A new accepted command clears result-valid; the previous
result code is stale until valid reasserts. Busy-rejected writes leave the
accepted command and its eventual result intact. No acquisition completion or
successful trigger/capture is implied by result 0: it acknowledges the request,
and later acquisition faults must be reported through the board status path.

Both domains share an epoch reset, asserted asynchronously into two-stage
local reset chains and released synchronously in each destination domain.
Mailbox validity is cleared even while a clock is stopped; command-port state
clears when its clock resumes, before the local reset releases. Reset aborts
command/result delivery, clears result-valid and sequence, and restores zero
configuration except post-count=1 and encode mask=0x3ff. Simulation covers a
1 ns pulse and stopped clocks; this does not qualify a hardware minimum reset
pulse width. The board reset stretcher remains required.

The 208-bit forward mailbox payload is `{opcode, cfg11, ..., cfg0}`. A separate
130-bit reverse mailbox carries the result and a 128-bit status snapshot. `core_start` and control pulses align
with the captured settings. The board must keep this clock-domain contract and
add physical timing/CDC constraints for both directions before deployment.

## Host readout window

Implemented by `acq_host_read_port` in the host clock domain. The board must
connect ownership descriptors and the host RAM read port, and combine its read
mux with the command window. Only selector 0x31 pops data on a completed GPMC
read; reading status or descriptors never advances the cursor.

| Selector | Meaning |
| --- | --- |
| 0x30 write | Select: bits 12:0 halfword offset, bit 13 bank, bit 14 token; bit 15 must be zero |
| 0x30 read | Current halfword cursor within the selected bank |
| 0x31 read | Prefetched 16-bit data; pop once at end of bus read |
| 0x32 read | Bits 0 ready, 1 exhausted, 2 sticky window fault, 3 selected |
| 0x33 write | Bit 0 explicit release, bit 1 clear window fault; all other bits zero |
| 0x34 read | Bits 1:0 published-ready, 3:2 tokens, bit 4 upstream host-path fault |
| 0x35 / 0x36 read | Published 32-bit word counts for bank 0 / 1 |
| 0x38..0x3b read | Bank 0 first-word ordinal, low 16 bits first |
| 0x3c..0x3f read | Bank 1 first-word ordinal, low 16 bits first |

Each bank holds at most 2560 words / 5120 halfwords. Select only a ready bank
with its advertised token and an offset below twice its word count. Wait for
data-ready before reading data. Qualified bus timing must allow the next
halfword to prefetch between reads; the interface does not have a WAIT pin.
Reading without ready data returns zero and faults on pop. End-of-bank does not
release automatically or advance into the other bank.

Release may explicitly abandon unread data but rejects while a RAM response is
pending. Invalid/reserved selection or control writes fault without changing
the cursor or releasing ownership. Clearing a fault does not select another
record. Token loss or upstream fault immediately masks prefetched data.

Word counts and ordinals read as zero when their bank is unpublished or the
host path is faulted. Descriptor coherence depends on the ownership protocol:
read metadata while its bank is ready and do not release it during those reads.
These registers are functionally simulated; the complete board image, physical
bus timing and kernel support remain unqualified.


## Acquisition status and discovery

Each command response includes a coherent 128-bit snapshot sampled when the
core offers the response. Opcode 7 queries it without changing acquisition.
Read selectors 0x40..0x47, low 16 bits first, only while result-valid is set.
They return zero while a new result is pending. Status stays fixed until the
next response, even if the acquisition changes during a multiword host read.
The command sequence/opcode identifies which query produced it.

The base `acq_profile_core` packs four 32-bit words:

| Word | Contents |
| --- | --- |
| 0 (0x40/0x41) | Flags below; other bits zero |
| 1 (0x42/0x43) | Record word count, low 20 bits |
| 2 (0x44/0x45) | Trigger word index, low 20 bits |
| 3 (0x46/0x47) | Physical record start, low 19 bits |

Flags: 0 start-ready, 1 active, 2 done, 3 acquisition fault, 4 record frozen,
5 triggered, 6 trigger in second raw sample, 7 precision format, 8 ADC PLL
locked, 9 host-path fault, 10 transport PLL locked. Interpret geometry only for
a frozen, non-faulted record. Query after completion; the result accompanying
a start acknowledges acceptance, not a completed record. The readout path also
receives acquisition fault through a host-domain synchronizer to invalidate
buffered data and hide published descriptors after an acquisition fault.

Discovery selectors in `acq_profile_core`:

| Selector | Value |
| --- | --- |
| 0x00 | Magic 0xacd2 |
| 0x01 | ABI 1 (draft, not legacy rev11) |
| 0x02 | Profile 1 deep, 0 unified experimental |
| 0x03 | Capability bits: 0 finite, 1 precision, 2 stream, 3 edge trigger |
| 0x04 / 0x05 | Total host words 5120 / bank words 2560 |
| 0x06 / 0x07 | SRAM depth 0x00080000 words, low half first |
| 0x08 | Sample-format bitmap 3: raw and Q8.8 |
| 0x09 | ADC encode clock 100 MHz |
| 0x0a | Hardware-qualified flag 0; this image is experimental |
| 0x0b / 0x0c | Build ID, low half first |

The base profile rejects external-match capture mode and raw ADC snapshot
opcode 6 rather than accepting requests whose hardware adapters are absent.
Protocol-specific compositions must connect and advertise their own adapters.
