// ENGMODEL-OWNER-UNIT: FU-WEB-APP-INIT
// app_init.js — boot: the workspace layout, the first frame/status poll. Loaded
// LAST so every handler is registered and every function defined. Shares app.js
// globals.
"use strict";

for (const el of document.querySelectorAll(".seg[id]")) segInit(el); // select-like segmented controls
mirrorInit();       // the strip controls' copies in the panes
glInit();           // create the WebGL renderers for the scope + navigator
wsInit();           // lay out the workspaces; SCOPE is shown and holds the scope display
window.addEventListener("resize", resize);
resize();           // sizes the canvas backing stores and paints once
updateRefRows();
updateDecodePanel();
// the phone remote's address, for the top bar's link (REQ-SDS-220): /r is the short form
$("remoteLink").title = "The phone remote: open " + location.host + "/r on your phone. It changes the scope's settings; nothing opens on the scope's screen.";
// Track the ACTUAL laid-out size of the scope box (workspace switches, pane
// resizes, collapse/maximize, late reflow, font load) so the GL backing store
// always matches what CSS displays — window "resize" alone misses layout-only changes.
if (typeof ResizeObserver === "function") {
  let rafPending = 0;
  new ResizeObserver(() => {
    if (rafPending) return;                 // coalesce bursts (a gutter drag)
    rafPending = requestAnimationFrame(() => { rafPending = 0; resize(); });
  }).observe($("scopebox"));
}
// Claim single-active-client control, THEN start polling (opening a second
// browser supersedes us — we show a "refresh to reclaim" overlay and stop).
fetch("/api/claim").then(r => r.json()).then(j => { myEpoch = j.epoch || 0; }).catch(() => {})
  .finally(() => {
    pollFrameBin();   // the only frame transport (binframe.js is an embedded asset)
    pollStatus();
  });
