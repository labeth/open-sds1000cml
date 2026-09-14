# Profile command window (draft ABI)

Implemented by `acq_command_port`, currently tested independently of the board
top. These selectors are a new profile interface, not legacy rev11 register
compatibility. Board identity/capability discovery, result/status snapshots and
host SRAM readout still need integration. Do not use this as a device API yet.

Selectors are the existing GPMC slave's logical 8-bit selectors. All transfers
are 16 bits. Configuration registers are host-domain shadows, readable and
writable at any time. A command snapshots all twelve words coherently into the
acquisition clock domain. Later edits apply only to later commands.

| Selector | Meaning |
| --- | --- |
| 0x10 write | Command opcode below; not a bit mask |
| 0x11 read | Bit 0: delivery busy. Bit 1: sticky busy-command rejection |
| 0x11 write | Write bit 1 to clear sticky rejection; a new rejection wins |
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
| 6 | Snapshot pulse; board adapter must implement the frontend toggle protocol |

Zero, unknown opcodes and nonzero upper opcode bits reject. Halt, force and
snapshot do not consume shadow geometry, so malformed geometry does not block
these controls. Mailbox busy still applies to every opcode. Software must wait
for an available slot before submitting one; a busy submission is discarded
and sets sticky rejection.

Delivery acknowledgment is not acquisition acceptance or completion. The board
shell must latch `core_error` and the acquisition core's `request_error` into a
coherent response visible to ARM. Reading busy=0 alone must never be treated as
successful acquisition. Both domains share reset; reset aborts delivery and
restores zero configuration except post-count=1 and encode mask=0x3ff.

The 208-bit mailbox payload is `{opcode, cfg11, ..., cfg0}`. `core_start` and
control pulses align with the captured settings. The board must keep this
clock-domain contract and add physical timing/CDC constraints before deployment.
