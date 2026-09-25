# Registered acquisition command dispatch

All modes reserve the acquisition on the acceptance edge; registered mode
pulses dispatch to the child one core clock later. Pre/post geometry, recall
range/bias and halt join the existing accepted setting snapshot. Changes to
inputs while pending/active cannot replace accepted settings. A coincident
halt stays held for the operation. ADC data cadence and SRAM capacity do
not change; command launch latency increases one core clock.

Pending reset cancels dispatch. Loss of lock, source/backend fault, bank
ownership or child readiness before dispatch prevents launch and latches
dispatch_fault plus request_error. Public frozen masks pending capture;
done masks pending dispatch and faults to prevent stale completion.

Immediate rejection, pending reset/fault cancellation, coincident finite halt
and immediate busy/not-done assertions pass. Initial integration passed before
the expanded accepted-setting mutation tests. The expanded integration and
first build are running; the first build predates the final done-status mask
and cannot qualify current RTL. Exact final build remains required.

Initial build (before done mask): 9350 LEs, 7829 registers, 44 M9Ks,
645/645 LABs. Setup -4.168 ns, recovery -5.751 ns; existing CDC audit
passes. Worst setup is stream-controller fault through public record_frozen
back into finite-recall state.FAILED. This exposes aggregation feedback;
next candidate separates writer_record_valid from public backend fault status.
The final done-mask integration and pending stream-lock-loss tests pass.

Writer-valid candidate: backend receives finite_record && cfr && !cf &&
!dispatch_capture && !dispatch_fault. Public record_frozen additionally masks
backend fault. Recall already handles host_core_fault and its own failures,
so this removes backend fault aggregation from its frozen input feedback.
Composed integration passes, and directed active-recall host-fault injection
proves immediate public invalidation, local fault retention and transport
drain. First injection attempt observed before a rising edge; corrected test
injects at the preceding falling edge and then checks clocked state. Exact
writer-valid candidate build is running. No current timing qualification.

## Dispatch experiment conclusion

Writer-valid build: 9313 LEs, 7829 registers, 44 M9Ks, 644/645 LABs.
Setup -3.427 ns, hold +0.142 ns, recovery -6.023 ns, removal +0.418 ns,
minimum pulse +1.499 ns. CDC audit fails; exit 3. Fault feedback no longer
dominates, but dispatch remains larger/slower than f6095da (9182 LEs,
-2.816 ns setup). The extra dispatch stage and its tests were archived and
removed from active RTL. Next experiment retains only writer/public validity
separation on the original command interface; active-recall host fault test
remains applicable. No device deployment.
