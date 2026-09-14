# Deep profile composition — in progress

`ENABLE_STREAM=0` excludes the continuous producer and ingress storage from
shared capture RTL. Default 1 preserves the unified composition. Finite capture
and recall reuse their existing implementations. Acquisition operation 1 rejects
without discarding a frozen record in the deep profile.

Passed simulation:
- `test_capture_engine.py`: both transport wrappers, unified five-operation
  sequences and deep wrap/full-address-space/single-word recall. Busy requests
  and incorrect bank-release tokens reject. Deep idle stream requests reject.
  The address space here is AW=13 (8192 words), not physical SRAM qualification.
- `test_acquisition_path.py --deep-only`: 3017 raw words, 48 precision words,
  and repeated precision recall after unsupported stream rejection; immediate
  readiness invalidation on lock/fault/reset/ownership changes.
  ADC/precision primitives are mocked in this integration bench.

Exact source hashes and output are in the adjacent text files. Real-ADC Quartus
probe launched with `build_probe.py --deep-only --cdc --balanced --seed 2` into
`fpga/acq_sram/out/deep-acquisition-path-probe`. The completed seed-2 fit uses 7536 LE, 6110 registers, 26 M9K and
626/645 LABs. Versus retained unified f6095da this saves 1646 LE and 18 M9K.
Setup slack is -2.033 ns; recovery -3.349 ns; hold +0.101 ns. The CDC audit
fails ADC encode source/meta slack at -0.232 ns (slow 85 C corner). Removed
ingress endpoint checks pass. Timing is not closed. No board image or physical capture rate is qualified here.
The unified acquisition integration test also passed: raw 3017, precision 48,
stream 6001 words, ownership and frontend-fault checks.

The deep probe retains host/ADC CDC checks and requires removed ingress CDC
endpoints to have zero registers. Unified builds retain their existing ingress
checks. All timebases remain runtime settings; profile selection belongs to
acquisition mode/protocol family, not timebase changes.
