// ENGMODEL-OWNER-UNIT: FU-WEB-APP-VISUAL
// app_visual_draw.js — each bus's picture, figures and controls in BUS's
// Visual view (VIS_VIEWS, one entry for each bus of VIS_KINDS in visual.js;
// app_visual.js runs them): a MIDI piano, DMX levels, a PS/2 keyboard or
// mouse, an LED strip, an IR remote's buttons, a PWM gauge or a servo, an
// encoder's or stepper's dial, a caliper's reading, the DCF77 time, a
// terminal, LIN and CAN frames (CAN signals from a DBC file), the USB PD
// contract, Wiegand cards, and a protocol layer's values. Drawing is on the
// card's WebGL 2D context. Classic script; shares app.js globals.
"use strict";

// VIS_CH_COLOURS colour MIDI's sixteen channels.
const VIS_CH_COLOURS = ["#4fc3f7", "#ffb74d", "#81c784", "#e57373", "#ba68c8", "#fff176", "#4db6ac", "#f06292",
  "#9575cd", "#a1887f", "#aed581", "#64b5f6", "#ff8a65", "#90a4ae", "#dce775", "#7986cb"];

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

// visPianoGeom is the piano's layout in a W x H picture: the 88 keys' white
// ones, a key's x and width (keyX), the keyboard's top, the felt line notes
// land on (hitY) and the space above it (from legH, rollH high).
// TRLC-LINKS: REQ-SDS-278
function visPianoGeom(W, H) {
  const lo = 21, hi = 108, whites = [];
  for (let n = lo; n <= hi; n++) if (![1, 3, 6, 8, 10].includes(n % 12)) whites.push(n);
  const ww = W / whites.length;
  // a white key is about five and a half times as long as it is wide, the
  // keyboard never more than a third of the picture
  const kbH = Math.max(36 * dpr, Math.min(H / 3, ww * 5.5)), felt = 3 * dpr, legH = 20 * dpr;
  const keyTop = H - kbH, hitY = keyTop - felt;
  // TRLC-LINKS: REQ-SDS-278
  const keyX = (n) => {
    const bl = [1, 3, 6, 8, 10].includes(n % 12);
    const wi = whites.indexOf(bl ? n - 1 : n);
    return bl ? { x: (wi + 1) * ww - ww * 0.31, w: ww * 0.62, black: true } : { x: wi * ww, w: ww, black: false };
  };
  return { lo, hi, whites, ww, kbH, felt, legH, keyTop, hitY, rollH: hitY - legH, keyX };
}

// visDrawPianoBack draws the space above the keys and its octave lanes.
// TRLC-LINKS: REQ-SDS-278
function visDrawPianoBack(g, W, P) {
  g.fillStyle = "#070a0f"; g.fillRect(0, 0, W, P.hitY);
  for (let k = 0; k < P.whites.length; k++) {
    const m = P.whites[k] % 12;
    if (m === 0 || m === 5) { g.fillStyle = m === 0 ? "#18222e" : "#10171f"; g.fillRect(Math.round(k * P.ww), P.legH, Math.max(1, dpr), P.rollH); }
  }
}

// visDrawPianoKeys draws the felt and the keys over the picture, the lit ones
// (note -> {ch}) pressed in their channel's colour with a glow above them.
// TRLC-LINKS: REQ-SDS-278
function visDrawPianoKeys(g, W, P, lit) {
  const { ww, kbH, felt, keyTop, hitY, keyX } = P;
  for (const [note, n] of lit) {
    const k = keyX(note);
    g.fillStyle = VIS_CH_COLOURS[n.ch];
    g.globalAlpha = 0.18; g.fillRect(k.x - ww * 0.2, hitY - 22 * dpr, k.w + ww * 0.4, 22 * dpr);
    g.globalAlpha = 0.35; g.fillRect(k.x, hitY - 9 * dpr, k.w, 9 * dpr);
  }
  g.globalAlpha = 1;
  g.fillStyle = "#7a1c22"; g.fillRect(0, hitY, W, felt); // the felt under the fallboard
  // the white keys: a face, a gap between, a lip at the front; pressed they take the note's colour
  g.fillStyle = "#2a2a2a"; g.fillRect(0, keyTop, W, kbH);
  const gap = Math.max(1, Math.round(0.6 * dpr));
  for (const n of P.whites) {
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
  for (let n = P.lo; n <= P.hi; n++) {
    const k = keyX(n);
    if (!k.black) continue;
    const h = lit.get(n);
    g.fillStyle = "#0c0c0f"; g.fillRect(k.x, keyTop, k.w, bh);
    g.fillStyle = h ? VIS_CH_COLOURS[h.ch] : "#2b2b33"; g.fillRect(k.x + k.w * 0.12, keyTop, k.w * 0.76, bh - 5 * dpr);
    if (!h) { g.fillStyle = "#44444e"; g.fillRect(k.x + k.w * 0.12, keyTop + bh - 9 * dpr, k.w * 0.76, 3 * dpr); }
  }
}

// visDrawLegend writes the channels in chans, each its colour and instrument.
// TRLC-LINKS: REQ-SDS-278
function visDrawLegend(g, P, s, chans) {
  g.font = Math.round(11 * dpr) + "px monospace"; g.textBaseline = "middle"; g.textAlign = "left";
  let lx = 8 * dpr;
  for (const ch of [...chans].sort((a, b) => a - b)) {
    const label = (ch + 1) + " " + midiInstrument(ch, s.prog[ch]);
    g.fillStyle = VIS_CH_COLOURS[ch]; g.fillRect(lx, P.legH / 2 - 5 * dpr, 10 * dpr, 10 * dpr);
    g.fillStyle = "#c9d1d9"; g.fillText(label, lx + 14 * dpr, P.legH / 2);
    lx += 14 * dpr + (label.length + 2) * 6.6 * dpr;
  }
}

// visDrawMIDI draws the MIDI bus on an 88-key piano, each channel its colour,
// with a legend of the channels and their instruments. Following the stream
// (gap-free: every note's whole length seen) the notes are bars as long as
// they are held, live rising from their keys and fading (with a lead,
// falling onto them); from captures, whose gaps hide notes' ends, the
// newest capture is drawn on its own (visDrawMIDICapture).
// TRLC-LINKS: REQ-SDS-278
function visDrawMIDI(g, W, H, s) {
  if (s.cap) return visDrawMIDICapture(g, W, H, s);
  const P = visPianoGeom(W, H), { lo, hi, ww, legH, hitY, rollH, keyX } = P;
  const now = performance.now() / 1000, lead = vis.midiLead;
  const rise = !(lead > 0), span = rise ? VIS_MIDI_RISE : lead;
  // a time's height: live, now at the keys and the past above; falling, now
  // at the top and lead seconds ago at the keys
  // TRLC-LINKS: REQ-SDS-278
  const yOf = (t) => rise ? hitY - ((now - t) / span) * rollH : hitY - ((t - (now - lead)) / lead) * rollH;
  visDrawPianoBack(g, W, P);
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
  visDrawPianoKeys(g, W, P, lit);
  visDrawLegend(g, P, s, chans);
  if (!s.roll.length) {
    g.fillStyle = "#8b98a5"; g.textAlign = "center";
    g.fillText(rise ? "a note presses its key as it plays, then rises away: Sound plays it" : "notes fall onto the keys: Sound plays them as they land", W / 2, legH + rollH / 2);
  }
}

// visDrawMIDICapture draws the newest capture alone, redrawn with each one:
// its pushes (a filled mark pointing at the key) and releases (a hollow one)
// above their keys, each as high as it is long before the capture's end (its
// time scale at the left), the keys pushed and not released in it pressed.
// A capture's gaps hide what was held across them, so no note is drawn on.
// TRLC-LINKS: REQ-SDS-278
function visDrawMIDICapture(g, W, H, s) {
  const P = visPianoGeom(W, H), { lo, hi, ww, legH, hitY, rollH, keyX } = P;
  const span = Math.max(1e-6, s.capT1 - s.capT0), top = legH + 10 * dpr, h = hitY - 6 * dpr - top;
  // TRLC-LINKS: REQ-SDS-278
  const yOf = (t) => hitY - 6 * dpr - ((s.capT1 - t) / span) * h;
  visDrawPianoBack(g, W, P);
  // the time scale: the capture's end at the keys, its start at the top
  g.font = Math.round(10 * dpr) + "px monospace"; g.textAlign = "left"; g.textBaseline = "middle";
  for (let k = 0; k <= 4; k++) {
    const age = span * k / 4, y = hitY - 6 * dpr - (k / 4) * h;
    g.fillStyle = "#1c2631"; g.fillRect(0, Math.round(y), W, Math.max(1, dpr));
    g.fillStyle = "#6b7884"; g.fillText(k ? eng(age, "s", 2) + " before" : "the capture's end", 4 * dpr, y - 6 * dpr);
  }
  const lit = new Map(), chans = new Set(), cap = s.cap;
  for (const e of cap) {
    chans.add(e.ch);
    if (e.on) lit.set(e.note, e); else lit.delete(e.note);
  }
  const sz = Math.max(4 * dpr, Math.min(ww * 0.45, 8 * dpr)), label = cap.length <= 48 && ww >= 6 * dpr;
  for (const e of cap) {
    if (e.note < lo || e.note > hi) continue;
    const k = keyX(e.note), cx = k.x + k.w / 2, y = yOf(e.t), c = VIS_CH_COLOURS[e.ch];
    g.globalAlpha = 0.25; g.fillStyle = c; g.fillRect(cx - Math.max(1, dpr) / 2, y, Math.max(1, dpr), hitY - y); // its lane down to the key
    g.globalAlpha = 1;
    g.beginPath();
    if (e.on) { // a push: filled, pointing down at the key
      g.moveTo(cx - sz, y - sz); g.lineTo(cx + sz, y - sz); g.lineTo(cx, y + sz * 0.6); g.closePath();
      g.fillStyle = c; g.fill();
    } else { // a release: hollow, pointing up from it
      g.moveTo(cx - sz, y + sz); g.lineTo(cx + sz, y + sz); g.lineTo(cx, y - sz * 0.6); g.closePath();
      g.strokeStyle = c; g.lineWidth = 1.5 * dpr; g.stroke();
    }
    if (label) {
      g.fillStyle = "#c9d1d9"; g.font = Math.round(9 * dpr) + "px monospace"; g.textAlign = "left"; g.textBaseline = "middle";
      g.fillText(midiNoteName(e.note), cx + sz + 2 * dpr, y);
    }
  }
  visDrawPianoKeys(g, W, P, lit);
  visDrawLegend(g, P, s, chans);
  g.fillStyle = "#8b98a5"; g.font = Math.round(11 * dpr) + "px monospace"; g.textAlign = "center"; g.textBaseline = "middle";
  if (!cap.length) g.fillText("this capture holds no push or release", W / 2, legH + rollH / 2);
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
  g.fillText("counted in the captures (not between them)", W * 0.72, H * 0.5 + 16 * dpr);
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

// visLayerTable lists the layer's rows: each note's newest and its count.
// TRLC-LINKS: REQ-SDS-278
function visLayerTable() {
  const rows = [...vis.layer.notes.values()].sort((a, b) => b.t - a.t).slice(0, 40);
  return '<table class="meas"><tbody>' + rows.map((e) => `<tr><td class="${e.err ? "bad" : ""}">${escHTML(e.note)}</td><td>${e.count}x</td></tr>`).join("") + "</tbody></table>";
}


// ---- the figures' table and times -----------------------------------------------------------

// visTab writes rows as the figures' table, each row's first cell its heading.
// TRLC-LINKS: REQ-SDS-278
function visTab(rows) { return '<table class="meas"><tbody>' + rows.map((r) => "<tr>" + r.map((c, i) => i ? `<td>${c}</td>` : `<th>${c}</th>`).join("") + "</tr>").join("") + "</tbody></table>"; }

// visAgo writes how long ago a time on the page's clock was.
// TRLC-LINKS: REQ-SDS-278
function visAgo(t) { const d = performance.now() / 1000 - t; return d < 1 ? "now" : d < 60 ? d.toFixed(0) + " s ago" : (d / 60).toFixed(0) + " min ago"; }

// visLog writes a list of the newest lines first.
// TRLC-LINKS: REQ-SDS-278
function visLog(title, lines) { return '<div class="sub">' + title + '</div><div class="vislog">' + lines.map(escHTML).join("<br>") + "</div>"; }

// ---- each bus's view ------------------------------------------------------------------------

// VIS_VIEWS are the views of the buses of VIS_KINDS, by its key: draw(g, W,
// H, s) the picture, figures(s) the side panel's figures (HTML), and
// optionally controls(s) the side panel's own controls (HTML) and wire(s, on)
// wiring them (on(id, event, handler)). The core (app_visual.js) adds what
// every bus has: the stream's note, Gap-free for a bus read from the stream,
// Digital audio.
const VIS_VIEWS = {
  midi: {
    draw: visDrawMIDI,
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => {
      const held = [...s.held.values()].map((n) => midiNoteName(n.note) + "<sub>" + (n.ch + 1) + "</sub>").join(" ");
      // from captures: the newest one's pushes and releases (what was held across their gaps is not known)
      const cap = s.cap ? [["this capture", s.cap.filter((e) => e.on).length + " pushed (filled) · " + s.cap.filter((e) => !e.on).length + " released (hollow)"]] : [];
      return visTab([...(s.cap ? cap : [["held", held || "-"]]), ["messages", s.msgs + (s.errs ? " · " + s.errs + " errors" : "")], ["clock", s.clocks ? s.clocks + " ticks" : "-"],
        ["programs", [...s.prog].map((v, i) => s.roll.some((n) => n.ch === i) ? (i + 1) + ":" + (v + 1) : "").filter(Boolean).join(" ") || "-"]]) +
        visLog("newest messages", s.log.slice(-14).reverse().map((l) => l.text));
    },
    // TRLC-LINKS: REQ-SDS-279
    controls: () => '<div class="r"><button type="button" id="visSound" class="xs tog' + (vis.sound ? " on" : "") + '">Sound</button><label class="row-tight"><span class="lbl">vol</span><input type="range" id="visVol" min="0" max="1" step="0.05" value="' + (vis.sy ? vis.sy.gain : 0.5) + '" aria-label="MIDI volume"></label><button type="button" id="visPanic" class="xs">All notes off</button></div>' +
      '<div class="r"><label class="row-tight"><span class="lbl">notes</span><select id="visLead" aria-label="live: a note sounds as it comes and rises away; fall: it falls onto its key for the seconds chosen and sounds as it lands">' +
      [0, 1.5, 3, 5, 8].map((v) => `<option value="${v}"${v === vis.midiLead ? " selected" : ""}>${v ? "fall " + v + " s" : "live, rise away"}</option>`).join("") + "</select></label></div>",
    // TRLC-LINKS: REQ-SDS-279
    wire: (s, on) => {
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
      on("visPanic", "onclick", () => { if (vis.sy) synthPanic(vis.sy); if (vis.s && vis.s.held) vis.s.held.clear(); });
    },
  },
  dmx512: {
    draw: visDrawDMX,
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => {
      const top = [...s.levels.keys()].filter((i) => s.levels[i] > 0).sort((a, b) => s.levels[b] - s.levels[a]).slice(0, 12);
      return visTab([["start code", s.sc < 0 ? "-" : visHex2(s.sc) + (s.sc === 0 ? " (levels)" : s.sc === 0xCC ? " (RDM)" : "")], ["slots", s.slots], ["packets", s.frames + " · " + dmxRate(s).toFixed(1) + " /s"]]) +
        '<div class="sub">highest channels</div>' + visTab(top.map((i) => ["ch " + (i + 1), s.levels[i] + " (" + Math.round(s.levels[i] / 2.55) + " %)"]));
    },
  },
  ps2: {
    // TRLC-LINKS: REQ-SDS-278
    draw: (g, W, H, s) => s.kind === "mouse" ? visDrawMouse(g, W, H, s) : visDrawKeyboard(g, W, H, s),
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab([["device", s.kind === "mouse" ? "mouse" : s.kind === "kbd" ? "keyboard" : "not known yet"], ["last key", escHTML(s.lastKey || "-")],
      ["held", [...s.keys].map((c) => escHTML((PS2_KEYS.get(c) || { label: c }).label)).join(" ") || "-"]]) +
      '<div class="sub">typed</div><pre class="transcript visterm">' + escHTML(s.typed.slice(-600)) + "</pre>",
    // TRLC-LINKS: REQ-SDS-278
    controls: () => '<div class="r"><label class="hl"><span class="lbl">device</span><select id="visPs2" aria-label="PS/2 device"><option value="">by its bytes</option><option value="kbd">keyboard</option><option value="mouse">mouse</option></select></label></div>',
    // TRLC-LINKS: REQ-SDS-278
    wire: (s, on) => {
      // the device chosen, or (by its bytes) the one its bytes vote for from here
      // TRLC-LINKS: REQ-SDS-278
      on("visPs2", "onchange", () => { vis.ps2Kind = $("visPs2").value; vis.s.kind = vis.ps2Kind; vis.s.forced = !!vis.ps2Kind; visDraw(true); });
      if ($("visPs2")) $("visPs2").value = vis.ps2Kind;
    },
  },
  ws281x: {
    draw: visDrawLEDs,
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab([["LEDs", Math.floor(s.leds.length / vis.wsOrder.length)], ["frames", s.frames], ["bytes", s.leds.length]]),
    // TRLC-LINKS: REQ-SDS-278
    controls: () => '<div class="r"><label class="hl"><span class="lbl">order</span><select id="visOrder" aria-label="LED byte order">' + ["GRB", "RGB", "BRG", "GRBW", "RGBW"].map((o) => `<option${o === vis.wsOrder ? " selected" : ""}>${o}</option>`).join("") + "</select></label></div>",
    // TRLC-LINKS: REQ-SDS-278
    wire: (s, on) => on("visOrder", "onchange", () => { vis.wsOrder = $("visOrder").value; visDraw(true); }),
  },
  irnec: {
    draw: visDrawIR,
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab([["address", "command", "pressed", "last"], ...[...s.buttons.values()].sort((a, b) => b.t - a.t).slice(0, 20).map((b) => ["A " + visHexN(b.addr), "C " + visHexN(b.cmd), b.count + "x", visAgo(b.t)])]),
  },
  pwm: {
    draw: visDrawPWM,
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab([["duty", Number.isFinite(s.duty) ? s.duty.toFixed(2) + " %" : "-"], ["range", Number.isFinite(s.min) ? s.min.toFixed(1) + " - " + s.max.toFixed(1) + " %" : "-"],
      ["frequency", Number.isFinite(s.freq) ? eng(s.freq, "Hz", 5) : "-"], ["period", Number.isFinite(s.periodS) ? eng(s.periodS, "s", 4) : "-"], ["servo", Number.isFinite(pwmServo(s)) ? pwmServo(s).toFixed(1) + "°" : "not a servo's"]]),
  },
  quad: { draw: visDrawDial, figures: visDialFigures, controls: visZeroControl, wire: visZeroWire },
  stepper: { draw: visDrawDial, figures: visDialFigures, controls: visZeroControl, wire: visZeroWire },
  caliper: {
    // TRLC-LINKS: REQ-SDS-278
    draw: (g, W, H, s) => visBig(g, W, H, s.text || "--", s.hist.length ? "min " + Math.min(...s.hist.map((p) => p.v)) + "  max " + Math.max(...s.hist.map((p) => p.v)) + " " + s.unit : "", s.hist),
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab([["reading", escHTML(s.text || "-")], ["readings", s.hist.length]]),
  },
  dcf77: {
    // TRLC-LINKS: REQ-SDS-278
    draw: (g, W, H, s) => visBig(g, W, H, s.time || "waiting for a minute", s.bad ? "a parity failed: the time is not sure" : s.time ? "DCF77 time and date" : visDcfHint(s)),
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab(Object.entries(s.fields).map(([k, f]) => [k, f.v + (f.bad ? " (parity)" : "")])),
  },
  uart: {
    // TRLC-LINKS: REQ-SDS-278
    draw: (g, W, H) => visBig(g, W, H, "", "the terminal is beside"),
    // TRLC-LINKS: REQ-SDS-278
    figures: () => "",
    // TRLC-LINKS: REQ-SDS-278
    controls: () => '<div class="r"><button type="button" id="visClear" class="xs">Clear</button></div><pre id="visTerm" class="transcript visterm" aria-label="terminal"></pre>',
    // TRLC-LINKS: REQ-SDS-278
    wire: (s, on) => on("visClear", "onclick", () => { vis.s.text = ""; visSide(false); }),
    // TRLC-LINKS: REQ-SDS-278
    update: (s) => { const term = $("visTerm"); if (term && term.textContent !== s.text) { term.textContent = s.text; term.scrollTop = term.scrollHeight; } },
  },
  lin: {
    // TRLC-LINKS: REQ-SDS-278
    draw: (g, W, H, s) => visDrawFrames(g, W, H, s, "the identifiers' newest responses; checksum failures count beside"),
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab([["id", "bytes", "count"], ...[...s.frames.entries()].sort((a, b) => a[0] - b[0]).slice(0, 40).map(([id, f]) => [visHex2(id), f.bytes.map(visHex2).join(" "), f.count])]) +
      (s.errs ? '<div class="sub">' + s.errs + " frames failed their checksum or identifier parity</div>" : ""),
  },
  usbpd: {
    draw: visDrawPD,
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab([["contract", s.contract || (s.request ? (s.request.rejected ? "rejected" : "asked: " + escHTML(s.request.text)) : "-")], ...s.caps.map((c, i) => [(i + 1) + " " + c.type, c.text])]) +
      visLog("messages", s.msgs.slice(-12).reverse().map((m) => m.sop + " " + m.name)),
  },
  wiegand: {
    // TRLC-LINKS: REQ-SDS-278
    draw: (g, W, H, s) => visBig(g, W, H, s.lastCard ? "card " + s.lastCard.card : "no card yet",
      s.lastCard ? s.lastCard.bits + "-bit · facility " + s.lastCard.fac + " · read " + s.lastCard.count + "x" : "present a card to the reader"),
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => visTab([["bits", "facility", "card", "read"], ...[...s.cards.values()].sort((a, b) => b.t - a.t).slice(0, 20).map((c) => [c.bits, c.fac, c.card, c.count + "x " + visAgo(c.t)])]),
  },
  can: {
    draw: visDrawCAN,
    // TRLC-LINKS: REQ-SDS-278
    figures: (s) => {
      if (s.dbc && s.sigs.size) return visTab([["", "signal", "value"], ...[...s.sigs.entries()].sort().map(([k, e]) => [`<input type="checkbox" data-sig="${escHTML(k)}"${vis.canPick.has(k) ? " checked" : ""} aria-label="graph ${escHTML(k)}">`, escHTML(k), visNum(e.v) + " " + escHTML(e.unit)])]);
      return visTab([["id", "bytes", "count"], ...[...s.frames.entries()].sort((a, b) => a[0] - b[0]).slice(0, 40).map(([id, f]) => [visHexN(id) + ((s.dbc && s.dbc.get(id)) ? " " + escHTML(s.dbc.get(id).name) : ""), f.bytes.map(visHex2).join(" "), f.count])]);
    },
    // TRLC-LINKS: REQ-SDS-278
    controls: () => '<div class="r"><label class="hl"><span class="lbl">DBC</span><input type="file" id="visDbc" accept=".dbc,text/plain" aria-label="DBC file"></label><span class="sub">' + escHTML(vis.dbcName) + "</span></div>",
    // TRLC-LINKS: REQ-SDS-278
    wire: (s, on) => {
      on("visDbc", "onchange", async () => {
        const f = $("visDbc").files[0];
        if (!f) return;
        vis.dbcText = await f.text(); vis.dbcName = f.name;
        vis.s.dbc = dbcParse(vis.dbcText); vis.s.sigs.clear();
        toast("DBC: " + vis.s.dbc.size + " messages from " + f.name);
        visSide(true);
      });
      const fig = $("visFig");
      // TRLC-LINKS: REQ-SDS-278
      if (fig) fig.onchange = (e) => { const k = e.target && e.target.dataset && e.target.dataset.sig; if (!k) return; if (e.target.checked) vis.canPick.add(k); else vis.canPick.delete(k); visDraw(true); };
    },
  },
  spdif: {
    // TRLC-LINKS: REQ-SDS-280
    draw: (g, W, H) => visBig(g, W, H, "S/PDIF", "Digital audio captures a whole record, decodes it, measures it and plays it"),
    // TRLC-LINKS: REQ-SDS-278
    figures: () => "",
  },
};

// visHexN writes a number in upper-case hex (a negative one, unknown, as "-").
// TRLC-LINKS: REQ-SDS-278
function visHexN(v) { return v >= 0 ? v.toString(16).toUpperCase() : "-"; }

// visDrawIR writes the remote's newest button: its address and command, held
// and repeating, the times pressed.
// TRLC-LINKS: REQ-SDS-278
function visDrawIR(g, W, H, s) {
  const b = s.lastBtn;
  if (!b) return visBig(g, W, H, "no button yet", "press a button on the remote");
  visBig(g, W, H, "A " + visHexN(b.addr) + "  C " + visHexN(b.cmd), b.name + (b.repeats ? " · held, " + b.repeats + " repeats" : "") + " · pressed " + b.count + "x");
}

// visDialFigures are an encoder's or a stepper's figures.
// TRLC-LINKS: REQ-SDS-278
function visDialFigures(s) {
  return visTab([["counted", s.pos], ["last capture", (s.lastCount > 0 ? "+" : "") + s.lastCount], ["direction", s.fwd ? "forward" : "reverse"], ["speed", Math.abs(s.speed).toFixed(1) + " steps/s"]]) +
    '<div class="sub">The count adds up what each capture saw; steps between captures are not seen.</div>';
}

// visZeroControl is Zero, setting an encoder's or a stepper's count to 0.
// TRLC-LINKS: REQ-SDS-278
function visZeroControl() { return '<div class="r"><button type="button" id="visZero" class="xs">Zero</button></div>'; }

// visZeroWire wires Zero.
// TRLC-LINKS: REQ-SDS-278
function visZeroWire(s, on) { on("visZero", "onclick", () => { vis.s.pos = 0; vis.s.hist = []; visDraw(true); }); }

// visView is the view of a protocol's bus (null for one VIS_KINDS lacks).
// TRLC-LINKS: REQ-SDS-278
function visView(proto) { const k = VIS_KINDS[proto]; return k ? VIS_VIEWS[k.key] || null : null; }

