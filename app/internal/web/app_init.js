// app_init.js — boot: initial layout + first frame/status poll. Loaded LAST so
// every handler is registered and every function defined. Shares app.js globals.
"use strict";

window.addEventListener("resize", resize);
glInit();   // set up the GPU trace layer (silent no-op → 2D fallback if unavailable)
resize();   // sizes both canvases (incl. the GL layer) and paints once
// If binframe.js failed to load (dropped subresource, OTA restart mid-load),
// decodeBinFrame is undefined — pollFrameBin would throw and retry forever
// mistaking it for a network error. Start on JSON instead.
if (typeof decodeBinFrame !== "function") transport = "json";
if (transport === "bin") pollFrameBin(); else pollFrame();
pollStatus();
