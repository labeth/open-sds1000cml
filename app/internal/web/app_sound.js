// ENGMODEL-OWNER-UNIT: FU-WEB-APP-SOUND
// app_sound.js — the SOUND workspace (ADR-SOUND): the gap-free audio stream
// (/api/sound.bin) in a ring here, Listen through an AudioWorklet (jitter
// buffer, rate following, limiter) with source, gain, filters, notch and A−B,
// the meters, the tuner (note, cents, strobe, ten-second strip), the views
// (pitch-locked cycle from the FPGA's edge marks, envelope, modulation,
// spectrum, A/B) and the scope's shared readouts (envelope capture, V/oct,
// drift). The analysis is sound.js, the twin of the scope's Go. While SOUND is
// shown the stream owns the fabric: the scope's triggered captures pause.
// Classic script; shares app.js globals.
"use strict";

const SND_RING = 1 << 20;
const SND_MASK = SND_RING - 1;
const SND_PREF = "sds.sound.";

const snd = {
  shown: false,
  gen: 0,
  rate: sndRate(SND_LOG2),
  c1: new Float32Array(SND_RING),
  c2: new Float32Array(SND_RING),
  next: 0, // ordinal after the newest sample held
  validFrom: 0, // the oldest ordinal that is one signal with the newest
  cursor: null, // the next ordinal to fetch
  hdr: null,
  marks: [],
  markV: 0,
  clipAt: [0, 0],
  lastVpc: [0.04, 0.04],
  tuner: sndTuner(false),
  hz: 0,
  clarity: 0,
  strip: [],
  strobe: 0,
  lastT: 0,
  levels: [null, null],
  settings: { a4: 440, ch: 0, ref: "dbu", range: "", slow: false, cycles: 2, ab_in: 0, env_cv: false, gate: -1, gate_v: 0.1, voct_auto: false, drift: false },
  report: null,
  reportAt: 0,
  view: "cycle",
  read: "adsr",
  cycles: [],
  cycleAt: -1,
  cycle: null,
  cycleWhy: "",
  spec: [null, null],
  specAt: 0,
  mod: null,
  modDemod: null,
  sb: null,
  modAt: 0,
  ab: null,
  abAt: 0,
  err: "",
  blocks: 0,
  audio: {
    on: false, ctx: null, node: null, src: "c1", mute: false, vol: 0.7, auto: true, gainDB: 0, hp: true, lp: false, notch: false,
    agc: 1, peak: 0, stat: {}, filt: null, delay: [], abMatch: 1, abDelay: 0, quiet: 0,
    probe: { recv: [], bursts: [] },
  },
};

// ---- the stream -------------------------------------------------------------
// TRLC-LINKS: REQ-SDS-265
function sndParse(buf) {
  const u8 = new Uint8Array(buf);
  if (u8.length < 8 || u8[0] !== 83 || u8[1] !== 66) return null;
  const hl = new DataView(buf).getUint32(4, true);
  const hdr = JSON.parse(new TextDecoder().decode(u8.subarray(8, 8 + hl)));
  const off = 8 + hl, n = hdr.n | 0;
  const q1 = new Uint16Array(buf.slice(off, off + 2 * n));
  const q2 = new Uint16Array(buf.slice(off + 2 * n, off + 4 * n));
  return { hdr, q1, q2 };
}

// sndVoltsOf converts one channel's codes over the block's scales (volts at
// the probe tip).
// TRLC-LINKS: REQ-SDS-265
function sndVoltsOf(h, q, ch) {
  const out = new Float32Array(q.length), sc = h.scales || [];
  let k = -1, vpc = 1, off = 0;
  for (let i = 0; i < q.length; i++) {
    const ord = h.first + i;
    while (k + 1 < sc.length && (k < 0 || sc[k + 1].from <= ord)) { k++; vpc = sc[k].vpc[ch]; off = sc[k].off[ch]; }
    out[i] = (q[i] - 32768) * vpc / 256 - off;
  }
  if (sc.length) snd.lastVpc[ch] = sc[sc.length - 1].vpc[ch];
  return out;
}

// sndIngest puts a reply's samples into the ring, its marks into the list,
// and the listen source into the player.
// TRLC-LINKS: REQ-SDS-265
function sndIngest(buf) {
  const p = sndParse(buf);
  if (!p) return;
  const h = p.hdr, n = h.n | 0;
  snd.hdr = h;
  snd.markV = h.mark_v;
  if (!n) { if (snd.cursor === null || snd.cursor > h.next) snd.cursor = h.next; return; }
  let gap = snd.cursor === null || h.first !== snd.cursor || (h.gaps && h.gaps.length > 0);
  const v1 = sndVoltsOf(h, p.q1, 0), v2 = sndVoltsOf(h, p.q2, 1);
  for (let i = 0; i < n; i++) { const k = (h.first + i) & SND_MASK; snd.c1[k] = v1[i]; snd.c2[k] = v2[i]; }
  if (gap) snd.validFrom = h.gaps && h.gaps.length ? h.gaps[h.gaps.length - 1] : h.first;
  snd.next = h.first + n;
  snd.cursor = snd.next;
  snd.blocks++;
  if (snd.next - snd.validFrom > SND_RING) snd.validFrom = snd.next - SND_RING;
  for (const m of h.marks || []) if (!snd.marks.length || m.at > snd.marks[snd.marks.length - 1].at) snd.marks.push(m);
  while (snd.marks.length && snd.marks[0].at < snd.validFrom) snd.marks.shift();
  if (snd.marks.length > 2048) snd.marks.splice(0, snd.marks.length - 2048);
  for (const [ch, q] of [[0, p.q1], [1, p.q2]]) if (sndClipCodes(q)) snd.clipAt[ch] = performance.now();
  if (snd.audio.on) sndAudioFeed(v1, v2, gap);
}

// sndPump long-polls the stream while SOUND is shown.
// TRLC-LINKS: REQ-SDS-265
async function sndPump(gen) {
  while (snd.shown && gen === snd.gen) {
    let url = "/api/sound.bin?max=16384&wait=600";
    if (snd.cursor !== null) url += "&from=" + snd.cursor;
    if (snd.view === "cycle") url += "&marks=1";
    try {
      const r = await fetch(url, { cache: "no-store" });
      if (!r.ok) throw new Error("HTTP " + r.status);
      sndIngest(await r.arrayBuffer());
      snd.err = "";
    } catch (e) {
      snd.err = String(e.message || e);
      await new Promise((res) => setTimeout(res, 600));
    }
  }
}

// sndLatest is the newest n samples of channel ch that are one signal.
// TRLC-LINKS: REQ-SDS-265
function sndLatest(ch, n) {
  const have = Math.max(0, snd.next - snd.validFrom);
  n = Math.max(0, Math.min(n, have, SND_RING));
  const out = new Float64Array(n), src = ch === 1 ? snd.c2 : snd.c1, from = snd.next - n;
  for (let i = 0; i < n; i++) out[i] = src[(from + i) & SND_MASK];
  return out;
}

// sndRange is samples [from, from+n) of channel ch (clipped to what is held).
// TRLC-LINKS: REQ-SDS-272
function sndRangeOf(ch, from, n) {
  const a = Math.max(from, snd.validFrom), b = Math.min(from + n, snd.next);
  const out = new Float64Array(Math.max(0, b - a)), src = ch === 1 ? snd.c2 : snd.c1;
  for (let i = 0; i < out.length; i++) out[i] = src[(a + i) & SND_MASK];
  return { from: a, x: out };
}

// ---- settings and the scope's readouts -----------------------------------------
// TRLC-LINKS: REQ-SDS-274
async function sndPost(body) {
  try {
    const r = await fetch("/api/sound", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    const j = await r.json();
    if (j.report) sndTakeReport(j.report);
    if (!j.ok && j.err) toast(j.err, "warn");
  } catch (e) { toast("SOUND: " + e.message, "err"); }
}

// TRLC-LINKS: REQ-SDS-274
async function sndPoll() {
  try {
    const r = await fetch("/api/sound" + (snd.view === "cycle" ? "?marks=1" : ""), { cache: "no-store" });
    const j = await r.json();
    if (j.report) sndTakeReport(j.report);
  } catch (e) { /* the stream's own errors show in the status line */ }
}

// sndTakeReport adopts the scope's settings and readouts and sets the controls.
// TRLC-LINKS: REQ-SDS-274
function sndTakeReport(rep) {
  snd.report = rep;
  snd.reportAt = performance.now();
  const s = rep.settings || {};
  snd.settings = Object.assign(snd.settings, s);
  const set = snd.settings;
  // A control being typed into keeps what is typed.
  if (document.activeElement !== $("sndA4")) $("sndA4").value = set.a4;
  if (document.activeElement !== $("sndGateV")) $("sndGateV").value = set.gate_v;
  $("sndRange").value = set.range || "";
  $("sndRef").value = set.ref;
  $("sndGate").value = String(set.gate);
  $("sndCh").value = String(set.ch);
  $("sndSlow").value = set.slow ? "1" : "0";
  $("sndCycles").value = String(set.cycles);
  $("sndEnvCV").value = set.env_cv ? "1" : "0";
  $("sndVAuto").value = set.voct_auto ? "1" : "0";
  $("sndDriftOn").value = set.drift ? "1" : "0";
}

// sndSet changes some settings on the scope (shared with the LCD).
// TRLC-LINKS: REQ-SDS-274
function sndSet(part) {
  Object.assign(snd.settings, part);
  if ("ch" in part || "range" in part || "slow" in part) { snd.tuner = sndTuner(snd.settings.slow); snd.strip = []; snd.cycles = []; }
  if ("cycles" in part) snd.cycles = [];
  sndPost({ settings: part });
}

// ---- Listen -----------------------------------------------------------------------
// sndBq is a streaming biquad (RBJ coefficients, direct form I).
// TRLC-LINKS: REQ-SDS-265
function sndBq(b) { return { b, x1: 0, x2: 0, y1: 0, y2: 0 }; }

// TRLC-LINKS: REQ-SDS-265
function sndBqRun(f, x) {
  const b = f.b;
  for (let i = 0; i < x.length; i++) {
    const v = x[i], y = b[0] * v + b[1] * f.x1 + b[2] * f.x2 - b[3] * f.y1 - b[4] * f.y2;
    f.x2 = f.x1; f.x1 = v; f.y2 = f.y1; f.y1 = y;
    x[i] = y;
  }
}

// sndNotchB is a notch at f0 (Q 8): the fundamental out, its harmonics in.
// TRLC-LINKS: REQ-SDS-265
function sndNotchB(f0, fs) {
  const w = 2 * Math.PI * f0 / fs, cw = Math.cos(w), al = Math.sin(w) / 16, a0 = 1 + al;
  return [1 / a0, -2 * cw / a0, 1 / a0, -2 * cw / a0, (1 - al) / a0];
}

// sndFilters builds the listen chain's filters for both outputs.
// TRLC-LINKS: REQ-SDS-265
function sndFilters() {
  const fs = snd.rate, a = snd.audio;
  // TRLC-LINKS: REQ-SDS-265
  const mk = () => ({ hp: sndBq(sndRbj("hp", 20, Math.SQRT1_2, fs)), lp: sndBq(sndRbj("lp", 5000, Math.SQRT1_2, fs)),
    notch: sndBq(sndNotchB(snd.hz > 0 ? snd.hz : 1000, fs)), notchHz: snd.hz });
  a.filt = [mk(), mk()];
}

// sndAudioFeed turns a block into the player's two outputs: the source, the
// high-pass, low-pass and notch, the gain (auto: the measured peak to a
// quarter of full scale), volume and mute.
// TRLC-LINKS: REQ-SDS-265
function sndAudioFeed(v1, v2, gap) {
  const a = snd.audio, n = v1.length;
  if (!a.node) return;
  if (!a.filt) sndFilters();
  let L = new Float32Array(n), R = new Float32Array(n);
  if (a.src === "c2") { L.set(v2); R.set(v2); }
  else if (a.src === "st") { L.set(v1); R.set(v2); }
  else if (a.src === "ab") {
    // The output less the input, matched in gain and delay (A/B's figures).
    const d = Math.max(0, Math.min(4096, a.abDelay | 0)), line = a.delay;
    for (let i = 0; i < n; i++) {
      line.push(v1[i]);
      const x = line.length > d ? line[line.length - 1 - d] : 0;
      L[i] = v2[i] - a.abMatch * x;
    }
    if (line.length > 8192) line.splice(0, line.length - 8192);
    R.set(L);
  } else { L.set(v1); R.set(v1); }
  if (a.notch && snd.hz > 0 && Math.abs(sndCents(snd.hz, a.filt[0].notchHz || 1)) > 10) sndFilters();
  for (let k = 0; k < 2; k++) {
    const x = k ? R : L, f = a.filt[k];
    if (a.hp) sndBqRun(f.hp, x);
    if (a.lp) sndBqRun(f.lp, x);
    if (a.notch && snd.hz > 0) sndBqRun(f.notch, x);
  }
  // the latency probe: a burst after a fifth of a second of quiet, received
  let pk = 0;
  for (let i = 0; i < n; i++) pk = Math.max(pk, Math.abs(L[i]), Math.abs(R[i]));
  const thr = 0.05;
  if (pk > thr && a.quiet > 0.2 * snd.rate) a.probe.recv.push(performance.now());
  a.quiet = pk > thr ? 0 : a.quiet + n;
  if (a.probe.recv.length > 50) a.probe.recv.shift();
  // gain
  let g;
  if (a.auto) {
    a.peak = Math.max(pk, a.peak * Math.exp(-n / (2 * snd.rate))); // 2 s release
    if (a.peak > 0.005) a.agc = 0.25 / a.peak; // quiet holds the gain: noise is not raised to full scale
    g = Math.max(0.01, Math.min(1000, a.agc));
  } else g = Math.pow(10, a.gainDB / 20);
  g *= a.mute ? 0 : a.vol;
  for (let i = 0; i < n; i++) { L[i] *= g; R[i] *= g; }
  a.node.port.postMessage({ l: L, r: R, gap }, [L.buffer, R.buffer]);
}

// sndWorkletMsg takes the player's reports and its bursts (latency probe).
// TRLC-LINKS: REQ-SDS-265
function sndWorkletMsg(e) {
  const m = e.data, a = snd.audio;
  if (m.burst !== undefined && a.ctx) {
    const ts = a.ctx.getOutputTimestamp ? a.ctx.getOutputTimestamp() : null;
    let perf = performance.now();
    if (ts && ts.performanceTime) perf = ts.performanceTime + (m.burst - ts.contextTime) * 1000;
    a.probe.bursts.push(perf + 1000 * ((a.ctx.outputLatency || 0) + (a.ctx.baseLatency || 0)));
    if (a.probe.bursts.length > 50) a.probe.bursts.shift();
    return;
  }
  a.stat = m;
}

// sndListen starts or stops playing the stream (a user's click starts audio).
// TRLC-LINKS: REQ-SDS-265
async function sndListen(on) {
  const a = snd.audio;
  if (on) {
    try {
      if (!a.ctx) {
        a.ctx = new AudioContext({ latencyHint: "interactive" });
        await a.ctx.audioWorklet.addModule("sound_worklet.js");
        a.node = new AudioWorkletNode(a.ctx, "sound-player", { numberOfInputs: 0, outputChannelCount: [2],
          processorOptions: { srcRate: snd.rate, targetMs: 120 } });
        a.node.port.onmessage = sndWorkletMsg;
        a.node.connect(a.ctx.destination);
      }
      await a.ctx.resume();
      a.node.port.postMessage({ reset: true });
      a.filt = null;
      a.on = true;
    } catch (e) {
      toast("Listen: " + e.message, "err");
      a.on = false;
    }
  } else {
    a.on = false;
    if (a.ctx) a.ctx.suspend();
  }
  $("sndListen").classList.toggle("on", a.on);
  $("sndListen").setAttribute("aria-pressed", String(a.on));
  $("sndListen").textContent = a.on ? "Listening ■" : "Listen ▶";
  wsDots();
}

// sndLatencyText is the latency the listener hears now, part by part.
// TRLC-LINKS: REQ-SDS-265
function sndLatencyText() {
  const a = snd.audio;
  if (!a.on || !a.ctx) return "";
  const buf = a.stat.fillMs || 0, out = 1000 * ((a.ctx.outputLatency || 0) + (a.ctx.baseLatency || 0));
  // The scope seals a stream bank about every 17 ms and the reply follows
  // it at once; that and the network are about 25 ms on a LAN.
  const dev = 25;
  return `latency ≈ ${Math.round(dev + buf + out)} ms (stream ~${dev} + buffer ${buf.toFixed(0)} + output ${out.toFixed(0)})`;
}

// ---- analysis tick ------------------------------------------------------------------
// TRLC-LINKS: REQ-SDS-266, REQ-SDS-267
function sndTick() {
  if (!snd.shown) return;
  const now = performance.now(), set = snd.settings, fs = snd.rate;
  if (now - snd.reportAt > 500) { snd.reportAt = now; sndPoll(); }
  if (snd.next > 0) {
    for (let ch = 0; ch < 2; ch++) {
      const x = sndLatest(ch, Math.round(0.2 * fs));
      snd.levels[ch] = x.length ? sndMeasure(x) : null;
    }
    const n = sndTunerWindow(snd.hz, fs), x = sndLatest(set.ch, n);
    const p = x.length >= n / 2 ? sndPitch(x, fs, sndPitchRange(set.range)) : { ok: false, hz: 0, clarity: 0 };
    snd.tuner.slow = !!set.slow;
    snd.hz = sndTunerAdd(snd.tuner, p);
    snd.clarity = p.clarity;
    const t = now / 1000;
    let cents = 0;
    if (snd.hz > 0) {
      cents = sndNote(snd.hz, set.a4).cents;
      const dt = t - snd.lastT;
      if (dt > 0 && dt < 1) snd.strobe = ((snd.strobe + cents / 2 * dt) % 1 + 1) % 1;
    }
    snd.lastT = t;
    snd.strip.push({ t, cents, ok: snd.hz > 0 });
    while (snd.strip.length && t - snd.strip[0].t > 10) snd.strip.shift();
    sndViewUpdate(now);
  }
  sndRender();
}

// sndViewUpdate runs the shown view's analysis at its rate.
// TRLC-LINKS: REQ-SDS-269, REQ-SDS-270, REQ-SDS-271, REQ-SDS-272
function sndViewUpdate(now) {
  const set = snd.settings, fs = snd.rate;
  switch (snd.view) {
    case "cycle": sndCycleUpdate(); break;
    case "mod":
      if (now - snd.modAt < 1000) break;
      snd.modAt = now;
      {
        const x = sndLatest(set.ch, Math.round(4 * fs));
        if (snd.hz > 0) {
          snd.modDemod = sndDemod(x, fs, snd.hz, 1000);
          snd.mod = sndModulation(snd.modDemod);
          const sp = sndSpectrum(x, fs, 32768);
          snd.sb = sndSidebands(sp.dbv, sp.binHz);
          snd.lfo = null;
        } else { snd.mod = null; snd.modDemod = null; snd.lfo = sndLFO(x, fs); }
      }
      break;
    case "spec":
      if (now - snd.specAt < 300) break;
      snd.specAt = now;
      for (let ch = 0; ch < 2; ch++) {
        const sp = sndSpectrum(sndLatest(ch, 16384), fs, 16384);
        const prev = snd.spec[ch];
        if (prev && prev.n === sp.n) for (let i = 0; i < sp.dbv.length; i++) sp.dbv[i] = 10 * Math.log10(0.7 * Math.pow(10, prev.dbv[i] / 10) + 0.3 * Math.pow(10, sp.dbv[i] / 10));
        snd.spec[ch] = sp;
      }
      snd.harm = snd.spec[set.ch] ? sndHarmonics(snd.spec[set.ch], snd.hz) : null;
      break;
    case "ab":
      if (now - snd.abAt < 500) break;
      snd.abAt = now;
      {
        const a = sndLatest(set.ab_in, 16384), b = sndLatest(1 - set.ab_in, 16384);
        snd.ab = sndCompareAB(a, b, fs, snd.hz);
        snd.abA = a; snd.abB = b;
        snd.specA = sndSpectrum(a, fs, 16384); snd.specB = sndSpectrum(b, fs, 16384);
        if (snd.ab.ok) { snd.audio.abMatch = snd.ab.match > 0 ? 1 / snd.ab.match : 1; snd.audio.abDelay = snd.ab.delayOK ? Math.max(0, Math.round(snd.ab.delayS * fs)) : 0; }
      }
      break;
  }
  if (snd.audio.src === "ab" && snd.view !== "ab" && now - snd.abAt > 1000) {
    snd.abAt = now;
    const r = sndCompareAB(sndLatest(set.ab_in, 16384), sndLatest(1 - set.ab_in, 16384), fs, snd.hz);
    if (r.ok) { snd.audio.abMatch = r.match > 0 ? 1 / r.match : 1; snd.audio.abDelay = r.delayOK ? Math.max(0, Math.round(r.delayS * fs)) : 0; }
  }
}

// sndCycleUpdate draws the newest marked cycle: the FPGA's rising mark on the
// tuned channel placed on its crossing of the marks' level, then the cycles
// after it. No mark, no trace: the trigger is the FPGA's.
// TRLC-LINKS: REQ-SDS-272
function sndCycleUpdate() {
  const set = snd.settings, fs = snd.rate, h = snd.hdr;
  if (!h || !h.marks_on) { snd.cycleWhy = "this FPGA image marks no edges in the stream"; return; }
  if (!(snd.hz > 0)) { snd.cycleWhy = "no pitch to lock to"; return; }
  const per = fs / snd.hz, span = per * (set.cycles || 2);
  let mark = null;
  for (let i = snd.marks.length - 1; i >= 0; i--) {
    const m = snd.marks[i];
    if (m.rising && m.ch === set.ch && m.at + span + 8 < snd.next && m.at - 8 >= snd.validFrom) { mark = m; break; }
  }
  if (!mark) { snd.cycleWhy = h.mark_ch !== set.ch ? "the marks are on the other channel" : "no edge marked yet"; return; }
  if (mark.at === snd.cycleAt) return;
  snd.cycleAt = mark.at;
  const r = sndRangeOf(set.ch, mark.at - 8, Math.ceil(span) + 24);
  const [at, ok] = sndPlaceMark(r.x, mark.at - r.from, snd.markV, true);
  if (!ok) { snd.cycleWhy = "the marked edge has no crossing beside it"; return; }
  const c = sndCycles(r.x, fs, at, per, set.cycles || 2, 600);
  if (!c.ok) return;
  snd.cycleWhy = "";
  snd.cycle = c;
  snd.cycles.push(c.trace);
  if (snd.cycles.length > 8) snd.cycles.shift();
}

// ---- drawing ------------------------------------------------------------------------
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-266, REQ-SDS-267
function sndRender() {
  sndRenderTuner();
  sndRenderMeters();
  sndRenderView();
  sndRenderCtx();
  sndRenderReadouts();
  const a = snd.audio, h = snd.hdr;
  let st = snd.err ? "stream: " + snd.err : !h ? "starting the audio stream…" : !h.active ? "audio stream: " + h.state + (h.err ? " — " + h.err : "") :
    `gap-free ${(h.rate / 1000).toFixed(1)} kSa/s · ${h.losses || 0} losses`;
  if (a.on) st += ` · buffer ${(a.stat.fillMs || 0).toFixed(0)} ms · rate ${(((a.stat.ratio || 1) - 1) * 1e6).toFixed(0)} ppm · ${a.stat.under || 0} underruns · ${a.stat.over || 0} overruns · ${sndLatencyText()}`;
  $("sndStatus").textContent = st;
  $("sndGainV").textContent = a.auto ? (20 * Math.log10(a.agc)).toFixed(0) + " dB" : a.gainDB + " dB";
}

// TRLC-LINKS: REQ-SDS-267
function sndRenderTuner() {
  const set = snd.settings, el = $("sndNote");
  if (snd.hz > 0) {
    const n = sndNote(snd.hz, set.a4), c = Math.abs(n.cents);
    el.textContent = n.text;
    el.className = c < 2 ? "in" : c < 10 ? "near" : "off";
    $("sndCents").textContent = (n.cents >= 0 ? "+" : "") + n.cents.toFixed(1) + " c";
    $("sndHz").textContent = snd.hz.toFixed(snd.hz >= 1000 ? 1 : 2) + " Hz";
  } else {
    el.textContent = "--"; el.className = "";
    $("sndCents").textContent = "— c"; $("sndHz").textContent = "no pitch";
  }
  $("sndTunerSub").textContent = `A4 = ${set.a4} Hz · C${set.ch + 1}` + (snd.hz > 0 ? ` · clarity ${snd.clarity.toFixed(2)}` : "");
  const cv = $("sndTunerCv");
  if (!fitCanvas(cv)) return;
  const g = glCardCtx(cv, "#05080c");
  if (!g) return;
  const W = cv.width, H = cv.height, d = window.devicePixelRatio || 1;
  const col = set.ch === 1 ? C2COL : C1COL;
  // needle
  const ny = 6 * d, nh = 30 * d;
  // TRLC-LINKS: REQ-SDS-267
  const cx = (c) => W * (0.5 + Math.max(-50, Math.min(50, c)) / 100);
  g.fillStyle = "rgba(63,185,80,0.25)"; g.fillRect(cx(-2), ny, cx(2) - cx(-2), nh);
  g.strokeStyle = "#50506e"; g.lineWidth = d;
  g.beginPath(); g.moveTo(0, ny + nh / 2); g.lineTo(W, ny + nh / 2); g.stroke();
  for (const c of [-50, -25, -10, 0, 10, 25, 50]) { const h = c === 0 ? nh : nh / 3; g.beginPath(); g.moveTo(cx(c), ny + nh / 2 - h / 2); g.lineTo(cx(c), ny + nh / 2 + h / 2); g.stroke(); }
  if (snd.hz > 0) {
    const c = sndNote(snd.hz, set.a4).cents;
    g.fillStyle = Math.abs(c) < 2 ? "#3fb950" : Math.abs(c) < 10 ? "#f2a63b" : "#d6dde4";
    g.fillRect(cx(c) - 1.5 * d, ny - 2 * d, 3 * d, nh + 4 * d);
  }
  // strobe
  const sy = ny + nh + 6 * d, sh = 16 * d, per = 24 * d;
  if (snd.hz > 0) {
    g.fillStyle = col;
    const off = snd.strobe * per;
    for (let x = -per + off; x < W; x += per) g.fillRect(Math.max(0, x), sy, Math.min(per / 2, W - x), sh);
  }
  // the ten-second strip
  const py = sy + sh + 6 * d, ph = H - py - 2 * d;
  g.strokeStyle = "#182430"; g.beginPath(); g.moveTo(0, py + ph / 2); g.lineTo(W, py + ph / 2); g.stroke();
  g.fillStyle = DIMCOL; g.font = (10 * d) + "px monospace"; g.fillText("pitch, 10 s ±50 c", 4 * d, py + 10 * d);
  const t1 = snd.lastT;
  g.strokeStyle = col; g.lineWidth = 1.5 * d; g.beginPath();
  let pen = false;
  for (const s of snd.strip) {
    if (!s.ok) { pen = false; continue; }
    const x = W * (1 - (t1 - s.t) / 10), y = py + ph / 2 - Math.max(-50, Math.min(50, s.cents)) / 50 * (ph / 2 - 2 * d);
    if (pen) g.lineTo(x, y); else { g.moveTo(x, y); pen = true; }
  }
  g.stroke();
  glCardEnd(cv);
}

// TRLC-LINKS: REQ-SDS-266
function sndRenderMeters() {
  const ref = sndPreset(snd.settings.ref), unit = ref.id === "dbu" ? "dBu" : ref.id === "dbv" ? "dBV" : "dB";
  for (let ch = 0; ch < 2; ch++) {
    const m = snd.levels[ch], el = $("sndMeter" + (ch + 1));
    const bar = el.querySelector(".snd-bar > i"), tick = el.querySelector(".snd-bar > b");
    el.querySelector(".snd-clip").hidden = performance.now() - snd.clipAt[ch] > 1500;
    if (!m) { bar.style.width = "0"; el.querySelector(".snd-mv").textContent = ""; continue; }
    const db = sndDB(m.rms, ref.vrms), pk = sndDB(m.peak / Math.SQRT2, ref.vrms);
    // TRLC-LINKS: REQ-SDS-266
    const frac = (v) => Math.max(0, Math.min(1, (v + 60) / 66));
    bar.style.width = (100 * frac(db)).toFixed(1) + "%";
    bar.classList.toggle("hot", db > 0);
    tick.style.left = (100 * frac(pk)).toFixed(1) + "%";
    const dbt = Number.isFinite(db) ? db.toFixed(1) + " " + unit : "— " + unit;
    el.querySelector(".snd-mv").innerHTML = `<span class="big">${dbt}</span>\nrms ${eng(m.rms, "V")}  pk ${eng(m.peak, "V")}\nVpp ${eng(m.vpp, "V")}  DC ${eng(m.dc, "V")}\ncrest ${m.crestDB.toFixed(1)} dB`;
  }
}

// sndAxisLog places f on the spectrum's log axis.
// TRLC-LINKS: REQ-SDS-270
function sndAxisLog(f, W) { return W * sndLogX(f, 10, snd.rate / 2); }

// TRLC-LINKS: REQ-SDS-272, REQ-SDS-268, REQ-SDS-269, REQ-SDS-270, REQ-SDS-271
function sndRenderView() {
  const cv = $("sndViewCv");
  if (!fitCanvas(cv)) return;
  const g = glCardCtx(cv, "#05080c");
  if (!g) return;
  const W = cv.width, H = cv.height, d = window.devicePixelRatio || 1, set = snd.settings;
  const col = set.ch === 1 ? C2COL : C1COL;
  g.font = (11 * d) + "px monospace";
  // TRLC-LINKS: REQ-SDS-272
  const msg = (t) => { g.fillStyle = DIMCOL; g.textAlign = "center"; g.fillText(t, W / 2, H / 2); g.textAlign = "left"; };
  // TRLC-LINKS: REQ-SDS-272
  const grid = () => {
    g.strokeStyle = "#182430"; g.lineWidth = 1;
    for (let i = 1; i < 10; i++) { g.beginPath(); g.moveTo(i * W / 10, 0); g.lineTo(i * W / 10, H); g.stroke(); }
    for (let i = 1; i < 8; i++) { g.beginPath(); g.moveTo(0, i * H / 8); g.lineTo(W, i * H / 8); g.stroke(); }
  };
  // TRLC-LINKS: REQ-SDS-272
  const line = (arr, x0, x1, y, c, w) => {
    g.strokeStyle = c; g.lineWidth = (w || 1.3) * d; g.beginPath();
    for (let i = 0; i < arr.length; i++) { const x = x0 + (x1 - x0) * i / Math.max(1, arr.length - 1), yy = y(arr[i]); if (i) g.lineTo(x, yy); else g.moveTo(x, yy); }
    g.stroke();
  };
  switch (snd.view) {
    case "cycle": {
      grid();
      if (!snd.cycles.length || snd.cycleWhy) { msg("Cycle: " + (snd.cycleWhy || "waiting")); break; }
      let lo = Infinity, hi = -Infinity;
      for (const t of snd.cycles) for (let i = 0; i < t.length; i++) { lo = Math.min(lo, t[i]); hi = Math.max(hi, t[i]); }
      const pad = 0.1 * (hi - lo) + 1e-6;
      lo -= pad; hi += pad;
      // TRLC-LINKS: REQ-SDS-272
      const y = (v) => H - (v - lo) / (hi - lo) * H;
      snd.cycles.forEach((t, k) => { g.globalAlpha = k === snd.cycles.length - 1 ? 1 : 0.15 + 0.4 * k / snd.cycles.length; line(t, 0, W, y, col); });
      g.globalAlpha = 1;
      g.fillStyle = DIMCOL;
      g.fillText(eng(hi, "V"), W - 70 * d, 14 * d); g.fillText(eng(lo, "V"), W - 70 * d, H - 6 * d);
      $("sndViewSub").textContent = `${set.cycles} ${set.cycles === 1 ? "cycle" : "cycles"} · trigger: FPGA edge mark C${set.ch + 1}`;
      break;
    }
    case "env": {
      const rep = snd.report, env = rep && rep.adsr_env;
      $("sndViewSub").textContent = rep ? "capture: " + rep.adsr : "";
      if (!env || !env.length) { msg(rep && rep.adsr === "armed" ? "Armed: waiting for the FPGA's edge" : rep && rep.adsr === "capturing" ? "Capturing…" : "Envelope: Arm in Readouts › ADSR"); break; }
      grid();
      const r = rep.adsr_res || {}, fs = rep.adsr_env_fs || 1000;
      let hi = 0;
      for (const v of env) hi = Math.max(hi, v || 0);
      hi = hi * 1.1 || 1;
      // TRLC-LINKS: REQ-SDS-268
      const y = (v) => H - (v || 0) / hi * H;
      line(env, 0, W, y, col, 1.6);
      const T = env.length / fs;
      for (const [t, lab] of [[r.Start, "start"], [r.PeakAt, "peak"], [r.DecayEnd, "decay"], [r.ReleaseAt, "release"], [r.End, "end"]]) {
        if (typeof t !== "number") continue;
        const x = W * t / T;
        g.strokeStyle = "#f2a63b"; g.lineWidth = d; g.setLineDash([4 * d, 4 * d]); g.beginPath(); g.moveTo(x, 0); g.lineTo(x, H); g.stroke(); g.setLineDash([]);
        g.fillStyle = "#f2a63b"; g.fillText(lab, x + 3 * d, 14 * d);
      }
      if (typeof r.Sustain === "number" && r.HasSustain) { g.strokeStyle = DIMCOL; g.beginPath(); g.moveTo(0, y(r.Sustain)); g.lineTo(W, y(r.Sustain)); g.stroke(); }
      break;
    }
    case "mod": {
      const dm = snd.modDemod;
      $("sndViewSub").textContent = snd.hz > 0 ? "amplitude (top) and pitch in cents (bottom), 4 s" : "no pitch: the LFO read directly";
      if (!dm || !dm.amp.length) {
        if (snd.lfo && snd.lfo.ok) { grid(); const x = sndLatest(set.ch, Math.round(8 * snd.rate)); const red = sndBlockMeans(x, snd.rate, 0.005).env; let lo = Infinity, hi = -Infinity; for (const v of red) { lo = Math.min(lo, v); hi = Math.max(hi, v); } line(red, 0, W, (v) => H - (v - lo) / (hi - lo || 1) * H, col); break; }
        msg("Modulation: waiting for a tone"); break;
      }
      grid();
      let amax = 0;
      for (const v of dm.amp) amax = Math.max(amax, v);
      line(dm.amp, 0, W, (v) => H / 2 - v / (amax * 1.1 || 1) * (H / 2 - 4 * d), col);
      const fc = (snd.mod && snd.mod.fc) || dm.fc, cs = dm.freq.map((f) => sndCents(f, fc));
      let span = 5;
      for (const c of cs) if (Number.isFinite(c)) span = Math.max(span, Math.abs(c));
      line(cs, 0, W, (c) => H * 0.75 - (Number.isFinite(c) ? c : 0) / (span * 1.1) * (H / 4 - 4 * d), "#35c8e8");
      g.fillStyle = DIMCOL; g.fillText("±" + span.toFixed(0) + " c", 4 * d, H / 2 + 14 * d);
      break;
    }
    case "spec": {
      const sp = snd.spec[set.ch];
      if (!sp || !sp.dbv.length) { msg("Spectrum: waiting"); break; }
      $("sndViewSub").textContent = `log axis 10 Hz – ${eng(snd.rate / 2, "Hz")} · dBV · RBW ${eng(sp.binHz, "Hz")} · grey: the scope's own floor`;
      const top = 20, bot = -120;
      // TRLC-LINKS: REQ-SDS-270
      const y = (db) => H * (top - Math.max(bot, Math.min(top, db))) / (top - bot);
      g.strokeStyle = "#182430"; g.lineWidth = 1;
      for (let db = 0; db >= bot; db -= 20) { g.beginPath(); g.moveTo(0, y(db)); g.lineTo(W, y(db)); g.stroke(); g.fillStyle = DIMCOL; g.fillText(db + " dBV", 4 * d, y(db) - 2 * d); }
      for (const n of sndNoteGrid(10, snd.rate / 2, set.a4)) { const x = sndAxisLog(n.f, W); g.strokeStyle = "#24364a"; g.beginPath(); g.moveTo(x, 0); g.lineTo(x, H); g.stroke(); g.fillStyle = DIMCOL; g.fillText(n.name, x + 2 * d, H - 4 * d); }
      // the floor at this range
      const fl = sndFloorDBV(sp.binHz, snd.lastVpc[set.ch]);
      g.strokeStyle = "#7c8894"; g.setLineDash([3 * d, 3 * d]); g.beginPath(); g.moveTo(0, y(fl)); g.lineTo(W, y(fl)); g.stroke(); g.setLineDash([]);
      for (const ch of [1 - set.ch, set.ch]) {
        const s2 = snd.spec[ch];
        if (!s2) continue;
        g.strokeStyle = ch === 1 ? C2COL : C1COL; g.globalAlpha = ch === set.ch ? 1 : 0.35; g.lineWidth = d; g.beginPath();
        let lastX = -1, best = -Infinity, pen = false;
        for (let k = 1; k < s2.dbv.length; k++) {
          const x = Math.round(sndAxisLog(k * s2.binHz, W));
          if (x < 0) continue;
          best = Math.max(best, s2.dbv[k]);
          if (x === lastX) continue;
          if (pen) g.lineTo(x, y(best)); else { g.moveTo(x, y(best)); pen = true; }
          lastX = x; best = -Infinity;
        }
        g.stroke();
      }
      g.globalAlpha = 1;
      const h = snd.harm;
      if (h && h.ok) for (const x of h.h) if (x.found) { const xx = sndAxisLog(x.hz, W); g.fillStyle = "#f2a63b"; g.fillText("H" + x.n, xx - 6 * d, y(x.dbv) - 6 * d); }
      break;
    }
    case "ab": {
      const a = snd.abA, b = snd.abB;
      $("sndViewSub").textContent = `in C${set.ab_in + 1} → out C${2 - set.ab_in} · left: transfer curve (out against in) · right: both spectra`;
      if (!a || !a.length) { msg("A/B: waiting"); break; }
      const half = W / 2;
      let al = Infinity, ah = -Infinity, bl = Infinity, bh = -Infinity;
      for (let i = 0; i < a.length; i++) { al = Math.min(al, a[i]); ah = Math.max(ah, a[i]); bl = Math.min(bl, b[i]); bh = Math.max(bh, b[i]); }
      g.fillStyle = "rgba(240,108,196,0.5)";
      const step = Math.max(1, Math.floor(a.length / 6000));
      for (let i = 0; i < a.length; i += step) g.fillRect((a[i] - al) / (ah - al || 1) * (half - 8 * d), H - (b[i] - bl) / (bh - bl || 1) * H, 1.5 * d, 1.5 * d);
      g.fillStyle = DIMCOL; g.fillText("out vs in", 4 * d, 14 * d);
      const top = 20, bot = -120;
      // TRLC-LINKS: REQ-SDS-271
      const y = (db) => H * (top - Math.max(bot, Math.min(top, db))) / (top - bot);
      for (const [sp, c, al2] of [[snd.specA, C1COL, 0.45], [snd.specB, C2COL, 1]]) {
        if (!sp) continue;
        g.strokeStyle = c; g.globalAlpha = al2; g.beginPath();
        let pen = false, lastX = -1, best = -Infinity;
        for (let k = 1; k < sp.dbv.length; k++) {
          const x = Math.round(half + sndLogX(k * sp.binHz, 10, snd.rate / 2) * half);
          if (x < half) continue;
          best = Math.max(best, sp.dbv[k]);
          if (x === lastX) continue;
          if (pen) g.lineTo(x, y(best)); else { g.moveTo(x, y(best)); pen = true; }
          lastX = x; best = -Infinity;
        }
        g.stroke();
      }
      g.globalAlpha = 1;
      break;
    }
  }
  glCardEnd(cv);
}

// sndRow is a table row of label and value(s).
// TRLC-LINKS: REQ-SDS-273
function sndRow(k, ...v) { return `<tr><td class="k">${k}</td>${v.map((x) => `<td>${x}</td>`).join("")}</tr>`; }

// TRLC-LINKS: REQ-SDS-269, REQ-SDS-270, REQ-SDS-271, REQ-SDS-272
function sndRenderCtx() {
  const set = snd.settings, rows = [];
  // TRLC-LINKS: REQ-SDS-269
  const pct = (v) => Number.isFinite(v) ? v.toFixed(2) + " %" : "—";
  let title = "Details";
  switch (snd.view) {
    case "cycle": {
      title = "Cycle";
      const c = snd.cycle;
      if (c && !snd.cycleWhy) rows.push(sndRow("period", eng(c.periodS, "s", 4), "frequency", eng(1 / c.periodS, "Hz", 5)),
        sndRow("Vpp", eng(c.vpp, "V"), "duty", c.dutyPct.toFixed(1) + " %"),
        sndRow("rise 10-90", eng(c.riseS, "s"), "fall", eng(c.fallS, "s")),
        sndRow("slew", c.slewVus.toPrecision(3) + " V/µs", "trigger", "FPGA mark, placed"));
      break;
    }
    case "mod": {
      title = "Modulation";
      const m = snd.mod;
      if (m && m.ok) {
        rows.push(sndRow("tremolo (AM)", m.hasAM ? `${m.amPct.toFixed(1)} % (${m.amDB.toFixed(1)} dB)` : `none (${m.amPct.toFixed(1)} %)`, "rate", m.hasAM ? `${m.amRate.toPrecision(3)} Hz ${m.amShape}` : "—"),
          sndRow("vibrato (FM)", m.hasFM ? `±${m.fmCents.toFixed(1)} c (±${m.fmHz.toPrecision(3)} Hz)` : `none (±${m.fmCents.toFixed(1)} c)`, "rate", m.hasFM ? `${m.fmRate.toPrecision(3)} Hz ${m.fmShape}` : "—"),
          sndRow("W&F weighted 2σ", pct(m.wfPct), "wow / flutter", `${pct(m.wowPct)} / ${pct(m.flutterPct)}`));
        const sb = snd.sb;
        if (sb && sb.ok && sb.lines >= 3 && sb.fit > 0.8 && sb.beta > 0.2) rows.push(sndRow("audio-rate FM", `C:M ${sb.ratio} · index ${sb.beta.toFixed(2)}`, "fm / fit", `${eng(sb.fm, "Hz")} / ${sb.fit.toFixed(2)}`));
      } else if (snd.lfo && snd.lfo.ok) {
        const l = snd.lfo;
        rows.push(sndRow("LFO rate", `${l.rateHz.toPrecision(4)} Hz ${l.shape}`, "Vpp", eng(l.vpp, "V")), sndRow("mean", eng(l.mean, "V"), "rise share", l.risePct.toFixed(0) + " %"));
      }
      break;
    }
    case "spec": {
      title = "Harmonics";
      const h = snd.harm;
      if (h && h.ok) {
        rows.push(sndRow("H1", eng(h.f0, "Hz", 5), h.h[0].dbv.toFixed(1) + " dBV"));
        for (let i = 1; i < h.h.length; i += 2) {
          // TRLC-LINKS: REQ-SDS-270
          const cell = (x) => x ? (x.found ? x.relDB.toFixed(1) + " dB" : "—") : "";
          rows.push(sndRow("H" + (i + 1) + " / H" + (i + 2), cell(h.h[i]), cell(h.h[i + 1])));
        }
        rows.push(sndRow("THD", pct(h.thdPct), Number.isFinite(h.evenOdd) ? `even/odd ${h.evenOdd.toFixed(1)} dB · ${h.character}` : ""));
        if (h.hum && h.hum.found) rows.push(sndRow("hum", `${h.hum.mainHz} Hz ${h.hum.dbv.toFixed(1)} dBV`, `family ${h.hum.sumDBV.toFixed(1)} dBV`));
      }
      break;
    }
    case "ab": {
      title = "A/B";
      const r = snd.ab;
      if (r && r.ok) rows.push(sndRow("gain at " + eng(r.f0, "Hz"), r.gainDB.toFixed(2) + " dB", "phase", r.phaseDeg.toFixed(1) + "°"),
        sndRow("RMS gain", r.rmsGainDB.toFixed(2) + " dB", "delay", r.delayOK ? eng(r.delayS, "s") : "—"),
        sndRow("THD in", pct(r.thdA), "THD out", pct(r.thdB)), sndRow("added THD", pct(r.addedPct), "", ""));
      else if (r) rows.push(sndRow("waiting", r.why || ""));
      break;
    }
    case "env": {
      title = "Envelope";
      const r = snd.report && snd.report.adsr_res;
      if (r && r.OK) rows.push(...sndAdsrRows(r));
      break;
    }
  }
  $("sndCtxTitle").textContent = title;
  $("sndCtxBody").innerHTML = rows.join("") || `<tr><td class="k">—</td></tr>`;
}

// sndAdsrRows are the envelope's figures (the scope's reading).
// TRLC-LINKS: REQ-SDS-268
function sndAdsrRows(r) {
  // TRLC-LINKS: REQ-SDS-268
  const s = (v) => typeof v === "number" ? eng(v, "s") : "—";
  const out = [sndRow("attack 10-90", s(r.Attack1090), "0-100", s(r.Attack0100))];
  if (r.HasSustain) out.push(sndRow("decay", s(r.Decay), "τ", s(r.DecayTau)), sndRow("sustain", r.SustainPct.toFixed(0) + " %", r.SustainDB.toFixed(1) + " dB"),
    sndRow("release", s(r.Release), "τ", s(r.ReleaseTau)));
  else out.push(sndRow("decay", s(r.Decay), "τ", s(r.DecayTau)), sndRow("sustain", "none (percussive)", ""));
  out.push(sndRow("peak", eng(r.Peak, "V"), "floor", eng(r.Floor, "V")));
  return out;
}

// TRLC-LINKS: REQ-SDS-267, REQ-SDS-268
function sndRenderReadouts() {
  const rep = snd.report;
  $("sndReadAdsr").hidden = snd.read !== "adsr";
  $("sndReadVoct").hidden = snd.read !== "voct";
  $("sndReadDrift").hidden = snd.read !== "drift";
  if (!rep) return;
  if (snd.read === "adsr") {
    const busy = rep.adsr === "armed" || rep.adsr === "capturing";
    $("sndArm").textContent = busy ? "Cancel" : "Arm";
    const rows = [sndRow("state", rep.adsr + (rep.adsr_why ? " — " + rep.adsr_why : ""))];
    if (rep.adsr === "done" && rep.adsr_res && rep.adsr_res.OK) rows.push(...sndAdsrRows(rep.adsr_res));
    $("sndAdsrBody").innerHTML = rows.join("");
  } else if (snd.read === "voct") {
    const f = rep.voct_fit || {}, rows = f.Rows || [];
    $("sndVoctBody").innerHTML = rows.map((r) => `<tr><td>${r.V.toFixed(3)} V</td><td>${r.Hz.toFixed(2)}</td><td>${r.Note}</td><td>${(r.ErrCents >= 0 ? "+" : "") + r.ErrCents.toFixed(1)} c</td><td>${(r.ResidCents >= 0 ? "+" : "") + r.ResidCents.toFixed(1)} c</td></tr>`).join("");
    $("sndVoctFit").textContent = f.OK ? `scale ${f.ScaleCents >= 0 ? "+" : ""}${f.ScaleCents.toFixed(1)} c/V · trim the scale by ${f.TrimPct >= 0 ? "+" : ""}${f.TrimPct.toFixed(2)} % · ${f.SpanOctaves.toFixed(2)} octaves` +
      (rows.length >= 4 ? ` · top against the lower half's fit ${f.HFDevCents >= 0 ? "+" : ""}${f.HFDevCents.toFixed(1)} c` : "") :
      `CV on C${2 - snd.settings.ch}, VCO on C${snd.settings.ch + 1}: take points at several CVs`;
  } else {
    const pts = rep.drift || [];
    $("sndDriftSub").textContent = pts.length ? `${pts.length} s · now ${pts[pts.length - 1].cents.toFixed(2)} c from the first second (${pts[pts.length - 1].hz.toFixed(3)} Hz)` : "the pitch of the tuned channel, once a second";
    const cv = $("sndDriftCv");
    if (!fitCanvas(cv)) return;
    const g = glCardCtx(cv, "#05080c");
    if (!g) return;
    const W = cv.width, H = cv.height;
    let span = 2;
    for (const p of pts) if (p && Number.isFinite(p.cents)) span = Math.max(span, Math.abs(p.cents));
    g.strokeStyle = "#182430"; g.beginPath(); g.moveTo(0, H / 2); g.lineTo(W, H / 2); g.stroke();
    g.strokeStyle = snd.settings.ch === 1 ? C2COL : C1COL; g.beginPath();
    pts.forEach((p, i) => { const x = W * i / Math.max(1, pts.length - 1), y = H / 2 - (p.cents || 0) / (span * 1.1) * (H / 2); if (i) g.lineTo(x, y); else g.moveTo(x, y); });
    g.stroke();
    g.fillStyle = DIMCOL; g.font = (10 * (window.devicePixelRatio || 1)) + "px monospace"; g.fillText("±" + span.toFixed(1) + " c", 4, 12);
    glCardEnd(cv);
  }
}

// ---- views, keys, enter and leave ------------------------------------------------------
const SND_VIEWS = ["cycle", "env", "mod", "spec", "ab"];

// sndNextView is SOUND again (Alt+6 on SOUND): the next view.
// TRLC-LINKS: REQ-SDS-274
function sndNextView() { sndSetView(SND_VIEWS[(SND_VIEWS.indexOf(snd.view) + 1) % SND_VIEWS.length]); }

// TRLC-LINKS: REQ-SDS-274
function sndSetView(v) {
  snd.view = v;
  $("sndView").value = v;
  $("sndCycles").hidden = v !== "cycle";
  snd.cycles = []; snd.cycleAt = -1; snd.modAt = 0; snd.specAt = 0; snd.abAt = 0; snd.spec = [null, null];
  try { localStorage.setItem(SND_PREF + "view", v); } catch (e) { /* private window */ }
}

let sndTimer = 0;

// sndEnter starts the stream (and the readouts) as SOUND is shown.
// TRLC-LINKS: REQ-SDS-274
function sndEnter() {
  snd.shown = true;
  snd.gen++;
  snd.cursor = null;
  snd.validFrom = snd.next;
  sndPump(snd.gen);
  sndPoll();
  clearInterval(sndTimer);
  sndTimer = setInterval(sndTick, 100);
}

// sndLeave stops the stream and Listen as SOUND goes: the scope captures again.
// TRLC-LINKS: REQ-SDS-274
function sndLeave() {
  snd.shown = false;
  snd.gen++;
  clearInterval(sndTimer);
  if (snd.audio.on) { sndListen(false); toast("Listen stopped: the scope captures again"); }
}

WS_HOOKS.push({
  // TRLC-LINKS: REQ-SDS-274
  enter(id) { if (id === "sound") sndEnter(); },
  // TRLC-LINKS: REQ-SDS-274
  leave(prev) { if (prev === "sound") sndLeave(); },
});

// sndPref reads a per-browser Listen choice.
// TRLC-LINKS: REQ-SDS-265
function sndPref(k, def) { try { const v = localStorage.getItem(SND_PREF + k); return v === null ? def : v; } catch (e) { return def; } }

// TRLC-LINKS: REQ-SDS-265
function sndPrefSet(k, v) { try { localStorage.setItem(SND_PREF + k, String(v)); } catch (e) { /* private window */ } }

// sndWire connects the controls.
// TRLC-LINKS: REQ-SDS-274, REQ-SDS-265
function sndWire() {
  for (const id of ["sndSrc", "sndGainMode", "sndView", "sndCycles", "sndCh", "sndSlow", "sndRead", "sndEnvCV", "sndVAuto", "sndDriftOn"]) segInit($(id));
  const a = snd.audio;
  a.src = sndPref("src", "c1"); a.auto = sndPref("auto", "1") === "1"; a.gainDB = +sndPref("gain", "0"); a.vol = +sndPref("vol", "0.7");
  a.hp = sndPref("hp", "1") === "1"; a.lp = sndPref("lp", "0") === "1";
  $("sndSrc").value = a.src; $("sndGainMode").value = a.auto ? "auto" : "man"; $("sndGain").value = a.gainDB; $("sndVol").value = a.vol;
  $("sndHP").classList.toggle("on", a.hp); $("sndLP").classList.toggle("on", a.lp);
  $("sndListen").onclick = () => sndListen(!a.on);
  $("sndSrc").onchange = () => { a.src = $("sndSrc").value; a.filt = null; a.delay = []; sndPrefSet("src", a.src); };
  $("sndMute").onclick = () => { a.mute = !a.mute; $("sndMute").classList.toggle("on", a.mute); };
  $("sndVol").oninput = () => { a.vol = +$("sndVol").value; sndPrefSet("vol", a.vol); };
  $("sndGainMode").onchange = () => { a.auto = $("sndGainMode").value === "auto"; sndPrefSet("auto", a.auto ? 1 : 0); };
  $("sndGain").oninput = () => { a.gainDB = +$("sndGain").value; a.auto = false; $("sndGainMode").value = "man"; sndPrefSet("gain", a.gainDB); sndPrefSet("auto", 0); };
  for (const [id, k] of [["sndHP", "hp"], ["sndLP", "lp"], ["sndNotch", "notch"]]) {
    $(id).onclick = () => { a[k] = !a[k]; $(id).classList.toggle("on", a[k]); a.filt = null; if (k !== "notch") sndPrefSet(k, a[k] ? 1 : 0); };
  }
  $("sndView").onchange = () => sndSetView($("sndView").value);
  $("sndCycles").onchange = () => sndSet({ cycles: +$("sndCycles").value });
  $("sndCh").onchange = () => sndSet({ ch: +$("sndCh").value });
  $("sndSlow").onchange = () => sndSet({ slow: $("sndSlow").value === "1" });
  $("sndA4").onchange = () => { const v = +$("sndA4").value; if (v >= 400 && v <= 480) sndSet({ a4: v }); else toast("A4 between 400 and 480 Hz", "warn"); };
  $("sndRange").onchange = () => sndSet({ range: $("sndRange").value });
  $("sndRef").onchange = () => sndSet({ ref: $("sndRef").value });
  $("sndRead").onchange = () => { snd.read = $("sndRead").value; };
  $("sndArm").onclick = () => { const busy = snd.report && (snd.report.adsr === "armed" || snd.report.adsr === "capturing"); sndPost({ action: busy ? "adsr_cancel" : "adsr_arm" }); if (!busy) sndSetView("env"); };
  $("sndGate").onchange = () => sndSet({ gate: +$("sndGate").value });
  $("sndGateV").onchange = () => sndSet({ gate_v: +$("sndGateV").value });
  $("sndEnvCV").onchange = () => sndSet({ env_cv: $("sndEnvCV").value === "1" });
  $("sndVTake").onclick = () => sndPost({ action: "voct_capture" });
  $("sndVUndo").onclick = () => sndPost({ action: "voct_undo" });
  $("sndVClear").onclick = () => sndPost({ action: "voct_clear" });
  $("sndVAuto").onchange = () => sndSet({ voct_auto: $("sndVAuto").value === "1" });
  $("sndDriftOn").onchange = () => sndSet({ drift: $("sndDriftOn").value === "1" });
  $("sndDriftClear").onclick = () => sndPost({ action: "drift_clear" });
  sndSetView(sndPref("view", "cycle"));
}
sndWire();
