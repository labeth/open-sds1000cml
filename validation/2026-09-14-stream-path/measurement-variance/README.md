# Preserve tiny fractional ripple in long-record RMS

The ARM measurement path already selects Q8.8 records for web/LCD measurements.
Its variance calculation used E[x²]-E[x]² directly on DC-sized codes. A precision
record of 524288-62 samples at code 60000 with one code increment reproduced
RMS 5.39479660939e-06 versus analytic 5.39511047439e-06 (voltsPerCode=1).

Moments now accumulate differences from the exact first sample code; the mean
adds the reference back. This is one pass, with no extra buffer. Tests cover
three DC levels and the increment at either end. All measure package tests pass.
Validation:
- `go test ./internal/measure -count=1` passes.
- The LCD package passed in the combined consumer run.
- Targeted web measurement/frame API tests pass: MeasCacheReuse,
  PrecisionBinaryPayload, BinFrameParity, BinFrameRawShape, FrameEndpoint,
  DeepFrameServesRawRecord, SRAMFullRecordAndWindowAPI, ACCouplingRemovesDC.
- The broad web run was interrupted with SIGQUIT after several minutes to
  inspect its wait. Its stack showed an external os/exec subprocess wait;
  this does not establish a measurement failure or a passing browser suite.
  Broad browser verification remains unproven; it was not silently skipped.

FPGA timing/default-image/hardware work remains incomplete. No device writes.
