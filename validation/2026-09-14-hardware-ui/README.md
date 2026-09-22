# First live SRAM UI checkpoint — manual review requested

Deployed the existing revision-11 FPGA by warm reload from /dev/acq-e2e.rbf.
SHA256 a8edf2305271b238a0b4be66529d8a8a0dfc698e9d10d3ff76ab91babde70797.
This is NOT the new split profile (which still fails timing).

ARM app /dev/acq-e2e-app, SHA256
b9ff39b2b18b10eac19e2681d22e426a96504adaf472d6c7361bc3d117fdf53d.
Main UI revision gate expanded to accept revision 11; finite capture library
already supports this ABI. Uses inherited GPMC fd 5 and key fd 6, one app owner.
SCOPE_CAPTURE=default-sram, SCOPE_EDMA=0, HTTP :8080.
App PID 1190; log /dev/acq-e2e-log; health /dev/acq-e2e-health.
Device code/bitstream/logs are RAM-resident; saved settings use existing USB path.

Scope LCD renderer is active on /dev/fb0. Webpage opened and observed receiving
live frames. Web API controls used by its controls accepted run/stop, timebase,
trigger slope/source and both volts/div changes. Left running at 500 ns/div,
2 V/div both channels. screen.png is the application's screen snapshot.
Physical knobs have not been manually exercised by the agent.

The saved status shows full raw depth 1,048,576 samples/channel, coherent frames,
zero bus errors and no wedge. Recall takes roughly 3.5 seconds using ioctl.
Triangle traces are visible without clipping at the final scale, with strong
ripple; frequency readouts are unreliable. Neither analog accuracy nor interleave
calibration nor high-speed transfer is qualified by this checkpoint.

App/capture/engine Go tests passed. The combined web test invocation had not
finished at handoff; do not claim that suite passed. Geometry optimization and
the new split-profile fit are separate unfinished work.

User requested stopping after the first credible normal hardware/UI flow.
Development is paused for manual review; leave the app running. Full project
goal remains incomplete. Do not treat automatic goal continuation as permission
to resume changes before that review.
