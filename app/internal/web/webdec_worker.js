// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// webdec_worker.js — the page's decode of the scope's digital stream
// (ADR-STREAM-CLIENT-DECODE), off the page's thread. It long-polls
// /api/stream/words.bin (which holds the scope's client-decode lease while
// it asks), decodes the words as the scope would (webdec.js) and posts the
// frames to /api/stream/frames.bin; the scope's transcript, LCD and views
// show them. The page starts it with {cmd: "start"} and stops it with
// {cmd: "stop"}; it reports {type: "status", ...} about twice a second.
importScripts("decode.js", "decode_sent.js", "decode_canfd.js", "decode_arinc429.js", "decode_manchester.js", "decode_mil1553.js",
  "decode_usbls.js", "decode_flexray.js", "decode_uartproto.js", "decode_pulsewire.js", "decode_ir.js", "decode_biphase.js",
  "decode_clocked.js", "decode_motion.js", "decode_rfcodes.js", "decode_rfid.js", "decode_wire1.js", "decode_wire2.js", "decode_irmp.js",
  "webdec.js");
webdecInstall(self);

const wd = { on: false, base: "", sess: null, seq: 0, stat: null, statAt: 0, postAt: 0, gen: 0 };

// wdSleep waits ms.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function wdSleep(ms) { return new Promise((r) => setTimeout(r, ms)); }

// wdNewStat is a session's counters.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function wdNewStat(hdr) {
  return { session: hdr ? hdr.session : 0, proto: hdr ? hdr.proto : "", periodS: hdr ? hdr.periodS : 0, words: 0, bytes: 0, frames: 0,
    losses: 0, dropped: 0, lagS: 0, busyMs: 0, t0: performance.now(), err: "" };
}

// wdStatus tells the page how the decode goes.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function wdStatus(active, why) {
  const s = wd.stat || wdNewStat(null);
  const sess = wd.sess;
  const wall = (performance.now() - s.t0) / 1e3;
  postMessage({ type: "status", active, why: why || "", session: s.session, proto: s.proto, words: s.words, bytes: s.bytes,
    frames: s.frames, losses: s.losses, dropped: s.dropped, lagS: s.lagS, wallS: wall, busyMs: s.busyMs,
    decodeMs: sess ? sess.decodeMs : 0, samples: sess ? sess.samplesIn : 0, edges: sess ? sess.edgesIn : 0, err: s.err });
}

// wdPost sends the session's frames (and the words it lost) to the scope.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
async function wdPost(force) {
  const sess = wd.sess;
  if (!sess) return true;
  const frames = sess.takeFrames();
  const losses = sess.losses.splice(0);
  const now = performance.now();
  if (!frames.length && !losses.length && !force && now - wd.postAt < 500) return true;
  wd.postAt = now;
  wd.stat.frames += frames.length;
  wd.stat.losses += losses.length;
  const t0 = performance.now();
  const body = webdecFramesBody(sess.info.session, sess.position(), frames, losses);
  wd.stat.busyMs += performance.now() - t0;
  try {
    const r = await fetch(wd.base + "/api/stream/frames.bin", { method: "POST", body, headers: { "Content-Type": "application/octet-stream" } });
    if (r.status === 409) return false; // the scope took the decode back, or the stream ended
  } catch (e) {
    wd.stat.err = "post: " + (e && e.message);
  }
  return true;
}

// wdLoop reads, decodes and posts until stopped.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
async function wdLoop(gen) {
  while (wd.on && gen === wd.gen) {
    let buf;
    try {
      const r = await fetch(wd.base + "/api/stream/words.bin?from=" + wd.seq + "&max=" + (1 << 20) + "&wait=1000", { cache: "no-store" });
      if (!r.ok) {
        wdStatus(false, "the scope has no client decode (" + r.status + ")");
        await wdSleep(2000);
        continue;
      }
      buf = await r.arrayBuffer();
    } catch (e) {
      // Wi-Fi dropped or the scope restarted: the scope takes the decode
      // back after its lease; a loss shows where words went missing.
      wdStatus(false, "no connection to the scope");
      await wdSleep(500);
      continue;
    }
    const t0 = performance.now();
    let rep;
    try {
      rep = webdecParse(buf);
    } catch (e) {
      wdStatus(false, "bad reply: " + e.message);
      await wdSleep(1000);
      continue;
    }
    const hdr = rep.hdr;
    if (!hdr.active) {
      if (wd.sess) await wdPost(true);
      wd.sess = null;
      wd.seq = hdr.next;
      wdStatus(false, hdr.why || "the scope streams no digital bus");
      continue;
    }
    if (!wd.sess || wd.sess.info.session !== hdr.session) {
      try {
        wd.sess = new WebDecSession(hdr, self);
      } catch (e) {
        wdStatus(false, e.message);
        await wdSleep(2000);
        continue;
      }
      if (wd.stat) wdStatus(true); // the last word on the session before
      wd.stat = wdNewStat(hdr);
    } else if (hdr.dropped) wd.stat.dropped += hdr.dropped;
    for (const c of rep.chunks) {
      wd.sess.feedChunk(c);
      wd.stat.words += c.words;
      wd.stat.bytes += c.bytes;
    }
    wd.seq = hdr.next;
    wd.stat.lagS = hdr.lag * hdr.periodS;
    wd.stat.busyMs += performance.now() - t0;
    if (!(await wdPost(false))) {
      wd.sess = null;
      continue;
    }
    if (performance.now() - wd.statAt > 500) {
      wd.statAt = performance.now();
      wdStatus(true);
    }
  }
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
onmessage = (ev) => {
  const m = ev.data || {};
  if (m.cmd === "start" && !wd.on) {
    wd.on = true;
    wd.base = m.base || "";
    wd.gen++;
    wdLoop(wd.gen);
  } else if (m.cmd === "stop") {
    wd.on = false;
    wd.sess = null;
    wdStatus(false, "stopped");
  }
};
