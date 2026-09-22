# Independent precision sample rate

PRECISION now has an explicit samples/s per channel selector. Time/div changes the view and preview span; it no longer changes FPGA decimation, bandwidth or physical capture depth. Supported rates are 500 MS/s divided by powers of two from 16 through 2^20. Averaging count remains independent. Default: 31.25 MS/s/channel. The selected rate is retained across timebase and mode changes within the running app; app restart defaults to 31.25 MS/s.

Real-browser hardware check passed: select 15.625 MS/s, change 500 ns/div to 10 us/div, verify sample rate, 1.171875 MHz passband and 524288/channel depth remain unchanged. Change rate to 31.25 MS/s and verify 10 us/div remains unchanged. Restore 500 ns/div. No page errors or bus errors. Unit tests check rate independence across timebases and hardware-rate selection; engine, DSP, app and focused web tests pass.

Wider views may transfer more preview data and reduce refresh rate. Views larger than the available capture duration are bounded by the retained record; the sample rate is not silently reduced. This supersedes the automatic timebase/rate selection described in the preceding precision report.

Deployed in RAM: /dev/acq-rate-app, SHA256 2d884510ff125f3d43d71c281af3cb2ff0be7a785ec95d3b1bc0de6d65fcc801. No scope internal permanent storage writes. Left running PRECISION, 31.25 MS/s/channel, 16-average, 500 ns/div.
