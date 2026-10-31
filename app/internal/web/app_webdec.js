// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// app_webdec.js — "decode in this browser" on BUS's Stream view
// (ADR-STREAM-CLIENT-DECODE): a web worker (webdec_worker.js) takes the
// scope's digital stream and decodes it here, and the scope's transcript,
// LCD and views show its frames. Kept per browser (localStorage); the
// worker holds the scope's lease only while this page runs it.

const webdec = { worker: null, stat: null };

// webdecFmt is the worker's status as a line.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecFmt(s) {
  if (!s) return "";
  if (!s.active) return s.why || "";
  const wall = Math.max(s.wallS, 1e-3);
  const load = s.busyMs / 10 / wall;
  return "decoding " + s.proto + " here: " + eng(s.words * 4 / wall, "B/s", 2) + " · " + eng(s.edges / wall, " edges/s", 2) +
    " · " + Math.round(load) + " % busy · " + s.frames + " frames · behind " + eng(s.lagS, "s", 2) +
    (s.losses || s.dropped ? " · lost " + (s.losses + s.dropped) : "") + (s.err ? " · " + s.err : "");
}

// webdecSet starts or stops the worker.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecSet(on) {
  try { localStorage.setItem("sds.webdec", on ? "1" : ""); } catch (e) { /* private window: not remembered */ }
  if (on && !webdec.worker) {
    webdec.worker = new Worker("webdec_worker.js");
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
    webdec.worker.onmessage = (ev) => {
      if (ev.data && ev.data.type === "status") webdec.stat = ev.data;
      const t = webdecFmt(webdec.stat);
      if ($("streamWebDecStat").textContent !== t) $("streamWebDecStat").textContent = t;
    };
    webdec.worker.postMessage({ cmd: "start" });
  } else if (!on && webdec.worker) {
    webdec.worker.postMessage({ cmd: "stop" });
    webdec.worker.terminate();
    webdec.worker = null;
    webdec.stat = null;
    $("streamWebDecStat").textContent = "";
  }
  $("streamWebDec").checked = !!on;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
$("streamWebDec").onchange = () => webdecSet($("streamWebDec").checked);
{
  let on = false;
  try { on = localStorage.getItem("sds.webdec") === "1"; } catch (e) { /* off */ }
  if (on) webdecSet(true);
}
