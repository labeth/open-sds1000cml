# Experimental revision 11 streaming ABI

This is experimental revision 11 HDL behind `STREAM_CAPTURE`. Build with
`build.py --interleave --precision --hostfix --stream --experimental --seed=13`.
The seed 13 candidate closes internal timing and passes the explicit bundled
CDC audit. It has been loaded on the device after a cold power cycle and tested
with counter streams. It is not yet the qualified default application image.
The production revision 10 path is unchanged when this define is absent.

## Capture and ownership

Streaming taps each accepted SRAM-format precision word, excluding priming.
It does not read back SRAM or replace the SRAM record. Two-word packets cross
from the 250 MHz capture clock to the 125 MHz RAM clock through an acknowledged
mailbox. Two banks reuse the same 4096-word host RAM used by frozen recall.

Stream mode requires decimation log2 >= 8; raw streaming is rejected on arm.
The two channels retain their 16-bit Q8.8 words. The word sequence starts at
zero on arm and counts sample pairs, independently of the SRAM address counter.

When recording stops, the packetizer flushes any odd final word and seals the
last bank. Empty seals do not manufacture samples. A stopped stream is complete
only after status reports finished and the host has consumed all ready banks.
The capture engine halts on packetizer or bank overrun and exposes the fault.
Old unread banks remain intact, but the stream must be treated as incomplete.

While stream mode is enabled, SRAM read/discard/continuation commands are
rejected. Drain streaming banks, then clear the mode to return the RAM to frozen
recall. Mode configuration is latched while acquisition is idle; do not stage a
mode clear while running, because it will take effect at the next idle point.
Clearing mode or arming again abandons old descriptors and begins a new epoch.

## Registers

Existing CS1 register selectors retain their revision 10 meanings, including
command selector 1, indexed reads 16/17/18 and burst port 25. The following are
additional direction-specific selectors (not byte addresses):

| Access | Selector | Meaning |
|---|---:|---|
| Write | 19 | Mode: bit 0 streams; bit 1 runs continuously until force/halt. Bit 1 requires bit 0. Configure while idle. |
| Write | 20 | Release bank: bit 0 bank number, bit 1 observed publication token. |
| Read | 13 | Revision 11 when STREAM_CAPTURE and HOST_READ_FIX are defined. |
| Read | 32 | Bits 1:0 ready banks; 3:2 tokens; 5:4 last packet has only one valid word; bit 6 fault; bit 7 finished; bit 8 mode enabled. |
| Read | 33,34 | Valid 64-bit packet count in banks 0,1. |
| Read | 35–38 | Bank 0 first stream word ordinal, least-significant 16-bit part first. |
| Read | 39–42 | Bank 1 first stream word ordinal, least-significant part first. |

For bank b, valid word count is `2*packets[b] - last_single[b]`.
Its starting 32-bit buffer index is `b*2048`; its starting burst halfword index
is `b*4096`. Descriptors are stable while ready and unreleased. Read the first
word ordinal and require exact adjacency with the previous block before
accepting data into ARM history. Never release a bank before DMA and CPU copying
have finished. A token is valid only for its currently outstanding descriptor.

The host should prime indexed RAM output at the bank's starting word before
switching to the burst selector, following the revision 10 first-word fix.
It must check the burst pointer and fault state as well as block sequence.

Mode 1 retains normal edge/force trigger behavior. Mode 3 suppresses the record's
edge trigger while recording continuously; command 4 can still force a trigger
and command 5 halts. This keeps SRAM history alongside the ARM stream. Software
trigger/ring-buffer policy and host integration are not implemented yet.

## Physical constraints and current device evidence

`stream.sdc` replaces the broad CPU/PLL cut for descriptor endpoints with
explicit 8 ns maximum and 0 ns minimum delay constraints. Packet mailbox
payloads have the same bounds. `stream_cdc_audit.tcl` checks every surviving
endpoint in all three timing corners, including physical data delay. A negative
control reintroducing the broad cut fails the audit as an untimed endpoint.

Seed 13 RBF SHA256:
`2ff6184c66bdd4b3531569ee872835de24d8548bd3fea7344d5c5add429c6535`.
Evidence is in `validation/2026-09-13-default/stream-device/` and
`stream-timing/` relative to the repository root.

Nine full 2 MiB counter recalls passed after applying indexed-output priming to
legacy revision >=10 recall too. Without it, the initial revision 11 test had
two incorrect block-start words. The original failure is retained.

With temporary CS1 timing cycle=10, access=8, gap=5, counter streams at /1024
and /512 each passed more than 4,194,304 consecutive words (16 MiB), exceeding
SRAM capacity eight times. /256 failed with a streaming overrun during DMA.
The DMA portion alone measures about 10.9 MB/s; this is not a sustainable
end-to-end rate guarantee. The two 8 KiB banks leave only about 1.05 ms to
consume and release a full bank at /256, including scheduling and copying.

`acqsram stream-profile LOG WORDS` checks full counter continuity, drains the
partial final bank, and releases banks only after copying. The optional
`SCOPE_PROFILE_RD_CYCLE`, `SCOPE_PROFILE_RD_ACCESS`, and `SCOPE_PROFILE_GAP`
settings apply temporarily to both recall and stream diagnostics and restore
on exit. Failure halts capture and retains the failed epoch for inspection;
a new arm starts a new epoch.

Remaining work includes /256 overrun diagnosis, longer loaded-system tests,
physical ADC stream mapping and trigger checks, ARM history and conditioning,
and integration with every timebase. Counter tests do not prove analog ENOB
or correct application behavior. Do not promote this candidate to default yet.
