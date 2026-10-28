// ENGMODEL-OWNER-UNIT: FU-WEB-APP-VISUAL
// app_visual.js — BUS's Visual view, its core: what the decoded bus means
// kept as its state (visual.js, VIS_KINDS), each new frame's decode taken
// once or, for a bus the scope streams byte for byte, its gap-free stream
// followed; the picture and the side panel drawn by the bus's view
// (app_visual_draw.js, VIS_VIEWS), the MIDI bus sounding (midi_synth.js),
// digital audio from a whole record (app_visual_audio.js). Classic script;
// shares app.js globals.
"use strict";

const vis = {
  proto: "", s: null, layer: null, seq: -1, sy: null, sound: false,
  stream: { next: -1, busy: false, s0: NaN, t0: NaN, rate: 0, on: false },
  wsOrder: "GRB", ps2Kind: "", dbcText: "", dbcName: "", canPick: new Set(),
  audio: { on: false, kind: "auto", res: null, busy: false, live: false, play: null, gain: 0.6, prevTdiv: 0, sampleS: 0, msg: "" },
  drawT: 0, midiLead: 0, autoStream: false,
};

// visShown reports the Visual view on screen.
// TRLC-LINKS: REQ-SDS-278
function visShown() { return typeof ws !== "undefined" && ws.cur === "bus" && ws.busView === "visual" && !document.hidden; }

// visReset starts the view over for the protocol decoded now (the DBC file
// and the LED order stay).
// TRLC-LINKS: REQ-SDS-278
function visReset() {
  vis.proto = dcfg.proto;
  vis.s = visNew(dcfg.proto === "usb" ? "usbls" : dcfg.proto);
  if (vis.s.proto === "can" && vis.dbcText) vis.s.dbc = dbcParse(vis.dbcText);
  if (vis.s.proto === "ps2" && vis.ps2Kind) vis.s.kind = vis.ps2Kind;
  vis.layer = visNew("layer");
  vis.seq = -1;
  vis.stream.next = -1;
  if (vis.sy) synthPanic(vis.sy);
  visSide(true);
}

// visOnDecode takes each new frame's decode once (busOnDecode calls it after
// every decode): its spans into the protocol's state, a layer's notes into
// the values table, the MIDI events into the synth.
// TRLC-LINKS: REQ-SDS-278
function visOnDecode() {
  if (dcfg.proto !== vis.proto || !vis.s) visReset();
  if (!frame || frame.seq === vis.seq) return;
  vis.seq = frame.seq;
  const r = dcfg.result;
  if (!r || !r.ok || !frame.c1) return;
  const following = vis.stream.on && visStreamed(vis.proto);
  const n = frame.c1.length, dt = frameDtS(frame, n), now = performance.now() / 1000;
  if (!following) visIngest(vis.s, r, now - n * dt, dt);
  if (dcfg.layer && typeof busLive !== "undefined") layerNotes(vis.layer, busLive, now);
  visSound();
  if (visShown()) visDraw();
}

// visSound plays the MIDI state's queued events, or drops them while silent.
// TRLC-LINKS: REQ-SDS-279
function visSound() {
  if (!vis.s || !vis.s.out) return;
  midiExpire(vis.s, performance.now() / 1000);
  if (vis.sound && vis.sy) { vis.sy.lead = vis.s.proto === "midi" ? vis.midiLead : 0; synthEvents(vis.sy, vis.s.out); }
  else vis.s.out.length = 0;
}

// ---- the scope's stream, gap-free -------------------------------------------------

// visStreamTick follows the scope's stream while it runs and the view shows a
// byte-wise protocol: new lines from the last one read (from the newest at
// the start), each stamped by its first unit's sample ordinal at the rate the
// ordinals advance (measured over the seconds read), its units into the state.
// TRLC-LINKS: REQ-SDS-278
async function visStreamTick() {
  const S = vis.stream;
  S.on = typeof busScopeStreaming === "function" && busScopeStreaming() && visStreamed(vis.proto);
  if (!S.on || S.busy || !vis.s || document.hidden) return;
  S.busy = true;
  try {
    const r = await fetch("/api/stream/lines?from=" + Math.max(0, S.next) + "&max=4000");
    if (!r.ok) return;
    const p = await r.json(), now = performance.now() / 1000;
    if (S.next < 0) { S.next = p.next; return; } // the newest from here on
    const lines = p.lines || [];
    if (!lines.length) { S.next = p.next; return; }
    const lastS = lines[lines.length - 1].s;
    if (!Number.isFinite(S.s0)) { S.s0 = lastS; S.t0 = now; }
    else if (now - S.t0 > 2 && lastS > S.s0) S.rate = (lastS - S.s0) / (now - S.t0);
    for (const l of lines) {
      const t = S.rate > 0 ? now - (lastS - l.s) / S.rate : now;
      visIngestUnits(vis.s, l.v, t);
    }
    S.next = p.next;
    visSound();
    if (visShown()) visDraw();
  } catch (e) { /* the next tick reads again */ } finally { S.busy = false; }
}
setInterval(visStreamTick, 120);

// visAskStream puts the scope on its gap-free stream of the bus (BUS Stream on
// the scope) or back, the page staying on its Visual view.
// TRLC-LINKS: REQ-SDS-278
function visAskStream(on) {
  if (typeof busStreamAsk !== "undefined") busStreamAsk = { on, until: Date.now() + 5000 };
  if (typeof busScopeWas !== "undefined") busScopeWas = on;
  sheetSet("bus.stream", on ? 1 : 0);
  vis.stream.next = -1; vis.stream.s0 = NaN; vis.stream.rate = 0;
}

// ---- drawing --------------------------------------------------------------------------

// visDraw draws the view: at most every 50 ms; the canvas for the graphics,
// the side panel's text for the figures.
// TRLC-LINKS: REQ-SDS-278
function visDraw(force) {
  const now = performance.now();
  if (!force && now - vis.drawT < 50) return;
  vis.drawT = now;
  const cv = $("visCv");
  if (!cv || !vis.s) return;
  const rc = cv.getBoundingClientRect();
  const W = Math.max(50, Math.round(rc.width * dpr)), H = Math.max(50, Math.round(rc.height * dpr));
  if (cv.width !== W || cv.height !== H) { cv.width = W; cv.height = H; }
  const g = glCardCtx(cv, "#05080c");
  if (!g) return;
  try {
    if (vis.audio.on) visDrawAudio(g, W, H);
    else if (dcfg.layer) visDrawLayer(g, W, H);
    else visDrawProto(g, W, H);
  } finally { glCardEnd(cv); }
  visSide(false);
}

// visDrawProto draws the bus's own picture (its view's), or says the view
// has none for it.
// TRLC-LINKS: REQ-SDS-278
function visDrawProto(g, W, H) {
  const v = visView(vis.s.proto);
  if (v) return v.draw(g, W, H, vis.s);
  visBig(g, W, H, "no picture for this bus", "Packets and Log list it; a protocol layer's values show here");
}

// ---- the side panel ---------------------------------------------------------------------

// visSide writes the side panel: its controls (rebuilt for a new bus, or
// asked: rebuild) and the figures, the view's title and the stream's note.
// TRLC-LINKS: REQ-SDS-278
function visSide(rebuild) {
  const side = $("visSide");
  if (!side || !vis.s) return;
  const p = vis.s.proto, a = vis.audio, v = visView(p), mode = a.on ? "audio" : dcfg.layer ? "layer" : p;
  if (rebuild || side.dataset.p !== mode) {
    side.dataset.p = mode;
    side.innerHTML = a.on ? visAudioControls() : visControls(p);
    visWire(p);
  }
  const fig = $("visFig");
  if (!fig) return;
  const html = a.on ? visAudioFigures() : dcfg.layer ? visLayerTable() : v ? v.figures(vis.s) : "";
  if (fig._html !== html) { fig._html = html; fig.innerHTML = html; }
  if (!a.on && v && v.update) v.update(vis.s);
  $("visTitle").textContent = a.on ? "Digital audio" : dcfg.layer ? "Layer values" : VIS_PROTOS[p] || "Visual";
  const note = $("visStreamNote");
  if (note) note.textContent = vis.stream.on ? "gap-free: following the scope's stream" + (vis.stream.rate > 0 ? "" : " (timing settles in 2 s)") : visStreamed(p) ? "from each capture (gaps between them)" : "";
}

// visControls are the side panel's controls for a bus: the stream's note,
// the bus's own (its view's), Gap-free where the scope streams it, Digital
// audio, then the figures.
// TRLC-LINKS: REQ-SDS-278
function visControls(p) {
  const v = visView(p);
  let h = '<div class="r"><span id="visStreamNote" class="sub grow"></span></div>';
  if (v && v.controls && !dcfg.layer) h += v.controls(vis.s);
  if (visStreamed(p)) h += '<div class="r"><button type="button" id="visGapless" class="xs tog' + (vis.stream.on ? " on" : "") + '" title="The scope streams the bus in hardware: every byte, no gaps between captures">Gap-free (scope streams)</button></div>';
  h += '<div class="r"><button type="button" id="visAudio" class="xs" title="Capture a whole record and decode S/PDIF, I2S or PDM audio from it">Digital audio...</button></div>';
  return h + '<div id="visFig"></div>';
}

// visWire wires the side panel's controls: the core's, the bus's (its
// view's), the audio's.
// TRLC-LINKS: REQ-SDS-278
function visWire(p) {
  // TRLC-LINKS: REQ-SDS-278
  const on = (id, ev, fn) => { const el = $(id); if (el) el[ev] = fn; };
  on("visGapless", "onclick", () => { visAskStream(!vis.stream.on); $("visGapless").classList.toggle("on", !vis.stream.on); });
  on("visAudio", "onclick", () => { vis.audio.on = true; visSide(true); visDraw(true); });
  const v = visView(p);
  if (v && v.wire && !vis.audio.on) v.wire(vis.s, on);
  if (vis.audio.on) visAudioWire(on);
}

// ---- the redraws --------------------------------------------------------------------------

// The view redraws while shown (a MIDI note held sounds on with no new frame).
setInterval(() => { if (visShown()) { if (vis.s && vis.s.held) visSound(); if (!vis.anim) visDraw(); } }, 100);

// visAnimate redraws the piano's notes each display frame while MIDI is shown
// (they move every frame; the other pictures change only with the data).
// TRLC-LINKS: REQ-SDS-278
function visAnimate() {
  vis.anim = visShown() && vis.s && vis.s.proto === "midi" && !vis.audio.on && !dcfg.layer;
  if (vis.anim) visDraw(true);
  requestAnimationFrame(visAnimate);
}
requestAnimationFrame(visAnimate);

// The Full screen button puts the picture and its panel on the whole screen.
// TRLC-LINKS: REQ-SDS-278
if ($("visFull")) $("visFull").onclick = () => {
  const el = $("visGrid");
  if (document.fullscreenElement) document.exitFullscreen();
  else if (el && el.requestFullscreen) el.requestFullscreen().catch(() => {});
};
