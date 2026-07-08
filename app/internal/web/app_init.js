// app_init.js — boot: initial layout + first frame/status poll. Loaded LAST so
// every handler is registered and every function defined. Shares app.js globals.
"use strict";

window.addEventListener("resize", resize);
glInit();   // create the WebGL renderers for the scope + navigator
resize();   // sizes the canvas backing stores and paints once
// Track the ACTUAL laid-out size of the scope box (dock collapse/expand, late
// reflow, font load) so the GL backing store always matches what CSS displays —
// window "resize" alone misses layout-only changes.
if (typeof ResizeObserver === "function") {
  let rafPending = 0;
  new ResizeObserver(() => {
    if (rafPending) return;                 // coalesce bursts (e.g. the drawer transition)
    rafPending = requestAnimationFrame(() => { rafPending = 0; resize(); });
  }).observe($("scopebox"));
}
// If binframe.js failed to load (dropped subresource, OTA restart mid-load),
// decodeBinFrame is undefined — pollFrameBin would throw and retry forever
// mistaking it for a network error. Start on JSON instead.
if (typeof decodeBinFrame !== "function") transport = "json";
if (transport === "bin") pollFrameBin(); else pollFrame();
pollStatus();
