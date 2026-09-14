# Registered readiness cache — simulation passes, timing fails

Nested client/transport readiness is computed one cycle before a start.
Reset, lock loss, faults, active ownership and outstanding banks immediately
mask cached readiness. Every request consumes readiness, including rejection;
ready can therefore return a clock later after idle or an invalid request.
An assertion requires advertised readiness to imply original client readiness.

Full integration passes raw 3017, precision 48 and stream 6001 word cases,
recall ownership, stale-record rejection and frontend faults. This run predates
added boundary cases but uses the same acquisition RTL. The separate ready-only
run verifies immediate rejection on lock loss, source fault and reset while
readiness was already high. Logs include their distinct bench source hashes.

Balanced seed-2 build completed: 9361 LEs, 7721 registers, 44 M9Ks,
645/645 LABs. Setup -3.471 ns; recovery -5.226 ns; CDC audit fails.
All 31 build source hashes were verified. Compared with committed writer-launch
(-3.284 ns and 642 LABs), this is not an overall improvement or a qualified
baseline. The worst path now starts at record halted/triggered/length and ends
at backend selected_finite, through recall geometry/acceptance logic.

The boundary test was extended after the full integration run to include
backend fault and owned-bank invalidation, and passes; logs record bench hashes.
This readiness-cache RTL remains an uncommitted intermediate candidate for
separating geometry validation from dispatch. No image was deployed.
