// ENGMODEL-OWNER-UNIT: FU-WEB-SOUND
// sound_client.js — the browser's end of the audio stream (ADR-SOUND), shared
// by the web page's SOUND tab and the phone remote: /api/sound.bin long-polled
// into a ring of volts (the newest 2^20 samples a channel), the FPGA's edge
// marks beside it, and Listen: the listen source through high-pass,
// low-pass, notch and gain into the AudioWorklet player (sound_worklet.js:
// jitter buffer, rate following, limiter). No DOM: the pages draw.
"use strict";

const SND_RING = 1 << 20;
const SND_MASK = SND_RING - 1;

// sndClient makes a stream client; wantMarks says when a page shows the
// cycle (the scope then keeps marking the tuned channel's edges).
// TRLC-LINKS: REQ-SDS-265
function sndClient(wantMarks) {
  return {
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
    err: "",
    blocks: 0,
    shown: false,
    gen: 0,
    wantMarks: wantMarks || (() => false),
    // the take shown (STOP, a NORMAL or SINGLE trigger's): its samples in
    // volts, the window the readouts and the drawing use, Listen's place in it
    mode: "live",
    take: null,
    takeOn: false,
    takeBusy: 0,
    tview: { from: 0, span: 0 },
    focus: -1,
    play: { pos: 0, loop: true, speed: 1, fed: 0, on: false },
    audio: {
      on: false, ctx: null, node: null, src: "c1", mute: false, vol: 0.7, auto: true, gainDB: 0, hp: true, lp: false, notch: false,
      hz: 0, agc: 1, peak: 0, stat: {}, filt: null, delay: [], abMatch: 1, abDelay: 0, quiet: 0,
      probe: { recv: [], bursts: [] },
    },
  };
}

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
function sndVoltsOf(c, h, q, ch) {
  const out = new Float32Array(q.length), sc = h.scales || [];
  let k = -1, vpc = 1, off = 0;
  for (let i = 0; i < q.length; i++) {
    const ord = h.first + i;
    while (k + 1 < sc.length && (k < 0 || sc[k + 1].from <= ord)) { k++; vpc = sc[k].vpc[ch]; off = sc[k].off[ch]; }
    out[i] = (q[i] - 32768) * vpc / 256 - off;
  }
  if (sc.length) c.lastVpc[ch] = sc[sc.length - 1].vpc[ch];
  return out;
}

// sndIngest puts a reply's samples into the ring, its marks into the list,
// and the listen source into the player.
// TRLC-LINKS: REQ-SDS-265
function sndIngest(c, buf) {
  const p = sndParse(buf);
  if (!p) return;
  const h = p.hdr, n = h.n | 0;
  c.hdr = h;
  c.markV = h.mark_v;
  if (!n) { if (c.cursor === null || c.cursor > h.next) c.cursor = h.next; return; }
  const gap = c.cursor === null || h.first !== c.cursor || (h.gaps && h.gaps.length > 0);
  const v1 = sndVoltsOf(c, h, p.q1, 0), v2 = sndVoltsOf(c, h, p.q2, 1);
  for (let i = 0; i < n; i++) { const k = (h.first + i) & SND_MASK; c.c1[k] = v1[i]; c.c2[k] = v2[i]; }
  if (gap) c.validFrom = h.gaps && h.gaps.length ? h.gaps[h.gaps.length - 1] : h.first;
  c.next = h.first + n;
  c.cursor = c.next;
  c.blocks++;
  if (c.next - c.validFrom > SND_RING) c.validFrom = c.next - SND_RING;
  for (const m of h.marks || []) if (!c.marks.length || m.at > c.marks[c.marks.length - 1].at) c.marks.push(m);
  while (c.marks.length && c.marks[0].at < c.validFrom) c.marks.shift();
  if (c.marks.length > 2048) c.marks.splice(0, c.marks.length - 2048);
  for (const [ch, q] of [[0, p.q1], [1, p.q2]]) if (sndClipCodes(q)) c.clipAt[ch] = performance.now();
  if (c.audio.on && !c.takeOn) sndAudioFeed(c, v1, v2, gap);
}

// ---- takes ------------------------------------------------------------------------
// sndClientSync follows the scope's stream state (a report's info): its
// mode, and the take it shows, fetched once per take.
// TRLC-LINKS: REQ-SDS-275
function sndClientSync(c, info) {
  if (!info) return;
  c.mode = info.mode || "live";
  const want = c.mode !== "live" && info.take ? info.take : 0;
  if (want && (!c.take || c.take.id !== want) && c.takeBusy !== want) sndTakeFetch(c, want);
  const on = !!(want && c.take); // a newer take shows once it is here
  if (on !== c.takeOn) {
    c.takeOn = on;
    c.play.on = false;
    if (c.audio.node) c.audio.node.port.postMessage({ reset: true, srcRate: c.rate });
  }
}

// sndTakeFetch reads the take (/api/sound/take.bin) into volts: shown whole,
// Listen from its start.
// TRLC-LINKS: REQ-SDS-275
async function sndTakeFetch(c, id) {
  c.takeBusy = id;
  try {
    const r = await fetch("/api/sound/take.bin", { cache: "no-store" });
    if (!r.ok) throw new Error("HTTP " + r.status);
    const p = sndParse(await r.arrayBuffer());
    if (!p) throw new Error("not a take");
    const h = p.hdr;
    c.take = { id: h.id, why: h.why, first: h.first, n: h.n | 0, rate: h.rate || c.rate, c1: sndVoltsOf(c, h, p.q1, 0), c2: sndVoltsOf(c, h, p.q2, 1),
      hasTrig: !!h.has_trig, trig: h.trig, trigCh: h.trig_ch | 0, trigRising: !!h.trig_rising, trigV: h.trig_v, marks: h.marks || [], gaps: h.gaps || [] };
    // A NORMAL refresh of the same length keeps the window, the cursor and
    // Listen's place; another take is shown whole.
    if (!c.tview.span || c.tview.from + c.tview.span > c.take.n || c.take.why === "stop") {
      c.tview = { from: 0, span: c.take.n };
      c.focus = -1;
      c.play.pos = 0;
    }
  } catch (e) {
    c.err = "take: " + (e.message || e);
  }
  c.takeBusy = 0;
}

// sndTakeAround is n samples of channel ch around the focus (the cursor, or
// the window's centre), clipped to the take.
// TRLC-LINKS: REQ-SDS-275
function sndTakeAround(c, ch, n) {
  const t = c.take, src = ch === 1 ? t.c2 : t.c1;
  n = Math.max(0, Math.min(n, t.n));
  const mid = c.focus >= 0 ? c.focus : c.tview.from + c.tview.span / 2;
  const a = Math.max(0, Math.min(t.n - n, Math.round(mid - n / 2)));
  return Float64Array.from(src.subarray(a, a + n));
}

// sndTakeWindow is the window's samples of channel ch.
// TRLC-LINKS: REQ-SDS-275
function sndTakeWindow(c, ch) {
  const t = c.take, src = ch === 1 ? t.c2 : t.c1, a = Math.max(0, Math.floor(c.tview.from)), b = Math.min(t.n, Math.ceil(c.tview.from + c.tview.span));
  return src.subarray(a, b);
}

// sndTakeZoom scales the window by k about sample at (k < 1 narrows), from
// 64 samples to the whole take.
// TRLC-LINKS: REQ-SDS-275
function sndTakeZoom(c, k, at) {
  const t = c.take, v = c.tview;
  if (!t) return;
  const span = Math.max(Math.min(64, t.n), Math.min(t.n, v.span * k));
  const f = v.span > 0 ? (at - v.from) / v.span : 0.5;
  v.from = Math.max(0, Math.min(t.n - span, at - f * span));
  v.span = span;
}

// sndTakeScroll moves the window by d samples.
// TRLC-LINKS: REQ-SDS-275
function sndTakeScroll(c, d) {
  const t = c.take, v = c.tview;
  if (t) v.from = Math.max(0, Math.min(t.n - v.span, v.from + d));
}

// sndTakeFeed keeps Listen playing the take: the window looped (or once),
// at the speed chosen (the player told the slower rate), the buffer a
// quarter of a second ahead.
// TRLC-LINKS: REQ-SDS-275, REQ-SDS-265
function sndTakeFeed(c) {
  const a = c.audio, t = c.take, p = c.play;
  if (!a.on || !a.node || !c.takeOn || !t) return;
  if (!p.on) {
    p.on = true;
    p.fed = p.speed;
    if (p.pos < c.tview.from || p.pos >= c.tview.from + c.tview.span) p.pos = Math.floor(c.tview.from);
    a.node.port.postMessage({ reset: true, srcRate: c.rate * p.speed });
    a.filt = null;
  }
  if (p.fed !== p.speed) { p.fed = p.speed; a.node.port.postMessage({ reset: true, srcRate: c.rate * p.speed }); }
  const fill = (a.stat && a.stat.fillMs) || 0;
  if (a.stat && a.stat.primed && fill > 250) return;
  const lo = Math.floor(c.tview.from), hi = Math.min(t.n, Math.ceil(c.tview.from + c.tview.span));
  if (hi - lo < 16) return;
  let n = Math.round(c.rate * 0.1);
  const v1 = new Float32Array(n), v2 = new Float32Array(n);
  for (let i = 0; i < n; i++) {
    if (p.pos >= hi || p.pos < lo) {
      if (!p.loop && p.pos >= hi) { n = i; break; }
      p.pos = lo;
    }
    v1[i] = t.c1[p.pos]; v2[i] = t.c2[p.pos];
    p.pos++;
  }
  if (n > 0) sndAudioFeed(c, v1.subarray(0, n), v2.subarray(0, n), false);
}

// sndPump long-polls the stream while the client is started.
// TRLC-LINKS: REQ-SDS-265
async function sndPump(c, gen) {
  while (c.shown && gen === c.gen) {
    let url = "/api/sound.bin?max=16384&min=1024&wait=600";
    if (c.cursor !== null) url += "&from=" + c.cursor;
    if (c.wantMarks()) url += "&marks=1";
    try {
      const r = await fetch(url, { cache: "no-store" });
      if (!r.ok) throw new Error("HTTP " + r.status);
      sndIngest(c, await r.arrayBuffer());
      c.err = "";
    } catch (e) {
      c.err = String(e.message || e);
      await new Promise((res) => setTimeout(res, 600));
    }
  }
}

// sndClientStart starts streaming (a fresh signal from now).
// TRLC-LINKS: REQ-SDS-265
function sndClientStart(c) {
  c.shown = true;
  c.gen++;
  c.cursor = null;
  c.validFrom = c.next;
  sndPump(c, c.gen);
}

// sndClientStop stops streaming and Listen.
// TRLC-LINKS: REQ-SDS-265
function sndClientStop(c) {
  c.shown = false;
  c.gen++;
  if (c.audio.on) sndClientListen(c, false);
}

// sndClientLatest is the newest n samples of channel ch that are one signal
// (a take shown: n of its samples around the cursor or the window's centre).
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-275
function sndClientLatest(c, ch, n) {
  if (c.takeOn) return sndTakeAround(c, ch, n);
  const have = Math.max(0, c.next - c.validFrom);
  n = Math.max(0, Math.min(n, have, SND_RING));
  const out = new Float64Array(n), src = ch === 1 ? c.c2 : c.c1, from = c.next - n;
  for (let i = 0; i < n; i++) out[i] = src[(from + i) & SND_MASK];
  return out;
}

// sndClientRange is samples [from, from+n) of channel ch (clipped to what is
// held).
// TRLC-LINKS: REQ-SDS-272
function sndClientRange(c, ch, from, n) {
  if (c.takeOn) {
    const t = c.take, a = Math.max(from, t.first), b = Math.min(from + n, t.first + t.n), src = ch === 1 ? t.c2 : t.c1;
    return { from: a, x: Float64Array.from(src.subarray(Math.max(0, a - t.first), Math.max(0, b - t.first))) };
  }
  const a = Math.max(from, c.validFrom), b = Math.min(from + n, c.next);
  const out = new Float64Array(Math.max(0, b - a)), src = ch === 1 ? c.c2 : c.c1;
  for (let i = 0; i < out.length; i++) out[i] = src[(a + i) & SND_MASK];
  return { from: a, x: out };
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
function sndFilters(c) {
  const fs = c.rate, hz = c.audio.hz;
  c.audio.filt = [0, 1].map(() => ({ hp: sndBq(sndRbj("hp", 20, Math.SQRT1_2, fs)), lp: sndBq(sndRbj("lp", 5000, Math.SQRT1_2, fs)),
    notch: sndBq(sndNotchB(hz > 0 ? hz : 1000, fs)), notchHz: hz }));
}

// sndAudioFeed turns a block into the player's two outputs: the source, the
// high-pass, low-pass and notch, the gain (auto: the measured peak to a
// quarter of full scale, held through quiet so noise is not raised), volume
// and mute.
// TRLC-LINKS: REQ-SDS-265
function sndAudioFeed(c, v1, v2, gap) {
  const a = c.audio, n = v1.length;
  if (!a.node) return;
  if (!a.filt) sndFilters(c);
  const L = new Float32Array(n), R = new Float32Array(n);
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
  if (a.notch && a.hz > 0 && Math.abs(sndCents(a.hz, a.filt[0].notchHz || 1)) > 10) sndFilters(c);
  for (let k = 0; k < 2; k++) {
    const x = k ? R : L, f = a.filt[k];
    if (a.hp) sndBqRun(f.hp, x);
    if (a.lp) sndBqRun(f.lp, x);
    if (a.notch && a.hz > 0) sndBqRun(f.notch, x);
  }
  // the latency probe: a burst after a fifth of a second of quiet, received
  let pk = 0;
  for (let i = 0; i < n; i++) pk = Math.max(pk, Math.abs(L[i]), Math.abs(R[i]));
  const thr = 0.05;
  if (pk > thr && a.quiet > 0.2 * c.rate) a.probe.recv.push(performance.now());
  a.quiet = pk > thr ? 0 : a.quiet + n;
  if (a.probe.recv.length > 50) a.probe.recv.shift();
  let g;
  if (a.auto) {
    a.peak = Math.max(pk, a.peak * Math.exp(-n / (2 * c.rate))); // 2 s release
    if (a.peak > 0.005) a.agc = 0.25 / a.peak;
    g = Math.max(0.01, Math.min(1000, a.agc));
  } else g = Math.pow(10, a.gainDB / 20);
  g *= a.mute ? 0 : a.vol;
  for (let i = 0; i < n; i++) { L[i] *= g; R[i] *= g; }
  a.node.port.postMessage({ l: L, r: R, gap }, [L.buffer, R.buffer]);
}

// sndWorkletMsg takes the player's reports and its bursts (latency probe:
// when a burst after quiet leaves the player, on the performance clock, plus
// the output's own latency).
// TRLC-LINKS: REQ-SDS-265
function sndWorkletMsg(c, e) {
  const m = e.data, a = c.audio;
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

// sndClientListen starts or stops playing the stream (a user's tap or click
// must start audio). It resolves to whether it plays; err says why not.
// TRLC-LINKS: REQ-SDS-265
async function sndClientListen(c, on) {
  const a = c.audio;
  if (!on) {
    a.on = false;
    if (a.ctx) a.ctx.suspend();
    return false;
  }
  if (!a.ctx) {
    const AC = window.AudioContext || window.webkitAudioContext;
    a.ctx = new AC({ latencyHint: "interactive" });
    const opts = { srcRate: c.rate, targetMs: 160 };
    if (a.ctx.audioWorklet) {
      await a.ctx.audioWorklet.addModule("sound_worklet.js");
      a.node = new AudioWorkletNode(a.ctx, "sound-player", { numberOfInputs: 0, outputChannelCount: [2],
        processorOptions: opts });
      // TRLC-LINKS: REQ-SDS-265
      a.node.port.onmessage = (e) => sndWorkletMsg(c, e);
    } else {
      // Plain http (the scope's pages): no AudioWorklet outside a secure
      // context, so the same player runs on this thread.
      a.node = sndMainPlayer(c, a.ctx, opts);
    }
    a.node.connect(a.ctx.destination);
  }
  await a.ctx.resume();
  a.node.port.postMessage({ reset: true });
  a.filt = null;
  a.on = true;
  return true;
}

// sndMainPlayer is the player (sound_worklet.js's SoundPlayerCore) behind a
// ScriptProcessorNode, with a port that hands messages to it directly. Its
// 1024-frame block adds about 21 ms at 48 kHz to the worklet's latency.
// TRLC-LINKS: REQ-SDS-265
function sndMainPlayer(c, ctx, opts) {
  const node = ctx.createScriptProcessor(1024, 0, 2);
  const core = new SoundPlayerCore(opts, ctx.sampleRate, (m) => sndWorkletMsg(c, { data: m }));
  // TRLC-LINKS: REQ-SDS-265
  node.onaudioprocess = (e) => {
    const out = e.outputBuffer;
    core.render(out.getChannelData(0), out.getChannelData(1), e.playbackTime || ctx.currentTime);
  };
  // TRLC-LINKS: REQ-SDS-265
  node.port = { postMessage: (m) => core.onMsg(m) };
  return node;
}

// sndLatencyText is the latency the listener hears now, part by part.
// TRLC-LINKS: REQ-SDS-265
function sndLatencyText(c) {
  const a = c.audio;
  if (!a.on || !a.ctx) return "";
  const buf = a.stat.fillMs || 0, out = 1000 * ((a.ctx.outputLatency || 0) + (a.ctx.baseLatency || 0));
  // The scope seals a stream bank about every 17 ms and the reply follows
  // it at once; that and the network are about 25 ms on a LAN.
  const dev = 25;
  return `latency ≈ ${Math.round(dev + buf + out)} ms (stream ~${dev} + buffer ${buf.toFixed(0)} + output ${out.toFixed(0)})`;
}
