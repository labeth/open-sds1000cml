// app_init.js — boot: initial layout + first frame/status poll. Loaded LAST so every handler is registered and every function is defined.
"use strict";

// ---- boot: initial layout + first poll ----
// app_wire.js — top-level event wiring + init, loaded AFTER app.js so every definition exists (classic script; shares app.js globals).

"use strict";
window.addEventListener("resize", resize);

resize();
// If binframe.js failed to load (dropped subresource fetch, OTA restart
// mid-page-load), decodeBinFrame is undefined — pollFrameBin would throw and
// retry forever mistaking it for a network error. Start on JSON instead.
if (typeof decodeBinFrame !== "function") transport = "json";
if (transport === "bin") pollFrameBin(); else pollFrame();
pollStatus();
