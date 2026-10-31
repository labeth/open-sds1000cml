// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// webdec.js — the digital stream decoded in the browser (ADR-STREAM-CLIENT-DECODE).
//
// The scope hands a page that asks (/api/stream/words.bin) the digital
// stream's words as they arrive - {CH2 bits 15..0, CH1 bits 15..0}, bit 0 the
// earliest period - and the page decodes them as the scope would: the
// engine's quiet-line skipping and pushes (soft_stream.go feedBitsQuiet,
// softPushEdges), the stream decoder over the lines' edges (decode/stream.go
// StreamDecoder, decode/edgeline.go EdgeLine) and the protocol's decoder
// (the decode_*.js twins of the Go decoders), each a step-for-step port, so
// its frames are the scope's. The frames go back to the scope
// (/api/stream/frames.bin), which keeps its transcript, LCD and stop on a
// value. A classic script: the worker (webdec_worker.js) and the node parity
// checks load it beside the decoders.

// ---- EdgeLine -------------------------------------------------------------------------
// A digital line held as its edges: n samples, the level of the first, and
// the samples where the level changes (decode/edgeline.go).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function WebDecLine() {
  this.n = 0;
  this.first = 0;
  this.at = new Int32Array(256);
  this.len = 0;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.reset = function () {
  this.n = 0;
  this.first = 0;
  this.len = 0;
};

// last is the level of the line's last sample (0 for an empty line).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.last = function () {
  return this.n === 0 ? 0 : this.first ^ (this.len & 1);
};

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.push = function (x) {
  if (this.len === this.at.length) {
    const a = new Int32Array(this.at.length * 2);
    a.set(this.at);
    this.at = a;
  }
  this.at[this.len++] = x;
};

// start opens the next samples at level lvl: the line's first, or an edge.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.start = function (lvl) {
  if (this.n === 0) {
    this.first = lvl;
    this.len = 0;
  } else if (this.last() !== lvl) this.push(this.n);
};

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.appendLevel = function (lvl, n) {
  if (n <= 0) return;
  this.start(lvl & 1);
  this.n += n;
};

// appendWords appends words[from..to) (Uint32Array) of line ch (0 CH1, 1
// CH2): a word's edges are its bits that differ from the bit before.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.appendWords = function (words, from, to, ch) {
  if (to <= from) return;
  const sh = ch ? 16 : 0;
  if (this.n === 0) {
    this.first = (words[from] >>> sh) & 1;
    this.len = 0;
  }
  // room for a word's every bit an edge, so the loop stores without a check
  const need = this.len + 16 * (to - from);
  if (need > this.at.length) {
    const a = new Int32Array(Math.max(need, 2 * this.at.length));
    a.set(this.at.subarray(0, this.len));
    this.at = a;
  }
  const out = this.at;
  let len = this.len;
  let prev = this.first ^ (len & 1);
  let at = this.n;
  for (let i = from; i < to; i++) {
    const w = (words[i] >>> sh) & 0xffff;
    let t = (w ^ ((w << 1) | prev)) & 0xffff;
    while (t !== 0) {
      const b = t & -t;
      out[len++] = at + 31 - Math.clz32(b);
      t ^= b;
    }
    prev = w >>> 15;
    at += 16;
  }
  this.len = len;
  this.n = at;
};

// search is the first edge index whose sample is >= x (sort.SearchInts).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.search = function (x) {
  let lo = 0;
  let hi = this.len;
  while (lo < hi) {
    const m = (lo + hi) >>> 1;
    if (this.at[m] < x) lo = m + 1;
    else hi = m;
  }
  return lo;
};

// appendRange appends samples from..to-1 of src.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.appendRange = function (src, from, to) {
  from = Math.max(from, 0);
  to = Math.min(to, src.n);
  if (to <= from) return;
  let k = src.search(from + 1);
  this.start(src.first ^ (k & 1));
  for (; k < src.len && src.at[k] < to; k++) this.push(this.n + src.at[k] - from);
  this.n += to - from;
};

// drop removes the first n samples.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.drop = function (n) {
  if (n <= 0) return;
  if (n >= this.n) {
    this.reset();
    return;
  }
  const k = this.search(n + 1);
  this.first ^= k & 1;
  this.at.copyWithin(0, k, this.len);
  this.len -= k;
  for (let i = 0; i < this.len; i++) this.at[i] -= n;
  this.n -= n;
};

// samples is the line as samples, 0 or 255.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.samples = function () {
  const out = new Uint8Array(this.n);
  let lvl = this.first;
  let from = 0;
  for (let k = 0; k <= this.len; k++) {
    const at = k < this.len ? this.at[k] : this.n;
    if (lvl) out.fill(255, from, at);
    lvl ^= 1;
    from = at;
  }
  return out;
};

// length is the line's samples: a decoder reads a line's length as its samples'.
Object.defineProperty(WebDecLine.prototype, "length", {
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
  get: function () { return this.n; },
});

// isEdgeLine marks a line the decoders read through its edges (sliceChannel).
WebDecLine.prototype.isEdgeLine = true;

// runs counts the line's high samples, and each level's samples in runs of
// three or more (EdgeLine.runs).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.runs = function () {
  let lvl = this.first;
  let from = 0;
  let ones = 0;
  const held = [0, 0];
  for (let k = 0; k <= this.len; k++) {
    const to = k < this.len ? this.at[k] : this.n;
    if (lvl) ones += to - from;
    if (to - from >= 3) held[lvl] += to - from;
    lvl ^= 1;
    from = to;
  }
  return { ones, held };
};

// webdecCodeCeil is decode.go codeCeil: the least code at or above t (256 none).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecCodeCeil(t) {
  if (!(t <= 255)) return 256;
  return Math.max(0, Math.ceil(t));
}

// webdecCodeFloor is decode.go codeFloor: the greatest code at or below t (-1 none).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecCodeFloor(t) {
  if (!(t >= 0)) return -1;
  return Math.min(255, Math.floor(t));
}

// sliceEdges is sliceChannel of the line's samples (0 or 255) built from its
// edges (EdgeLine.slice): the same rails, threshold, first level and edges,
// each edge at the crossing sliceChannel computes between a 0 and a 255. A
// line sliceChannel would refuse, or slice with a band outside the codes or
// settings of its own, is sliced from its samples. The samples and levels
// are made only for a decoder that reads them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecLine.prototype.sliceEdges = function (opts, sampled) {
  opts = opts || {};
  const thr = opts.threshold;
  const haveThr = thr != null;
  const n = this.n;
  const own = (opts.hystFrac != null && opts.hystFrac !== 0.2) || (opts.minAmp != null && opts.minAmp !== 20);
  const { ones, held } = this.runs();
  const floor = Math.max(1, 0.001 * n);
  let reason = own || n < 8 || n - ones < floor || ones < floor;
  if (reason && !own && haveThr) {
    const on = webdecCodeCeil(thr);
    if (n >= 8 && on >= 1 && on <= 255 && Math.min(held[0], held[1]) >= 3) reason = false;
  }
  const threshold = haveThr ? thr : 127.5;
  const band = 0.2 * 255 / 2;
  const thHi = threshold + band;
  const thLo = threshold - band;
  const on = webdecCodeCeil(threshold);
  if (reason || on < 1 || on > 255 || webdecCodeCeil(thHi) > 255 || webdecCodeFloor(thLo) < 0) return sampled(this.samples(), opts);
  const rise = (threshold - 0) / (255 - 0);
  const fall = (threshold - 255) / (0 - 255);
  const edges = new Array(this.len);
  let d = this.first ? -1 : 1;
  for (let k = 0; k < this.len; k++) {
    const j = this.at[k];
    edges[k] = { i: j, dir: d, x: j - 1 + (d > 0 ? rise : fall) };
    d = -d;
  }
  const S = { ok: true, reason: null, n, lowRail: 0, highRail: 255, amp: 255, threshold, thHi, thLo, edges, first: this.first, eline: this };
  const line = this;
  let codes = null;
  let level = null;
  Object.defineProperty(S, "codes", {
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
    get: function () { return codes || (codes = line.samples()); },
  });
  // The decoders on a digital line read a level at a sample or two (the
  // first), not every sample's: each read finds its edge.
  Object.defineProperty(S, "level", {
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
    get: function () { return level || (level = webdecLevels(line, n)); },
  });
  return S;
};

// webdecLevels is a line's level at each sample (sliceChannel's level
// array), read where a decoder reads it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecLevels(line, n) {
  const first = line.first;
  return new Proxy({}, {
    // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
    get: function (t, k) {
      if (k === "length") return n;
      const i = typeof k === "string" ? Number(k) : NaN;
      if (!(i >= 0 && i < n) || !Number.isInteger(i)) return undefined;
      return first ^ (line.search(i + 1) & 1);
    },
  });
}

// webdecInstall makes the decoders read a WebDecLine through its edges:
// sliceChannel slices it from its edges and logicAt reads its level there.
// The page's own decode (which never hands them a WebDecLine) is unchanged.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecInstall(scope) {
  if (!scope || typeof scope.sliceChannel !== "function" || scope.sliceChannel.webdec) return;
  const sampled = scope.sliceChannel;
  const level = scope.logicAt;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
  const slice = function (codes, opts) {
    if (codes && codes.isEdgeLine) return codes.sliceEdges(opts, sampled);
    return sampled(codes, opts);
  };
  slice.webdec = true;
  scope.sliceChannel = slice;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
  scope.logicAt = function (s, x) {
    if (!s.eline) return level(s, x);
    const i = Math.round(x);
    if (i < 0 || i >= s.n) return -1;
    return s.first ^ (s.eline.search(i + 1) & 1);
  };
}

// ---- StreamDecoder over edges ----------------------------------------------------------
// decode/stream.go's StreamDecoder with an edge window: a window starting
// in a silence between frames, each frame handed out once it is whole.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function WebDecStream(edec, closeN, maxN) {
  closeN = Math.max(closeN, 2);
  this.edec = edec;
  this.closeN = closeN;
  this.maxN = Math.max(maxN, 4 * closeN);
  this.leadN = 0;
  this.subframes = false;
  this.base = 0;
  this.done = 0;
  this.ea = new WebDecLine();
  this.eb = new WebDecLine();
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecStream.prototype.held = function () { return this.ea.n; };

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecStream.prototype.next = function () { return this.base + this.ea.n; };

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecStream.prototype.pushEdges = function (a, b, from, to) {
  this.ea.appendRange(a, from, to);
  if (b) this.eb.appendRange(b, from, to);
  return this.run();
};

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecStream.prototype.flush = function () {
  const out = this.run();
  this.skip(0);
  return out;
};

// skip drops the window and moves the stream on by n samples that will not
// arrive.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecStream.prototype.skip = function (n) {
  this.base += this.held() + n;
  this.done = Math.max(this.done, this.base);
  this.ea.reset();
  this.eb.reset();
};

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecStream.prototype.isFrameStart = function (s) {
  return s.kind === "sof" || s.kind === "break" || (this.subframes && s.kind === "rw");
};

// run decodes the window and hands out its whole frames (StreamDecoder.run).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecStream.prototype.run = function () {
  const n = this.held();
  if (n === 0) return [];
  const r = this.edec(this.ea, this.eb.n === n ? this.eb : null);
  const sp = [];
  for (const s of r.spans || []) {
    const i0 = s.i0;
    const i1 = s.i1 < s.i0 ? s.i0 : s.i1;
    if (i0 >= 0 && i0 < n && this.base + i0 >= this.done) sp.push({ i0, i1, text: s.text || "", kind: s.kind || "", val: s.val || 0 });
  }
  let sorted = true;
  for (let k = 1; k < sp.length && sorted; k++) sorted = sp[k - 1].i0 <= sp[k].i0;
  if (!sorted) sp.sort((x, y) => x.i0 - y.i0); // stable, as sort.SliceStable
  const groups = [];
  for (let k = 0; k < sp.length; k++) {
    const s = sp[k];
    if (!groups.length || this.isFrameStart(s) || s.i0 - groups[groups.length - 1].i1 > this.closeN) {
      groups.push({ from: k, to: k + 1, i0: s.i0, i1: s.i1 });
      continue;
    }
    const g = groups[groups.length - 1];
    g.to = k + 1;
    g.i1 = Math.max(g.i1, s.i1);
  }
  const out = [];
  const base = this.base;
  let last = -1;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
  const take = (g) => {
    const spans = [];
    for (let k = g.from; k < g.to; k++) {
      const s = sp[k];
      spans.push({ i0: base + s.i0, i1: base + s.i1, text: s.text, kind: s.kind, val: s.val });
    }
    out.push({ i0: base + g.i0, i1: base + g.i1, spans });
    last = g.i1;
  };
  let k = 0;
  for (; k < groups.length; k++) {
    if (n - 1 - groups[k].i1 <= this.closeN) break;
    take(groups[k]);
  }
  if (k < groups.length && n - groups[k].i0 > this.maxN) {
    const g = groups[k];
    let j = g.from;
    while (j < g.to && sp[j].i1 < n - this.closeN) j++;
    if (j > g.from) {
      take({ from: g.from, to: j, i0: g.i0, i1: sp[j - 1].i1 });
      groups[k].from = j;
      if (j < g.to) groups[k].i0 = sp[j].i0;
    }
  }
  let cut = 0;
  if (last >= 0) {
    let next = n;
    if (k < groups.length && groups[k].from < groups[k].to) next = groups[k].i0;
    cut = Math.max(last + 1, last + Math.trunc((next - last) / 2));
    cut = Math.min(cut, Math.max(last + 1, n - this.closeN), next);
    cut = Math.max(0, Math.min(cut, next - this.leadN));
    this.done = this.base + last + 1;
  } else if (k === groups.length || groups[k].from >= groups[k].to) {
    cut = Math.max(0, n - this.closeN - this.leadN);
  } else {
    cut = Math.max(0, groups[k].i0 - this.closeN - this.leadN);
  }
  const limit = 2 * this.maxN + this.leadN;
  if (n - cut > limit) {
    cut = n - limit;
    this.done = Math.max(this.done, this.base + cut);
  }
  if (cut > 0) {
    cut = Math.min(cut, n);
    this.ea.drop(cut);
    if (this.eb.n === n) this.eb.drop(cut);
    this.base += cut;
  }
  return out;
};

// ---- the protocols' decoders -------------------------------------------------------------
// WEBDEC_RATE are the one-line decoders the scope hands its rate (engine
// lineCfg(p, true)); the others measure it.
const WEBDEC_RATE = { miller: 1, dcf77: 1, em4100: 1, em4305: 1, t55xx: 1, sdq: 1 };

// WEBDEC_TWO are the clocked two-line protocols (engine clockedTwoLine).
const WEBDEC_TWO = { ps2: "decodePS2", swd: "decodeSWD", mdio: "decodeMDIO", wiegand: "decodeWiegand", quad: "decodeQuadrature",
  stepper: "decodeStepper", sdcard: "decodeSDCard", pdi: "decodePDI", cjtag: "decodeCJTAG", maple: "decodeMaple", caliper: "decodeCaliper" };

// WEBDEC_ONE are the one-line protocols' decoders by the decode package's names.
const WEBDEC_ONE = { lin: "decodeLIN", dmx512: "decodeDMX512", modbus: "decodeModbus", onewire: "decodeOneWire", ws281x: "decodeWS281x",
  dht: "decodeDHT", cec: "decodeCEC", j1850: "decodeJ1850", irnec: "decodeIRNEC", irsirc: "decodeIRSIRC", irrc5: "decodeIRRC5",
  irrc6: "decodeIRRC6", dali: "decodeDALI", usbpd: "decodeUSBPD", spdif: "decodeSPDIF", pwm: "decodePWM", rcencode: "decodeRCEncode",
  ook: "decodeOOK", miller: "decodeMiller", morse: "decodeMorse", dcf77: "decodeDCF77", em4100: "decodeEM4100", em4305: "decodeEM4305",
  t55xx: "decodeT55xx", swim: "decodeSWIM", sdq: "decodeSDQ", pjdl: "decodePJDL", dsi: "decodeDSI", qi: "decodeQi", irmp: "decodeIRMP" };

// WEBDEC_LINECFG are the decoders beyond the first thirty, which take a
// variant (engine lineCfg).
const WEBDEC_LINECFG = { pwm: 1, rcencode: 1, ook: 1, miller: 1, morse: 1, dcf77: 1, em4100: 1, em4305: 1, t55xx: 1, swim: 1, sdq: 1,
  pjdl: 1, dsi: 1, qi: 1, irmp: 1 };

// webdecDecoder is the protocol's decoder as the scope's registry calls it
// (engine serial_registry.go), on a window's samples: (a, b, sampleS,
// thresholdB) -> the decoder's result. null for a protocol it has none for.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecDecoder(proto, p, scope) {
  scope = scope || globalThis;
  const thr = p.haveThr ? p.threshold : undefined;
  if (WEBDEC_TWO[proto]) {
    const fn = scope[WEBDEC_TWO[proto]];
    if (!fn) return null;
    return (a, b, ss, thrB) => fn(a, b, ss, { threshold: thr, thresholdB: thrB });
  }
  const name = WEBDEC_ONE[proto];
  const fn = name && scope[name];
  if (!fn) return null;
  const cfg = { threshold: thr };
  if (WEBDEC_LINECFG[proto]) {
    cfg.variant = p.bits | 0;
    if (WEBDEC_RATE[proto]) cfg.baud = p.baud | 0;
  }
  if (proto === "lin" || proto === "dmx512" || proto === "modbus" || proto === "usbpd") cfg.baud = p.baud > 0 ? p.baud : 0;
  if (proto === "modbus") cfg.parity = p.parity || "";
  if (proto === "onewire") cfg.overdrive = !!p.overdrive;
  return (a, b, ss) => fn(a, ss, cfg);
}

// ---- the engine's stream, as the page runs it ----------------------------------------
// WebDecSession decodes one stream session's words as the scope's engine
// would (soft_stream.go): info is the words.bin header. Frames come out of
// onFrames(frames) in the stream's sample index, as the scope's decoder
// counts them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function WebDecSession(info, scope) {
  const p = info.params || {};
  this.info = info;
  this.p = p;
  this.ch = (p.chA | 0) & 1;
  this.two = info.lines === 2;
  this.sampleS = info.periodS;
  this.keepWords = info.keepWords;
  this.leadWords = info.leadWords;
  this.pushN = info.pushN;
  this.pushMax = info.pushMax || 1 << 18;
  webdecInstall(scope || globalThis);
  const dec = webdecDecoder(info.proto, p, scope);
  if (!dec) throw new Error("no decoder for " + info.proto);
  this.decodeFn = dec;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
  const edec = (a, b) => this.decode(a, b);
  this.dec = new WebDecStream(edec, info.closeN, info.maxN);
  this.dec.leadN = info.leadN;
  this.dec.subframes = !!info.subframes;
  this.pendA = new WebDecLine();
  this.pendB = new WebDecLine();
  this.idleWords = 0;
  this.skipped = 0;
  this.lvlA = 0;
  this.lvlB = 0;
  this.haveLvl = false;
  this.bLo = 0;
  this.bHi = 0;
  this.bSeen = false;
  this.frames = [];
  this.losses = [];
  this.samplesIn = 0;
  this.edgesIn = 0;
  this.decodeMs = 0;
  this.one = new Uint32Array(1);
  this.started = false;
}

// decode is the scope's edgeDecodeFunc: the decoder on the window, line B
// sliced at its learned level when the settings give none.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.decode = function (a, b) {
  let thrB = this.p.haveThrB ? this.p.thresholdB : undefined;
  if (!this.p.haveThrB && this.bSeen && this.bHi - this.bLo >= 24) thrB = (this.bLo + this.bHi) / 2;
  if (this.samplesPath) return this.decodeFn(a.samples(), b ? b.samples() : undefined, this.sampleS, thrB);
  return this.decodeFn(a, b || undefined, this.sampleS, thrB);
};

// position is the stream index of the next word's first sample.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.position = function () {
  return this.dec.next() + this.pendA.n + 16 * this.skipped;
};

// feedBits queues words[from..to) as the lines' edges (feedBits).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.feedBits = function (words, from, to) {
  if (to <= from) return;
  this.pendA.appendWords(words, from, to, this.ch);
  if (this.two) {
    const o = 1 - this.ch;
    this.pendB.appendWords(words, from, to, o);
    this.learnBWords(words, from, to, o);
  }
};

// learnBWords follows line B's extremes (learnBWords).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.learnBWords = function (words, from, to, ch) {
  const sh = ch ? 16 : 0;
  if (!this.bSeen) {
    const v = 255 * ((words[from] >>> sh) & 1);
    this.bLo = v;
    this.bHi = v;
    this.bSeen = true;
  }
  for (let i = from; i < to && (this.bLo !== 0 || this.bHi !== 255); i++) {
    const w = (words[i] >>> sh) & 0xffff;
    if (w !== 0xffff) this.bLo = 0;
    if (w !== 0) this.bHi = 255;
  }
};

// feedWords is feedBitsQuiet over words[from..to).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.feedWords = function (words, i0, i1) {
  const ch = this.ch;
  const two = this.two;
  let from = i0;
  for (let i = i0; i < i1; i++) {
    const w = words[i];
    const a = ch ? w >>> 16 : w & 0xffff;
    const b = two ? (ch ? w & 0xffff : w >>> 16) : 0;
    const quiet = this.haveLvl && a === this.lvlA && b === this.lvlB && (a === 0 || a === 0xffff) && (b === 0 || b === 0xffff);
    if (quiet) {
      if (++this.idleWords > this.keepWords) {
        this.feedBits(words, from, i);
        from = i + 1;
        this.skipped++;
      }
    } else if (this.skipped > 0) {
      this.feedBits(words, from, i);
      from = i;
      this.push();
      const lead = Math.min(this.skipped, this.leadWords);
      this.dec.skip(16 * (this.skipped - lead));
      this.queueLevel(16 * lead);
      this.skipped = 0;
      this.idleWords = 0;
    } else this.idleWords = 0;
    this.lvlA = a;
    this.lvlB = b;
    this.haveLvl = true;
  }
  this.feedBits(words, from, i1);
};

// feedRun is feedWords over n copies of the quiet word w, without making them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.feedRun = function (w, n) {
  if (n <= 0) return;
  this.one[0] = w;
  this.feedWords(this.one, 0, 1);
  let m = n - 1;
  if (m <= 0) return;
  // The rest are the line's level again: each queued as it is while the
  // line has been quiet keepWords words or less, then left out.
  const keep = Math.max(0, Math.min(m, this.keepWords - this.idleWords));
  if (keep > 0) {
    this.pendA.appendLevel(this.lvlA & 1, 16 * keep);
    if (this.two) {
      this.pendB.appendLevel(this.lvlB & 1, 16 * keep);
      this.learnBRun(this.lvlB);
    }
  }
  this.idleWords += m;
  this.skipped += m - keep;
};

// learnBRun is learnBWords over a quiet word of line B.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.learnBRun = function (b) {
  this.one[0] = this.ch ? b : b << 16;
  this.learnBWords(this.one, 0, 1, 1 - this.ch);
};

// queueLevel queues n samples of each line's quiet level.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.queueLevel = function (n) {
  this.pendA.appendLevel(this.lvlA & 1, n);
  if (this.two) this.pendB.appendLevel(this.lvlB & 1, n);
};

// push decodes the queued edges in the scope's pieces (softPushEdges).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.push = function () {
  const n = this.pendA.n;
  if (n === 0) return;
  const b = this.pendB.n === n ? this.pendB : null;
  const t0 = performance.now();
  this.samplesIn += n * (b ? 2 : 1);
  this.edgesIn += this.pendA.len + (b ? b.len : 0);
  for (let at = 0; at < n;) {
    const to = Math.min(n, at + this.pushMax);
    const f = this.dec.pushEdges(this.pendA, b, at, to);
    for (const x of f) this.frames.push(x);
    at = to;
  }
  this.decodeMs += performance.now() - t0;
  this.pendA.reset();
  this.pendB.reset();
};

// loss is words lost before index (a worker fault, or the page fell behind
// by the scope's ring): what arrived is decoded, the window dropped, and
// the decode starts afresh at index (softLoss). report: the scope does not
// know of it (the page fell behind), so the frames posted back mark it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.loss = function (index, report) {
  this.push();
  this.dec.skip(0);
  this.idleWords = 0;
  this.skipped = 0;
  this.haveLvl = false;
  if (report) this.losses.push(index);
  if (index > this.dec.next()) this.dec.skip(index - this.dec.next());
};

// feedChunk takes one chunk of a words.bin reply: its words coded as runs
// and literals (engine appendWordsRLE), then the scope's push check.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.feedChunk = function (c) {
  if (!this.started) {
    // the session's first words: the scope's decoder index starts there
    this.started = true;
    this.dec.base = c.index;
    this.dec.done = c.index;
  } else if (c.brk || c.index !== this.position()) {
    if (c.index < this.position() && !c.brk) return; // a chunk read before
    this.loss(c.index, !c.brk);
  }
  const t = c.toks;
  for (let i = 0; i < t.length;) {
    const k = t[i];
    if (k & 0x80000000) {
      this.feedRun(t[i + 1], k & 0x7fffffff);
      i += 2;
    } else {
      this.feedWords(t, i + 1, i + 1 + k);
      i += 1 + k;
    }
  }
  if (c.check && this.pendA.n >= this.pushN) this.push();
};

// takeFrames hands out the frames decoded so far.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
WebDecSession.prototype.takeFrames = function () {
  const f = this.frames;
  this.frames = [];
  return f;
};

// ---- the wire -----------------------------------------------------------------------------
// webdecParse reads a /api/stream/words.bin reply (an ArrayBuffer): the
// JSON header and the chunks, each chunk's tokens a view in buf.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecParse(buf) {
  const dv = new DataView(buf);
  if (buf.byteLength < 8 || dv.getUint8(0) !== 0x53 || dv.getUint8(1) !== 0x44 || dv.getUint8(2) !== 0x57 || dv.getUint8(3) !== 0x31)
    throw new Error("not a words.bin reply");
  const jl = dv.getUint32(4, true);
  const hdr = JSON.parse(new TextDecoder().decode(new Uint8Array(buf, 8, jl)));
  let at = 8 + jl;
  const chunks = [];
  for (let k = 0; k < (hdr.chunks | 0); k++) {
    const index = dv.getUint32(at, true) + dv.getUint32(at + 4, true) * 4294967296;
    const flags = dv.getUint32(at + 12, true);
    chunks.push({ index, words: dv.getUint32(at + 8, true), brk: (flags & 1) !== 0, check: (flags & 2) !== 0, bytes: dv.getUint32(at + 16, true) });
    at += 20;
  }
  for (const c of chunks) {
    c.toks = new Uint32Array(buf, at, c.bytes >>> 2);
    at += c.bytes;
  }
  return { hdr, chunks };
}

// WEBDEC_KINDS are the span kinds the frames carry back (web spanKinds).
const WEBDEC_KINDS = { data: 0, "frame-error": 1, "parity-error": 2, gap: 3, sof: 4 };

// webdecFramesBody is the /api/stream/frames.bin body: the session, the
// index decoded through and the frames - each frame's first span (its start
// of frame), its data and error spans, and a loss (losses: indices) as a
// frame of one "loss" span.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
function webdecFramesBody(session, through, frames, losses) {
  const enc = new TextEncoder();
  const items = [];
  let size = 20;
  for (const at of losses || []) {
    items.push({ at, f: null, spans: [], text: new Uint8Array(0) });
    size += 20 + 13;
  }
  for (const f of frames) {
    const spans = [];
    f.spans.forEach((s, k) => {
      const kind = WEBDEC_KINDS[s.kind];
      if (k === 0) spans.push({ i0: s.i0, val: s.val, kind: kind != null ? kind : 5 });
      else if (kind != null && kind !== 4) spans.push({ i0: s.i0, val: s.val, kind });
    });
    const text = f.spans.length && f.spans[0].kind === "sof" ? enc.encode(f.spans[0].text || "").slice(0, 255) : new Uint8Array(0);
    items.push({ at: f.i0, f, spans, text });
    size += 20 + text.length + 13 * spans.length;
  }
  // losses and frames in the stream's order
  items.sort((x, y) => x.at - y.at);
  const buf = new ArrayBuffer(size);
  const dv = new DataView(buf);
  const u8 = new Uint8Array(buf);
  let o = 0;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
  const u32 = (v) => { dv.setUint32(o, v >>> 0, true); o += 4; };
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
  const u64 = (v) => { u32(v % 4294967296); u32(Math.floor(v / 4294967296)); };
  u8.set([0x53, 0x44, 0x46, 0x31], 0);
  o = 4;
  u32(session);
  u64(through);
  u32(items.length);
  for (const it of items) {
    const f = it.f;
    u64(f ? f.i0 : it.at);
    u64(f ? f.i1 : it.at);
    dv.setUint16(o, f ? it.spans.length : 1, true);
    dv.setUint8(o + 2, it.text.length);
    o += 4;
    u8.set(it.text, o);
    o += it.text.length;
    if (!f) {
      u64(it.at);
      u32(0);
      u8[o++] = 6;
      continue;
    }
    for (const s of it.spans) {
      u64(s.i0);
      dv.setInt32(o, s.val | 0, true);
      o += 4;
      u8[o++] = s.kind;
    }
  }
  return buf;
}

if (typeof module !== "undefined" && module.exports) module.exports = { WebDecLine, WebDecStream, WebDecSession, webdecParse, webdecFramesBody, webdecDecoder, webdecInstall };
