# Experimental stream path integration — no device load

This checkpoint connects the acknowledged packetizer and two-bank controller
to top.v behind STREAM_CAPTURE. The packetizer observes real accepted SRAM
write words, excluding priming. It writes the shared host RAM only in streaming
mode. Frozen SRAM operations are rejected until that mode is disabled.

`fpga/acq_sram/STREAMING.md` defines the experimental revision 11 registers and
remaining integration work. Build/deployment is not enabled: physical CDC bounds,
implementation timing, host support and device qualification remain outstanding.

## Evidence

- `packetizer.json`: production-size and small-bank composition tests with
  250/125/80 MHz clocks. Covers zero/odd/even lengths, simultaneous last word and
  stop, multi-bank continuity, mailbox overload failure and reset recovery.
- `integration.json`: legacy top-level GPMC test reads 8192 halfwords correctly
  with skewed CS/OE releases and pointer wrap. Experimental top-level test
  streams 8193 consecutive words, including the odd tail; independently checks
  that SRAM contains every same word; rejects recall during stream ownership;
  accepts recall after disabling streaming; and verifies normal capture again.

The top-level stream test uses a rate-controlled precision-source stub and the
existing ideal SRAM model. It tests ownership, word alignment and command
semantics, not ADC conversion accuracy, physical SRAM timing, metastability or
actual GPMC streaming throughput. Those remain hardware qualification items.

The registered publication token holds payload stable across clock domains, but
the implementation must still bound bundled-data delay. Installed Quartus 21.1
help confirms set_clock_groups cuts all paths between groups and has no
allow_paths option. Existing broad cuts must be replaced/qualified for the new
host descriptors before loading an experimental image.
