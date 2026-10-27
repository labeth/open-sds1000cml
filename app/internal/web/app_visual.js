// ENGMODEL-OWNER-UNIT: FU-WEB-APP-VISUAL
// app_visual.js — BUS's Visual view: what the decoded bus means, drawn
// (visual.js keeps the state): a MIDI keyboard and piano roll that can sound
// (midi_synth.js), DMX levels, a PS/2 keyboard or mouse, an LED strip, an IR
// remote's buttons, a PWM gauge or a servo, an encoder's or stepper's
// position, a caliper's reading, the DCF77 time, a terminal, the USB PD
// contract, Wiegand cards, CAN signals from a DBC file, a protocol layer's
// values; and digital audio (S/PDIF, I2S, PDM) captured as a whole sampled
// record (/api/record.bin), measured and played. Each new frame's decode is
// taken once; with the scope streaming the bus (gap-free) the byte-wise
// protocols follow its stream instead. Classic script; shares app.js globals.
"use strict";

const vis = {
  proto: "", s: null, layer: null, seq: -1, sy: null, sound: false,
  stream: { next: -1, busy: false, s0: NaN, t0: NaN, rate: 0, on: false },
  wsOrder: "GRB", ps2Kind: "", dbcText: "", dbcName: "", canPick: new Set(),
  audio: { on: false, kind: "auto", res: null, busy: false, live: false, play: null, gain: 0.6, prevTdiv: 0, sampleS: 0, msg: "" },
  drawT: 0, midiLead: 0, autoStream: false,
};

// VIS_STREAMED are the protocols the scope's stream carries byte for byte
// that the view reads gap-free (visual.js visIngestUnits).
const VIS_STREAMED = ["midi", "dmx512", "ps2", "uart"];

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
  const following = vis.stream.on && VIS_STREAMED.includes(vis.proto);
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
  S.on = typeof busScopeStreaming === "function" && busScopeStreaming() && VIS_STREAMED.includes(vis.proto);
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

// VIS_CH_COLOURS colour MIDI's sixteen channels.
const VIS_CH_COLOURS = ["#4fc3f7", "#ffb74d", "#81c784", "#e57373", "#ba68c8", "#fff176", "#4db6ac", "#f06292",
  "#9575cd", "#a1887f", "#aed581", "#64b5f6", "#ff8a65", "#90a4ae", "#dce775", "#7986cb"];

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

// visDrawProto draws the protocol's own picture.
// TRLC-LINKS: REQ-SDS-278
function visDrawProto(g, W, H) {
  const s = vis.s;
  switch (s.proto) {
    case "midi": return visDrawMIDI(g, W, H, s);
    case "dmx512": return visDrawDMX(g, W, H, s);
    case "ps2": return s.kind === "mouse" ? visDrawMouse(g, W, H, s) : visDrawKeyboard(g, W, H, s);
    case "ws281x": return visDrawLEDs(g, W, H, s);
    case "irnec": case "irsirc": case "irrc5": case "irrc6": case "irmp":
      return visBig(g, W, H, s.lastBtn ? "A " + s.lastBtn.addr.toString(16).toUpperCase() + "  C " + s.lastBtn.cmd.toString(16).toUpperCase() : "no button yet",
        s.lastBtn ? s.lastBtn.name + (s.lastBtn.repeats ? " · held, " + s.lastBtn.repeats + " repeats" : "") + " · pressed " + s.lastBtn.count + "x" : "press a button on the remote");
    case "pwm": return visDrawPWM(g, W, H, s);
    case "quad": case "stepper": return visDrawDial(g, W, H, s);
    case "caliper": return visBig(g, W, H, s.text || "--", s.hist.length ? "min " + Math.min(...s.hist.map((p) => p.v)) + "  max " + Math.max(...s.hist.map((p) => p.v)) + " " + s.unit : "", s.hist);
    case "dcf77": return visBig(g, W, H, s.time || "waiting for a minute", s.bad ? "a parity failed: the time is not sure" : s.time ? "DCF77 time and date" : visDcfHint(s));
    case "usbpd": return visDrawPD(g, W, H, s);
    case "wiegand": return visBig(g, W, H, s.lastCard ? "card " + s.lastCard.card : "no card yet",
      s.lastCard ? s.lastCard.bits + "-bit · facility " + s.lastCard.fac + " · read " + s.lastCard.count + "x" : "present a card to the reader");
    case "can": case "canfd": return visDrawCAN(g, W, H, s);
    case "uart": return visBig(g, W, H, "", "the terminal is beside");
    case "lin": return visDrawFrames(g, W, H, s, "the identifiers' newest responses; checksum failures count beside");
  }
  return visBig(g, W, H, VIS_PROTOS[s.proto] ? "" : "no picture for this bus", "Packets and Log list it; a protocol layer's values show here");
}

// visDcfHint says what the time needs: a whole minute (60 of the bus's
// seconds) after the record's trigger, so two minutes in the record, and the
// timebase step that holds them.
// TRLC-LINKS: REQ-SDS-278
function visDcfHint(s) {
  const sec = s.secS > 0 ? s.secS : dcfg.baud > 0 ? 1 / dcfg.baud : 0;
  if (!(sec > 0)) return "a whole minute (60 of its seconds) must fit the record";
  const need = (120 * sec) / 14, td = (st && st.tdiv_s) || 0;
  const step = ((st && st.tdivs) || []).filter((t) => t >= need * 0.999).sort((a, b) => a - b)[0] || need;
  return "a minute takes " + fmtTdiv(60 * sec) + (td >= need * 0.999 ? "" : ": " + fmtTdiv(step) + "/div holds one");
}

// visDrawFrames draws the identifiers seen as tiles, each with its newest
// bytes and count, brighter the newer: a bus's traffic at a glance.
// TRLC-LINKS: REQ-SDS-278
function visDrawFrames(g, W, H, s, hint) {
  const all = [...s.frames.entries()].sort((a, b) => a[0] - b[0]);
  if (!all.length) return visBig(g, W, H, "no frame yet", hint);
  const cols = Math.max(1, Math.min(4, Math.floor(W / (220 * dpr)))), rows = Math.ceil(Math.min(all.length, 48) / cols);
  const tw = W / cols, th = Math.min(46 * dpr, (H - 18 * dpr) / rows), now = performance.now() / 1000;
  all.slice(0, cols * rows).forEach(([id, f], k) => {
    const x = (k % cols) * tw, y = Math.floor(k / cols) * th, age = Math.max(0, now - f.t);
    g.globalAlpha = Math.max(0.25, 1 - age / 5);
    g.fillStyle = "#1f2a36"; g.fillRect(x + 2 * dpr, y + 2 * dpr, tw - 4 * dpr, th - 4 * dpr);
    g.fillStyle = "#4fc3f7"; g.font = Math.round(12 * dpr) + "px monospace"; g.textAlign = "left"; g.textBaseline = "top";
    g.fillText(id.toString(16).toUpperCase().padStart(2, "0") + "  ×" + f.count, x + 8 * dpr, y + 6 * dpr);
    g.fillStyle = "#e6edf3"; g.font = Math.round(11 * dpr) + "px monospace";
    g.fillText(f.bytes.map(visHex2).join(" ").slice(0, Math.floor(tw / (7 * dpr))), x + 8 * dpr, y + 22 * dpr);
  });
  g.globalAlpha = 1;
  g.fillStyle = "#8b98a5"; g.font = Math.round(11 * dpr) + "px monospace"; g.textAlign = "center"; g.textBaseline = "bottom";
  g.fillText(hint, W / 2, H - 2 * dpr);
}

// visBig writes a large reading and a line under it, with a history graph.
// TRLC-LINKS: REQ-SDS-278
function visBig(g, W, H, big, small, hist) {
  g.fillStyle = "#e6edf3"; g.textAlign = "center"; g.textBaseline = "middle";
  const p = Math.max(14, Math.min(H * 0.22, W / Math.max(6, big.length) * 1.6));
  g.font = Math.round(p) + "px monospace";
  g.fillText(big, W / 2, H * (hist && hist.length > 1 ? 0.3 : 0.42));
  g.font = Math.round(12 * dpr) + "px monospace"; g.fillStyle = "#8b98a5";
  g.fillText(small, W / 2, H * (hist && hist.length > 1 ? 0.5 : 0.62));
  if (hist && hist.length > 1) visSpark(g, 12 * dpr, H * 0.6, W - 24 * dpr, H * 0.35, hist, "#4fc3f7");
}

// visSpark draws a history (points {t, v}) as a line, its range written.
// TRLC-LINKS: REQ-SDS-278
function visSpark(g, x, y, w, h, hist, col) {
  if (hist.length < 2) return;
  let lo = Infinity, hi = -Infinity;
  for (const p of hist) { lo = Math.min(lo, p.v); hi = Math.max(hi, p.v); }
  if (hi === lo) { hi += 1; lo -= 1; }
  const t0 = hist[0].t, t1 = hist[hist.length - 1].t || t0 + 1;
  g.strokeStyle = "#263039"; g.lineWidth = dpr; g.strokeRect(x, y, w, h);
  g.strokeStyle = col; g.lineWidth = 1.5 * dpr; g.beginPath();
  hist.forEach((p, i) => {
    const px = x + (t1 > t0 ? (p.t - t0) / (t1 - t0) : i / (hist.length - 1)) * w, py = y + h - (p.v - lo) / (hi - lo) * h;
    if (i === 0) g.moveTo(px, py); else g.lineTo(px, py);
  });
  g.stroke();
  g.fillStyle = "#8b98a5"; g.font = Math.round(10 * dpr) + "px monospace"; g.textAlign = "left"; g.textBaseline = "top";
  g.fillText(visNum(hi), x + 3 * dpr, y + 2 * dpr);
  g.textBaseline = "bottom"; g.fillText(visNum(lo), x + 3 * dpr, y + h - 2 * dpr);
}

// visNum writes a value briefly.
// TRLC-LINKS: REQ-SDS-278
function visNum(v) { return Math.abs(v) >= 1000 || Number.isInteger(v) ? String(Math.round(v * 100) / 100) : v.toPrecision(4); }

// VIS_MIDI_RISE is the seconds a played note takes to rise off the picture.
const VIS_MIDI_RISE = 4;

// visDrawMIDI draws an 88-key piano and its notes, each a bar in its
// channel's colour as long as it is held, and a legend of the channels heard
// with their instruments. Live (no lead) a note sounds and presses its key as
// it comes, its bar growing up from the key while held, then rising away and
// fading; with a lead it falls from the top and lands on its key lead seconds
// later, when it sounds (the sound waits as long).
// TRLC-LINKS: REQ-SDS-278
function visDrawMIDI(g, W, H, s) {
  const lo = 21, hi = 108, now = performance.now() / 1000, lead = vis.midiLead;
  const whites = [];
  for (let n = lo; n <= hi; n++) if (![1, 3, 6, 8, 10].includes(n % 12)) whites.push(n);
  const ww = W / whites.length;
  // a white key is about five and a half times as long as it is wide, the
  // keyboard never more than a third of the picture
  const kbH = Math.max(36 * dpr, Math.min(H / 3, ww * 5.5)), felt = 3 * dpr, legH = 20 * dpr;
  const keyTop = H - kbH, hitY = keyTop - felt, rollH = hitY - legH;
  // TRLC-LINKS: REQ-SDS-278
  const keyX = (n) => {
    const bl = [1, 3, 6, 8, 10].includes(n % 12);
    const wi = whites.indexOf(bl ? n - 1 : n);
    return bl ? { x: (wi + 1) * ww - ww * 0.31, w: ww * 0.62, black: true } : { x: wi * ww, w: ww, black: false };
  };
  const rise = !(lead > 0), span = rise ? VIS_MIDI_RISE : lead;
  // a time's height: live, now at the keys and the past above; falling, now
  // at the top and lead seconds ago at the keys
  // TRLC-LINKS: REQ-SDS-278
  const yOf = (t) => rise ? hitY - ((now - t) / span) * rollH : hitY - ((t - (now - lead)) / lead) * rollH;
  g.fillStyle = "#070a0f"; g.fillRect(0, 0, W, hitY);
  for (let k = 0; k < whites.length; k++) {
    const m = whites[k] % 12;
    if (m === 0 || m === 5) { g.fillStyle = m === 0 ? "#18222e" : "#10171f"; g.fillRect(Math.round(k * ww), legH, Math.max(1, dpr), rollH); }
  }
  const lit = new Map(), chans = new Set();
  // white keys' notes first, black keys' over them, as on the keyboard
  for (const pass of [false, true]) {
    for (const n of s.roll) {
      if (n.note < lo || n.note > hi) continue;
      const k = keyX(n.note);
      if (k.black !== pass) continue;
      chans.add(n.ch);
      const held = !Number.isFinite(n.t1), t1 = held ? now : n.t1;
      if (t1 < now - span) continue; // gone: risen off the top, or into its key
      let yb, yt, fade = 1;
      if (rise) {
        if (held || n.t1 > now) lit.set(n.note, n);
        yb = Math.min(hitY, yOf(t1)); yt = Math.max(legH, yOf(n.t0));
        fade = 1 - Math.max(0, now - t1) / span; // fading as it goes
      } else {
        if (n.t0 <= now - lead) lit.set(n.note, n);
        yb = Math.min(hitY, yOf(n.t0)); yt = Math.max(legH, yOf(t1));
      }
      if (yb - yt < 1 && !(rise && held)) continue;
      const pad = k.black ? ww * 0.04 : ww * 0.1, x = k.x + pad, w = Math.max(2 * dpr, k.w - 2 * pad), h = Math.max(3 * dpr, yb - yt);
      const c = VIS_CH_COLOURS[n.ch];
      g.globalAlpha = (0.6 + 0.4 * n.vel / 127) * fade; g.fillStyle = c; g.fillRect(x, yb - h, w, h);
      g.globalAlpha = 0.45 * fade; g.fillStyle = "#ffffff"; g.fillRect(x, rise ? yb - h : yb - Math.min(h, 2 * dpr), w, Math.min(h, 2 * dpr)); // a lit edge leading
      g.globalAlpha = 0.35 * fade; g.fillStyle = "#000000"; g.fillRect(x + w - Math.max(1, dpr), yb - h, Math.max(1, dpr), h); // a shaded side
    }
  }
  g.globalAlpha = 1;
  // the glow where a sounding note meets its key
  for (const [note, n] of lit) {
    const k = keyX(note), c = VIS_CH_COLOURS[n.ch];
    g.fillStyle = c;
    g.globalAlpha = 0.18; g.fillRect(k.x - ww * 0.2, hitY - 22 * dpr, k.w + ww * 0.4, 22 * dpr);
    g.globalAlpha = 0.35; g.fillRect(k.x, hitY - 9 * dpr, k.w, 9 * dpr);
  }
  g.globalAlpha = 1;
  g.fillStyle = "#7a1c22"; g.fillRect(0, hitY, W, felt); // the felt under the fallboard
  // the white keys: a face, a gap between, a lip at the front; pressed they take the note's colour
  g.fillStyle = "#2a2a2a"; g.fillRect(0, keyTop, W, kbH);
  const gap = Math.max(1, Math.round(0.6 * dpr));
  for (const n of whites) {
    const k = keyX(n), h = lit.get(n);
    g.fillStyle = h ? VIS_CH_COLOURS[h.ch] : "#f3f1ea"; g.fillRect(k.x + gap, keyTop, k.w - 2 * gap, kbH - 2 * dpr);
    if (!h) { g.fillStyle = "#d4d1c6"; g.fillRect(k.x + gap, keyTop + kbH - 6 * dpr, k.w - 2 * gap, 4 * dpr); }
    else { g.globalAlpha = 0.25; g.fillStyle = "#000000"; g.fillRect(k.x + gap, keyTop, k.w - 2 * gap, 4 * dpr); g.globalAlpha = 1; }
    if (n % 12 === 0 && ww >= 9 * dpr) {
      g.fillStyle = h ? "#10161c" : "#8a8676"; g.font = Math.round(Math.min(10 * dpr, ww * 0.55)) + "px monospace"; g.textAlign = "center"; g.textBaseline = "bottom";
      g.fillText("C" + (n / 12 - 1), k.x + k.w / 2, keyTop + kbH - 8 * dpr);
    }
  }
  g.globalAlpha = 0.35; g.fillStyle = "#000000"; g.fillRect(0, keyTop, W, 3 * dpr); g.globalAlpha = 1; // the fallboard's shadow
  // the black keys: a body, a lighter top face, a front bevel
  const bh = kbH * 0.62;
  for (let n = lo; n <= hi; n++) {
    const k = keyX(n);
    if (!k.black) continue;
    const h = lit.get(n);
    g.fillStyle = "#0c0c0f"; g.fillRect(k.x, keyTop, k.w, bh);
    g.fillStyle = h ? VIS_CH_COLOURS[h.ch] : "#2b2b33"; g.fillRect(k.x + k.w * 0.12, keyTop, k.w * 0.76, bh - 5 * dpr);
    if (!h) { g.fillStyle = "#44444e"; g.fillRect(k.x + k.w * 0.12, keyTop + bh - 9 * dpr, k.w * 0.76, 3 * dpr); }
  }
  // the legend: each channel heard, its colour and its instrument
  g.font = Math.round(11 * dpr) + "px monospace"; g.textBaseline = "middle"; g.textAlign = "left";
  let lx = 8 * dpr;
  for (const ch of [...chans].sort((a, b) => a - b)) {
    const label = (ch + 1) + " " + midiInstrument(ch, s.prog[ch]);
    g.fillStyle = VIS_CH_COLOURS[ch]; g.fillRect(lx, legH / 2 - 5 * dpr, 10 * dpr, 10 * dpr);
    g.fillStyle = "#c9d1d9"; g.fillText(label, lx + 14 * dpr, legH / 2);
    lx += 14 * dpr + (label.length + 2) * 6.6 * dpr;
  }
  if (!s.roll.length) {
    g.fillStyle = "#8b98a5"; g.textAlign = "center";
    g.fillText(rise ? "a note presses its key as it plays, then rises away: Sound plays it" : "notes fall onto the keys: Sound plays them as they land", W / 2, legH + rollH / 2);
  }
}

// visDrawDMX draws the universe's 512 levels as bars, 32 a row, a bar just
// changed outlined; the channel number every eighth.
// TRLC-LINKS: REQ-SDS-278
function visDrawDMX(g, W, H, s) {
  const cols = 32, rows = 16, cw = W / cols, ch = H / rows, now = performance.now() / 1000;
  g.font = Math.round(Math.min(10 * dpr, cw / 3)) + "px monospace"; g.textBaseline = "top"; g.textAlign = "left";
  for (let i = 0; i < 512; i++) {
    const x = (i % cols) * cw, y = Math.floor(i / cols) * ch, v = s.levels[i];
    g.fillStyle = i < s.slots ? "#101820" : "#080b0f"; g.fillRect(x + dpr, y + dpr, cw - 2 * dpr, ch - 2 * dpr);
    if (v > 0) { g.fillStyle = "hsl(" + (200 - v / 255 * 160) + ",70%,55%)"; const bh = (ch - 2 * dpr) * v / 255; g.fillRect(x + dpr, y + ch - dpr - bh, cw - 2 * dpr, bh); }
    if (now - s.changed[i] < 0.5) { g.strokeStyle = "#fff"; g.lineWidth = dpr; g.strokeRect(x + dpr, y + dpr, cw - 2 * dpr, ch - 2 * dpr); }
    if (i % 8 === 0 || cw > 30 * dpr) { g.fillStyle = "#6b7884"; g.fillText(String(i + 1), x + 2 * dpr, y + 2 * dpr); }
    if (cw > 30 * dpr && ch > 26 * dpr) { g.fillStyle = "#e6edf3"; g.textBaseline = "bottom"; g.fillText(String(v), x + 2 * dpr, y + ch - 2 * dpr); g.textBaseline = "top"; }
  }
}

// visDrawKeyboard draws a keyboard, the keys held lit.
// TRLC-LINKS: REQ-SDS-278
function visDrawKeyboard(g, W, H, s) {
  const u = Math.min(W / 23.2, H / 6.6), ox = (W - 23 * u) / 2, oy = (H - 6.5 * u) / 2;
  g.textAlign = "center"; g.textBaseline = "middle";
  for (const k of PS2_LAYOUT) {
    const on = s.keys.has(k.code), x = ox + k.x * u, y = oy + k.y * u;
    g.fillStyle = on ? "#4fc3f7" : "#1b2430"; g.fillRect(x + 1.5 * dpr, y + 1.5 * dpr, k.w * u - 3 * dpr, u - 3 * dpr);
    g.fillStyle = on ? "#05080c" : "#c9d3dc";
    g.font = Math.round(Math.min(u * 0.32, (k.w * u) / Math.max(2, k.label.length) * 1.4)) + "px monospace";
    g.fillText(k.label, x + k.w * u / 2, y + u / 2);
  }
}

// visDrawMouse draws the mouse's moves (the trail, newest bright), its
// buttons and the wheel.
// TRLC-LINKS: REQ-SDS-278
function visDrawMouse(g, W, H, s) {
  const tr = s.trail;
  let x0 = Infinity, x1 = -Infinity, y0 = Infinity, y1 = -Infinity;
  for (const p of tr) { x0 = Math.min(x0, p.x); x1 = Math.max(x1, p.x); y0 = Math.min(y0, p.y); y1 = Math.max(y1, p.y); }
  const span = Math.max(50, x1 - x0, y1 - y0), sc = Math.min(W * 0.7, H * 0.85) / span, cx = W * 0.4 - (x0 + x1) / 2 * sc, cy = H / 2 + (y0 + y1) / 2 * sc;
  g.lineWidth = 2 * dpr;
  for (let i = 1; i < tr.length; i++) {
    g.globalAlpha = 0.15 + 0.85 * i / tr.length; g.strokeStyle = tr[i].btn ? "#ffb74d" : "#4fc3f7";
    g.beginPath(); g.moveTo(cx + tr[i - 1].x * sc, cy - tr[i - 1].y * sc); g.lineTo(cx + tr[i].x * sc, cy - tr[i].y * sc); g.stroke();
  }
  g.globalAlpha = 1;
  const bx = W * 0.8, by = H * 0.3, bw = W * 0.06, bh = H * 0.25;
  ["L", "M", "R"].forEach((n, i) => {
    const on = (s.btn >> [0, 2, 1][i]) & 1;
    g.fillStyle = on ? "#ffb74d" : "#1b2430"; g.fillRect(bx + i * bw * 1.1 - bw * 1.6, by, bw, bh);
    g.fillStyle = "#c9d3dc"; g.font = Math.round(12 * dpr) + "px monospace"; g.textAlign = "center"; g.textBaseline = "middle"; g.fillText(n, bx + i * bw * 1.1 - bw * 1.1, by + bh / 2);
  });
  g.fillStyle = "#8b98a5"; g.textAlign = "center";
  g.fillText("x " + s.x + "  y " + s.y + (s.wheel ? "  wheel " + s.wheelPos : ""), bx - bw * 0.5, by + bh + 20 * dpr);
}

// visDrawLEDs draws the strip's LEDs in their colours, as many a row as fit.
// TRLC-LINKS: REQ-SDS-278
function visDrawLEDs(g, W, H, s) {
  const leds = wsColours(s.leds, vis.wsOrder);
  if (!leds.length) return visBig(g, W, H, "no frame latched yet", "an LED frame ends with its reset");
  const per = Math.max(8, Math.min(leds.length, Math.ceil(Math.sqrt(leds.length * W / H)))), rows = Math.ceil(leds.length / per);
  const d = Math.min(W / per, H / rows), r = d * 0.42;
  leds.forEach((c, i) => {
    const x = (i % per) * d + d / 2 + (W - per * d) / 2, y = Math.floor(i / per) * d + d / 2 + (H - rows * d) / 2;
    g.fillStyle = "#1b2430"; g.beginPath(); g.arc(x, y, r + dpr, 0, 2 * Math.PI); g.fill();
    g.fillStyle = "rgb(" + c[0] + "," + c[1] + "," + c[2] + ")"; g.beginPath(); g.arc(x, y, r, 0, 2 * Math.PI); g.fill();
    if (d > 26 * dpr) { g.fillStyle = "#8b98a5"; g.font = Math.round(9 * dpr) + "px monospace"; g.textAlign = "center"; g.textBaseline = "top"; g.fillText(String(i + 1), x, y + r + dpr); }
  });
}

// visDrawPWM draws the duty cycle as a gauge (a servo's angle as a pointer
// beside it), and its history.
// TRLC-LINKS: REQ-SDS-278
function visDrawPWM(g, W, H, s) {
  const cx = W * 0.3, cy = H * 0.62, r = Math.min(W * 0.25, H * 0.5);
  // TRLC-LINKS: REQ-SDS-278
  const arc = (v, col, w) => { g.strokeStyle = col; g.lineWidth = w; g.beginPath(); g.arc(cx, cy, r, Math.PI, Math.PI + Math.PI * v); g.stroke(); };
  arc(1, "#1b2430", 14 * dpr);
  if (Number.isFinite(s.duty)) arc(Math.max(0, Math.min(1, s.duty / 100)), "#4fc3f7", 14 * dpr);
  g.fillStyle = "#e6edf3"; g.textAlign = "center"; g.textBaseline = "middle"; g.font = Math.round(r * 0.3) + "px monospace";
  g.fillText(Number.isFinite(s.duty) ? s.duty.toFixed(1) + " %" : "--", cx, cy - r * 0.2);
  g.font = Math.round(12 * dpr) + "px monospace"; g.fillStyle = "#8b98a5";
  g.fillText(Number.isFinite(s.freq) ? eng(s.freq, "Hz", 4) : "", cx, cy + 16 * dpr);
  const a = pwmServo(s);
  if (Number.isFinite(a)) {
    const sx = W * 0.75, sy = H * 0.62, sr = Math.min(W * 0.18, H * 0.45), ang = Math.PI + a / 180 * Math.PI;
    g.strokeStyle = "#1b2430"; g.lineWidth = 3 * dpr; g.beginPath(); g.arc(sx, sy, sr, Math.PI, 2 * Math.PI); g.stroke();
    g.strokeStyle = "#ffb74d"; g.lineWidth = 4 * dpr; g.beginPath(); g.moveTo(sx, sy); g.lineTo(sx + Math.cos(ang) * sr, sy + Math.sin(ang) * sr); g.stroke();
    g.fillStyle = "#e6edf3"; g.fillText("servo " + a.toFixed(0) + "°", sx, sy + 16 * dpr);
  }
  if (s.hist.length > 1) visSpark(g, 12 * dpr, 6 * dpr, W - 24 * dpr, H * 0.18, s.hist, "#4fc3f7");
}

// visDrawDial draws an encoder's or a stepper's position on a dial (a turn
// of 200 steps, the count written) and its history.
// TRLC-LINKS: REQ-SDS-278
function visDrawDial(g, W, H, s) {
  const cx = W * 0.3, cy = H / 2, r = Math.min(W * 0.22, H * 0.4), a = (s.pos % 200) / 200 * 2 * Math.PI - Math.PI / 2;
  g.strokeStyle = "#1b2430"; g.lineWidth = 6 * dpr; g.beginPath(); g.arc(cx, cy, r, 0, 2 * Math.PI); g.stroke();
  g.strokeStyle = "#4fc3f7"; g.lineWidth = 4 * dpr; g.beginPath(); g.moveTo(cx, cy); g.lineTo(cx + Math.cos(a) * r, cy + Math.sin(a) * r); g.stroke();
  g.fillStyle = "#e6edf3"; g.textAlign = "center"; g.textBaseline = "middle"; g.font = Math.round(r * 0.35) + "px monospace";
  g.fillText(String(s.pos), W * 0.72, H * 0.35);
  g.font = Math.round(12 * dpr) + "px monospace"; g.fillStyle = "#8b98a5";
  g.fillText((s.fwd ? "forward" : "reverse") + " · " + Math.abs(s.speed).toFixed(0) + " steps/s", W * 0.72, H * 0.5);
  if (s.hist.length > 1) visSpark(g, W * 0.52, H * 0.6, W * 0.45, H * 0.3, s.hist, "#81c784");
}

// visDrawPD draws the source's offers as bars by voltage (the one asked for
// lit, the contract's ringed).
// TRLC-LINKS: REQ-SDS-278
function visDrawPD(g, W, H, s) {
  if (!s.caps.length) return visBig(g, W, H, "no Source_Capabilities yet", "plug the sink in: the source offers its power first");
  const n = s.caps.length, bw = W / (n + 1), maxV = Math.max(...s.caps.map((c) => c.mv)) || 1;
  s.caps.forEach((c, i) => {
    const asked = s.request && s.request.pos === i + 1, h = (H * 0.7) * c.mv / maxV, x = bw * (i + 0.5), y = H * 0.85 - h;
    g.fillStyle = asked ? (s.contract ? "#81c784" : "#ffb74d") : "#29435a"; g.fillRect(x, y, bw * 0.8, h);
    g.fillStyle = "#e6edf3"; g.textAlign = "center"; g.textBaseline = "bottom"; g.font = Math.round(Math.min(12 * dpr, bw / 8)) + "px monospace";
    g.fillText(c.text, x + bw * 0.4, y - 3 * dpr);
    g.textBaseline = "top"; g.fillStyle = "#8b98a5"; g.fillText(c.type + " " + (i + 1), x + bw * 0.4, H * 0.87);
  });
}

// visDrawCAN graphs up to four signals ticked in the table (else the first
// four), each in its own band.
// TRLC-LINKS: REQ-SDS-278
function visDrawCAN(g, W, H, s) {
  if (!s.dbc) return visDrawFrames(g, W, H, s, "load a DBC file to graph its messages' signals");
  const all = [...s.sigs.values()], pick = all.filter((e) => vis.canPick.has(e.msg + "." + e.name));
  const show = (pick.length ? pick : all).slice(0, 4);
  if (!show.length) return visBig(g, W, H, "no frame of the DBC file yet", "the ids seen list beside");
  const bh = H / show.length;
  show.forEach((e, i) => {
    visSpark(g, 8 * dpr, i * bh + 16 * dpr, W - 16 * dpr, bh - 22 * dpr, e.hist, VIS_CH_COLOURS[i]);
    g.fillStyle = "#e6edf3"; g.font = Math.round(11 * dpr) + "px monospace"; g.textAlign = "left"; g.textBaseline = "top";
    g.fillText(e.msg + "." + e.name + " = " + visNum(e.v) + " " + e.unit, 8 * dpr, i * bh + 2 * dpr);
  });
}

// visDrawLayer graphs the layer's values: the four rows with numbers updated last.
// TRLC-LINKS: REQ-SDS-278
function visDrawLayer(g, W, H) {
  const rows = [...vis.layer.notes.values()].filter((e) => e.hist.length > 1).sort((a, b) => b.t - a.t).slice(0, 4);
  if (!rows.length) return visBig(g, W, H, "no value yet", "the layer's notes list beside; those with numbers graph here");
  const bh = H / rows.length;
  rows.forEach((e, i) => {
    visSpark(g, 8 * dpr, i * bh + 16 * dpr, W - 16 * dpr, bh - 22 * dpr, e.hist, VIS_CH_COLOURS[i]);
    g.fillStyle = "#e6edf3"; g.font = Math.round(11 * dpr) + "px monospace"; g.textAlign = "left"; g.textBaseline = "top";
    g.fillText(e.note.slice(0, 80), 8 * dpr, i * bh + 2 * dpr);
  });
}

// ---- the side panel ---------------------------------------------------------------------

// visSide writes the side panel: the controls (rebuilt on a new protocol,
// rebuild) and the figures.
// TRLC-LINKS: REQ-SDS-278
function visSide(rebuild) {
  const side = $("visSide");
  if (!side || !vis.s) return;
  const p = vis.s.proto, a = vis.audio;
  if (rebuild || side.dataset.p !== (a.on ? "audio" : dcfg.layer ? "layer" : p)) {
    side.dataset.p = a.on ? "audio" : dcfg.layer ? "layer" : p;
    side.innerHTML = visControls(p);
    visWire(p);
  }
  const fig = $("visFig");
  if (!fig) return;
  const html = a.on ? visAudioFigures() : dcfg.layer ? visLayerTable() : visFigures(p);
  if (fig._html !== html) { fig._html = html; fig.innerHTML = html; }
  const term = $("visTerm");
  if (term && p === "uart" && term.textContent !== vis.s.text) { term.textContent = vis.s.text; term.scrollTop = term.scrollHeight; }
  $("visTitle").textContent = a.on ? "Digital audio" : dcfg.layer ? "Layer values" : VIS_PROTOS[p] || "Visual";
  const st = $("visStreamNote");
  if (st) st.textContent = vis.stream.on ? "gap-free: following the scope's stream" + (vis.stream.rate > 0 ? "" : " (timing settles in 2 s)") : VIS_STREAMED.includes(p) ? "from each capture (gaps between them)" : "";
}

// visControls are the side panel's controls for a protocol.
// TRLC-LINKS: REQ-SDS-278
function visControls(p) {
  const a = vis.audio;
  let h = '<div class="r"><span id="visStreamNote" class="sub grow"></span></div>';
  if (a.on) {
    return h + '<div class="r"><label class="hl"><span class="lbl">source</span><select id="visAKind" aria-label="audio source">' +
      [["auto", "decoded S/PDIF, else I2S"], ["spdif", "S/PDIF (the decoded line)"], ["i2s", "I2S: WS on C1, SD on C2"], ["i2s21", "I2S: WS on C2, SD on C1"], ["lj", "Left-justified: WS C1, SD C2"],
        ["pdm", "PDM: clock C1, data C2"], ["pdm21", "PDM: clock C2, data C1"]].map(([v, t]) => `<option value="${v}"${a.kind === v ? " selected" : ""}>${t}</option>`).join("") +
      '</select></label></div><div class="r"><button type="button" id="visACap" class="xs">Capture</button><button type="button" id="visALive" class="xs tog' + (a.live ? " on" : "") + '">Live</button>' +
      '<button type="button" id="visAPlay" class="xs tog' + (a.play ? " on" : "") + '">Play (loop)</button><label class="row-tight"><span class="lbl">vol</span><input type="range" id="visAGain" min="0" max="1" step="0.05" value="' + a.gain + '" aria-label="audio volume"></label>' +
      '<button type="button" id="visAOff" class="xs">Back to the bus</button></div><div id="visAMsg" class="sub"></div><div id="visFig"></div>';
  }
  if (p === "midi") h += '<div class="r"><button type="button" id="visSound" class="xs tog' + (vis.sound ? " on" : "") + '">Sound</button><label class="row-tight"><span class="lbl">vol</span><input type="range" id="visVol" min="0" max="1" step="0.05" value="' + (vis.sy ? vis.sy.gain : 0.5) + '" aria-label="MIDI volume"></label><button type="button" id="visPanic" class="xs">All notes off</button></div>' +
    '<div class="r"><label class="row-tight"><span class="lbl">notes</span><select id="visLead" aria-label="live: a note sounds as it comes and rises away; fall: it falls onto its key for the seconds chosen and sounds as it lands">' +
    [0, 1.5, 3, 5, 8].map((v) => `<option value="${v}"${v === vis.midiLead ? " selected" : ""}>${v ? "fall " + v + " s" : "live, rise away"}</option>`).join("") + "</select></label></div>";
  if (VIS_STREAMED.includes(p)) h += '<div class="r"><button type="button" id="visGapless" class="xs tog' + (vis.stream.on ? " on" : "") + '" title="The scope streams the bus in hardware: every byte, no gaps between captures">Gap-free (scope streams)</button></div>';
  if (p === "ws281x") h += '<div class="r"><label class="hl"><span class="lbl">order</span><select id="visOrder" aria-label="LED byte order">' + ["GRB", "RGB", "BRG", "GRBW", "RGBW"].map((o) => `<option${o === vis.wsOrder ? " selected" : ""}>${o}</option>`).join("") + "</select></label></div>";
  if (p === "ps2") h += '<div class="r"><label class="hl"><span class="lbl">device</span><select id="visPs2" aria-label="PS/2 device"><option value="">by its bytes</option><option value="kbd">keyboard</option><option value="mouse">mouse</option></select></label></div>';
  if (p === "can" || p === "canfd") h += '<div class="r"><label class="hl"><span class="lbl">DBC</span><input type="file" id="visDbc" accept=".dbc,text/plain" aria-label="DBC file"></label><span class="sub">' + escHTML(vis.dbcName) + "</span></div>";
  if (p === "quad" || p === "stepper") h += '<div class="r"><button type="button" id="visZero" class="xs">Zero</button></div>';
  if (p === "uart") h += '<div class="r"><button type="button" id="visClear" class="xs">Clear</button></div><pre id="visTerm" class="transcript visterm" aria-label="terminal"></pre>';
  h += '<div class="r"><button type="button" id="visAudio" class="xs" title="Capture a whole record and decode S/PDIF, I2S or PDM audio from it">Digital audio...</button></div>';
  return h + '<div id="visFig"></div>';
}

// visWire wires the side panel's controls.
// TRLC-LINKS: REQ-SDS-278
function visWire(p) {
  // TRLC-LINKS: REQ-SDS-278
  const on = (id, ev, fn) => { const el = $(id); if (el) el[ev] = fn; };
  on("visSound", "onclick", async () => {
    if (!vis.sy) vis.sy = synthNew();
    vis.sound = !vis.sound;
    if (vis.sound) vis.sound = await synthStart(vis.sy);
    else synthStop(vis.sy);
    if (vis.s && vis.s.out) vis.s.out.length = 0;
    $("visSound").classList.toggle("on", vis.sound);
    if (!vis.sound) toast("MIDI sound off");
  });
  on("visVol", "oninput", () => { if (!vis.sy) vis.sy = synthNew(); synthVolume(vis.sy, +$("visVol").value); });
  on("visLead", "onchange", () => { vis.midiLead = +$("visLead").value; if (vis.sy) { vis.sy.lead = vis.midiLead; vis.sy.offset = NaN; } visDraw(true); });
  on("visPanic", "onclick", () => { if (vis.sy) synthPanic(vis.sy); if (vis.s && vis.s.held) { vis.s.held.clear(); } });
  on("visGapless", "onclick", () => { visAskStream(!vis.stream.on); $("visGapless").classList.toggle("on", !vis.stream.on); });
  on("visOrder", "onchange", () => { vis.wsOrder = $("visOrder").value; visDraw(true); });
  on("visPs2", "onchange", () => { vis.ps2Kind = $("visPs2").value; vis.s.kind = vis.ps2Kind; visDraw(true); });
  if ($("visPs2")) $("visPs2").value = vis.ps2Kind;
  on("visDbc", "onchange", async () => {
    const f = $("visDbc").files[0];
    if (!f) return;
    vis.dbcText = await f.text(); vis.dbcName = f.name;
    vis.s.dbc = dbcParse(vis.dbcText); vis.s.sigs.clear();
    toast("DBC: " + vis.s.dbc.size + " messages from " + f.name);
    visSide(true);
  });
  on("visZero", "onclick", () => { vis.s.pos = 0; vis.s.hist = []; visDraw(true); });
  on("visClear", "onclick", () => { vis.s.text = ""; visSide(false); });
  on("visAudio", "onclick", () => { vis.audio.on = true; visSide(true); visDraw(true); });
  on("visAOff", "onclick", () => visAudioOff());
  on("visAKind", "onchange", () => { vis.audio.kind = $("visAKind").value; vis.audio.sampleS = 0; });
  on("visACap", "onclick", () => visAudioCapture());
  on("visALive", "onclick", () => { vis.audio.live = !vis.audio.live; $("visALive").classList.toggle("on", vis.audio.live); if (vis.audio.live) visAudioCapture(); });
  on("visAPlay", "onclick", () => visAudioPlay(!vis.audio.play));
  on("visAGain", "oninput", () => { vis.audio.gain = +$("visAGain").value; if (vis.audio.play) vis.audio.play.g.gain.value = vis.audio.gain; });
  const fig = $("visFig");
  // TRLC-LINKS: REQ-SDS-278
  if (fig) fig.onchange = (e) => { const k = e.target && e.target.dataset && e.target.dataset.sig; if (!k) return; if (e.target.checked) vis.canPick.add(k); else vis.canPick.delete(k); visDraw(true); };
}

// visFigures are a protocol's figures as an HTML table or list.
// TRLC-LINKS: REQ-SDS-278
function visFigures(p) {
  const s = vis.s;
  // TRLC-LINKS: REQ-SDS-278
  const tab = (rows) => '<table class="meas"><tbody>' + rows.map((r) => "<tr>" + r.map((c, i) => i ? `<td>${c}</td>` : `<th>${c}</th>`).join("") + "</tr>").join("") + "</tbody></table>";
  // TRLC-LINKS: REQ-SDS-278
  const ago = (t) => { const d = performance.now() / 1000 - t; return d < 1 ? "now" : d < 60 ? d.toFixed(0) + " s ago" : (d / 60).toFixed(0) + " min ago"; };
  switch (p) {
    case "midi": {
      const held = [...s.held.values()].map((n) => midiNoteName(n.note) + "<sub>" + (n.ch + 1) + "</sub>").join(" ");
      return tab([["held", held || "-"], ["messages", s.msgs + (s.errs ? " · " + s.errs + " errors" : "")], ["clock", s.clocks ? s.clocks + " ticks" : "-"],
        ["programs", [...s.prog].map((v, i) => s.roll.some((n) => n.ch === i) ? (i + 1) + ":" + (v + 1) : "").filter(Boolean).join(" ") || "-"]]) +
        '<div class="sub">newest messages</div><div class="vislog">' + s.log.slice(-14).reverse().map((l) => escHTML(l.text)).join("<br>") + "</div>";
    }
    case "dmx512": {
      const top = [...s.levels.keys()].filter((i) => s.levels[i] > 0).sort((a, b) => s.levels[b] - s.levels[a]).slice(0, 12);
      return tab([["start code", s.sc < 0 ? "-" : visHex2(s.sc) + (s.sc === 0 ? " (levels)" : s.sc === 0xCC ? " (RDM)" : "")], ["slots", s.slots], ["packets", s.frames + " · " + dmxRate(s).toFixed(1) + " /s"]]) +
        '<div class="sub">highest channels</div>' + tab(top.map((i) => ["ch " + (i + 1), s.levels[i] + " (" + Math.round(s.levels[i] / 2.55) + " %)"]));
    }
    case "ps2": return tab([["device", s.kind === "mouse" ? "mouse" : s.kind === "kbd" ? "keyboard" : "not known yet"], ["last key", escHTML(s.lastKey || "-")], ["held", [...s.keys].map((c) => escHTML((PS2_KEYS.get(c) || { label: c }).label)).join(" ") || "-"]]) +
      '<div class="sub">typed</div><pre class="transcript visterm">' + escHTML(s.typed.slice(-600)) + "</pre>";
    case "ws281x": return tab([["LEDs", Math.floor(s.leds.length / vis.wsOrder.length)], ["frames", s.frames], ["bytes", s.leds.length]]);
    case "irnec": case "irsirc": case "irrc5": case "irrc6": case "irmp":
      return tab([["address", "command", "pressed", "last"], ...[...s.buttons.values()].sort((a, b) => b.t - a.t).slice(0, 20).map((b) => ["A " + b.addr.toString(16).toUpperCase(), "C " + b.cmd.toString(16).toUpperCase(), b.count + "x", ago(b.t)])]);
    case "pwm": return tab([["duty", Number.isFinite(s.duty) ? s.duty.toFixed(2) + " %" : "-"], ["range", Number.isFinite(s.min) ? s.min.toFixed(1) + " - " + s.max.toFixed(1) + " %" : "-"],
      ["frequency", Number.isFinite(s.freq) ? eng(s.freq, "Hz", 5) : "-"], ["period", Number.isFinite(s.periodS) ? eng(s.periodS, "s", 4) : "-"], ["servo", Number.isFinite(pwmServo(s)) ? pwmServo(s).toFixed(1) + "°" : "not a servo's"]]);
    case "quad": case "stepper": return tab([["position", s.pos], ["direction", s.fwd ? "forward" : "reverse"], ["speed", Math.abs(s.speed).toFixed(1) + " steps/s"]]);
    case "caliper": return tab([["reading", escHTML(s.text || "-")], ["readings", s.hist.length]]);
    case "dcf77": return tab(Object.entries(s.fields).map(([k, f]) => [k, f.v + (f.bad ? " (parity)" : "")]));
    case "usbpd": return tab([["contract", s.contract || (s.request ? (s.request.rejected ? "rejected" : "asked: " + s.request.text) : "-")], ...s.caps.map((c, i) => [(i + 1) + " " + c.type, c.text])]) +
      '<div class="sub">messages</div><div class="vislog">' + s.msgs.slice(-12).reverse().map((m) => escHTML(m.sop + " " + m.name)).join("<br>") + "</div>";
    case "wiegand": return tab([["bits", "facility", "card", "read"], ...[...s.cards.values()].sort((a, b) => b.t - a.t).slice(0, 20).map((c) => [c.bits, c.fac, c.card, c.count + "x " + ago(c.t)])]);
    case "lin":
      return tab([["id", "bytes", "count"], ...[...s.frames.entries()].sort((a, b) => a[0] - b[0]).slice(0, 40).map(([id, f]) => [visHex2(id), f.bytes.map(visHex2).join(" "), f.count])]) +
        (s.errs ? '<div class="sub">' + s.errs + " frames failed their checksum or identifier parity</div>" : "");
    case "can": case "canfd":
      if (s.dbc && s.sigs.size) return tab([["", "signal", "value"], ...[...s.sigs.entries()].sort().map(([k, e]) => [`<input type="checkbox" data-sig="${escHTML(k)}"${vis.canPick.has(k) ? " checked" : ""} aria-label="graph ${escHTML(k)}">`, escHTML(k), visNum(e.v) + " " + escHTML(e.unit)])]);
      return tab([["id", "bytes", "count"], ...[...s.frames.entries()].sort((a, b) => a[0] - b[0]).slice(0, 40).map(([id, f]) => [id.toString(16).toUpperCase() + ((s.dbc && s.dbc.get(id)) ? " " + escHTML(s.dbc.get(id).name) : ""), f.bytes.map(visHex2).join(" "), f.count])]);
  }
  return "";
}

// visLayerTable lists the layer's rows: each note's newest and its count.
// TRLC-LINKS: REQ-SDS-278
function visLayerTable() {
  const rows = [...vis.layer.notes.values()].sort((a, b) => b.t - a.t).slice(0, 40);
  return '<table class="meas"><tbody>' + rows.map((e) => `<tr><td class="${e.err ? "bad" : ""}">${escHTML(e.note)}</td><td>${e.count}x</td></tr>`).join("") + "</tbody></table>";
}

// ---- digital audio -------------------------------------------------------------------

// VIS_RECORD_WORDS is about the words a channel's record holds; a capture
// picks the timebase whose 1.5 screens fit it at the sample interval wanted.
const VIS_RECORD_WORDS = 500000;

// visAudioPlan is the sample interval a source wants: S/PDIF four samples a
// half cell (48 kHz until the first capture measured the rate), I2S six a
// data bit (64 bits a frame), PDM six a clock period (3 MHz).
// TRLC-LINKS: REQ-SDS-280
function visAudioPlan(kind, res) {
  const fs = res && res.ok && res.fs > 0 ? res.fs : 48000;
  if (kind === "spdif") return Math.max(2e-9, 1 / (128 * fs) / 4);
  if (kind.startsWith("pdm")) return Math.max(2e-9, (res && res.ok ? 1 / (fs * PDM_DECIM) : 1 / 3.072e6) / 6);
  const slot = res && res.slot ? res.slot : 32;
  return Math.max(2e-9, 1 / (2 * slot * fs) / 6);
}

// visAudioKind is the source the capture decodes: the one chosen, or S/PDIF
// where the bus decoded is, else I2S (WS C1, SD C2).
// TRLC-LINKS: REQ-SDS-280
function visAudioKind() {
  const k = vis.audio.kind;
  if (k !== "auto") return k;
  return dcfg.proto === "spdif" ? "spdif" : "i2s";
}

// visAudioCapture takes one whole sampled record at the interval the source
// wants (the timebase stepped so its record fits the memory: the page says
// so and puts it back when the audio view closes), decodes it and shows it;
// Live takes the next one when this one is done.
// TRLC-LINKS: REQ-SDS-280
async function visAudioCapture() {
  const a = vis.audio;
  if (a.busy) return;
  a.busy = true;
  const kind = visAudioKind();
  try {
    const want = a.sampleS > 0 ? a.sampleS : visAudioPlan(kind, null);
    const tds = (st && st.tdivs) || [];
    let td = 0;
    for (const t of tds) if (15 * t / want <= VIS_RECORD_WORDS && t > td) td = t;
    if (td > 0 && st && Math.abs(st.tdiv_s - td) > 1e-3 * td) {
      if (!a.prevTdiv) a.prevTdiv = st.tdiv_s;
      await send("tdiv", td);
    }
    visAudioMsg("capturing " + eng(want, "s", 3) + " a sample" + (td ? " at " + eng(td, "s", 3) + "/div" : "") + "...");
    const rec = await visFetchRecord(want);
    if (!rec.ok) { visAudioMsg(rec.error); return; }
    const res = visAudioDecode(kind, rec);
    a.res = res;
    if (!res.ok) { visAudioMsg(res.error); return; }
    const better = visAudioPlan(kind, res);
    if (!(a.sampleS > 0) || Math.abs(better - a.sampleS) > 0.2 * a.sampleS) a.sampleS = better;
    res.stats = res.ch.map((x, i) => audioStats(x, res.raw && res.raw[i], res.bits));
    res.durS = res.ch[0].length / res.fs;
    visAudioMsg(res.kind + " · " + eng(res.fs, "Hz", 4) + " · " + eng(res.durS, "s", 3) + " of audio from a record of " + rec.n + " samples");
    if (a.play) visAudioPlay(true);
    visDraw(true);
  } finally {
    a.busy = false;
    if (a.live && a.on) setTimeout(visAudioCapture, 50);
  }
}

// visFetchRecord fetches a whole sampled record (/api/record.bin) as codes.
// TRLC-LINKS: REQ-SDS-280
async function visFetchRecord(sampleS) {
  try {
    const r = await fetch("/api/record.bin?sample_s=" + sampleS + "&wait_ms=6000");
    if (!r.ok) {
      const j = await r.json().catch(() => ({}));
      return { ok: false, error: j.err || "no record (" + r.status + ")" };
    }
    const b = new Uint8Array(await r.arrayBuffer());
    const hl = b[0] | (b[1] << 8) | (b[2] << 16) | (b[3] << 24);
    const h = JSON.parse(new TextDecoder().decode(b.subarray(4, 4 + hl)));
    const c1 = b.subarray(4 + hl, 4 + hl + h.n), c2 = b.subarray(4 + hl + h.n, 4 + hl + 2 * h.n);
    return { ok: true, n: h.n, sampleS: h.sample_s, c1: Array.from(c1), c2: Array.from(c2) };
  } catch (e) { return { ok: false, error: "the record did not come: " + e.message }; }
}

// visAudioDecode decodes a record as the source's audio.
// TRLC-LINKS: REQ-SDS-280
function visAudioDecode(kind, rec) {
  if (kind === "spdif") {
    const line = dcfg.proto === "spdif" && dcfg.line === 2 ? rec.c2 : rec.c1;
    return audioFromSPDIF(decodeSPDIF(line, rec.sampleS, {}));
  }
  if (kind === "pdm") return audioFromPDM(rec.c1, rec.c2, rec.sampleS);
  if (kind === "pdm21") return audioFromPDM(rec.c2, rec.c1, rec.sampleS);
  if (kind === "i2s21") return audioFromI2S(rec.c2, rec.c1, rec.sampleS, {});
  return audioFromI2S(rec.c1, rec.c2, rec.sampleS, { lj: kind === "lj" });
}

// visAudioMsg writes the audio's status line.
// TRLC-LINKS: REQ-SDS-280
function visAudioMsg(t) { vis.audio.msg = t; const el = $("visAMsg"); if (el) el.textContent = t; }

// visAudioPlay plays the newest capture looped (the page's own audio: a
// click started it), or stops.
// TRLC-LINKS: REQ-SDS-280
function visAudioPlay(on) {
  const a = vis.audio;
  if (a.play) { try { a.play.src.stop(); } catch (e) { /* stopped already */ } a.play = null; }
  if (on && a.res && a.res.ok) {
    if (!vis.sy) vis.sy = synthNew();
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!a.ctx) a.ctx = new AC();
    a.ctx.resume();
    const fs = Math.max(3000, Math.min(768000, a.res.fs)), n = a.res.ch[0].length;
    const buf = a.ctx.createBuffer(2, n, fs);
    buf.copyToChannel(a.res.ch[0], 0); buf.copyToChannel(a.res.ch[1], 1);
    const src = a.ctx.createBufferSource(), g = a.ctx.createGain();
    src.buffer = buf; src.loop = true; g.gain.value = a.gain;
    src.connect(g); g.connect(a.ctx.destination); src.start();
    a.play = { src, g };
  }
  const b = $("visAPlay");
  if (b) b.classList.toggle("on", !!a.play);
}

// visAudioOff closes the audio view: playing and Live stop, the timebase goes back.
// TRLC-LINKS: REQ-SDS-280
function visAudioOff() {
  const a = vis.audio;
  a.on = false; a.live = false;
  visAudioPlay(false);
  if (a.prevTdiv) { send("tdiv", a.prevTdiv); a.prevTdiv = 0; }
  visSide(true); visDraw(true);
}

// visAudioFigures are the capture's figures a channel.
// TRLC-LINKS: REQ-SDS-280
function visAudioFigures() {
  const r = vis.audio.res;
  if (!r) return '<div class="sub">Capture takes a whole record of the audio bus (the scope steps its timebase so the record fits) and decodes it; Play loops it.</div>';
  if (!r.ok) return "";
  // TRLC-LINKS: REQ-SDS-280
  const db = (v) => Number.isFinite(v) ? v.toFixed(1) + " dBFS" : "silent";
  const rows = [["", "left", "right"], ["rate", eng(r.fs, "Hz", 5), ""], ["level (RMS)", db(r.stats[0].rmsDb), db(r.stats[1].rmsDb)], ["peak", db(r.stats[0].peakDb), db(r.stats[1].peakDb)],
    ["clipped", r.stats[0].clip, r.stats[1].clip], ["DC", (r.stats[0].dc * 100).toFixed(2) + " %", (r.stats[1].dc * 100).toFixed(2) + " %"]];
  if (r.bits) rows.push(["bits used", r.stats[0].bitsUsed + " of " + r.bits, r.stats[1].bitsUsed + " of " + r.bits]);
  if (r.slot) rows.push(["slot", r.slot + " bits a channel", ""]);
  if (r.errs) rows.push(["parity errors", r.errs, ""]);
  return '<table class="meas"><tbody>' + rows.map((x, i) => "<tr>" + x.map((c) => i ? `<td>${c}</td>` : `<th>${c}</th>`).join("") + "</tr>").join("") + "</tbody></table>";
}

// visDrawAudio draws the capture's two channels' waveforms (min and max a
// column), the left above the right, full scale the band's edges.
// TRLC-LINKS: REQ-SDS-280
function visDrawAudio(g, W, H) {
  const r = vis.audio.res;
  if (!r || !r.ok) return visBig(g, W, H, vis.audio.busy ? "capturing..." : "no audio yet", vis.audio.msg || "Capture takes a record of the bus");
  const bh = H / 2;
  r.ch.forEach((x, c) => {
    const y0 = c * bh, mid = y0 + bh / 2, n = x.length, cols = Math.max(1, Math.floor(W / dpr));
    g.fillStyle = "#0b1016"; g.fillRect(0, y0 + dpr, W, bh - 2 * dpr);
    g.fillStyle = "#263039"; g.fillRect(0, mid, W, dpr);
    g.fillStyle = c ? "#ffb74d" : "#4fc3f7";
    for (let k = 0; k < cols; k++) {
      const a = Math.floor(k * n / cols), b = Math.max(a + 1, Math.floor((k + 1) * n / cols));
      let lo = 1, hi = -1;
      for (let i = a; i < b && i < n; i++) { if (x[i] < lo) lo = x[i]; if (x[i] > hi) hi = x[i]; }
      if (hi < lo) continue;
      g.fillRect(k * dpr, mid - hi * bh * 0.48, dpr, Math.max(dpr, (hi - lo) * bh * 0.48));
    }
    g.fillStyle = "#8b98a5"; g.font = Math.round(10 * dpr) + "px monospace"; g.textAlign = "left"; g.textBaseline = "top";
    g.fillText(c ? "right" : "left", 4 * dpr, y0 + 3 * dpr);
  });
}

// ---- wiring --------------------------------------------------------------------------------

// The view redraws while shown (the piano roll scrolls with no new frame).
setInterval(() => { if (visShown()) { if (vis.s && vis.s.held) visSound(); if (!vis.anim) visDraw(); } }, 100);

// visAnimate redraws the falling notes each display frame while MIDI is shown
// (they move every frame; the other pictures change only with the data).
// TRLC-LINKS: REQ-SDS-278
function visAnimate() {
  vis.anim = visShown() && vis.s && vis.s.proto === "midi" && !vis.audio.on && !dcfg.layer;
  if (vis.anim) visDraw(true);
  requestAnimationFrame(visAnimate);

// The Full screen button puts the picture and its panel on the whole screen.
// TRLC-LINKS: REQ-SDS-278
if ($("visFull")) $("visFull").onclick = () => {
  const el = $("visGrid");
  if (document.fullscreenElement) document.exitFullscreen();
  else if (el && el.requestFullscreen) el.requestFullscreen().catch(() => {});
};
}
requestAnimationFrame(visAnimate);
