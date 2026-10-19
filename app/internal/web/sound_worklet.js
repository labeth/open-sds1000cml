// ENGMODEL-OWNER-UNIT: FU-WEB-SOUND
// sound_worklet.js — the Listen player (ADR-SOUND), an AudioWorkletProcessor:
// a jitter buffer of the scope's stream (stereo, at the stream's rate),
// resampled to the sound card's rate with a rate that follows the buffer's
// fill (the scope's crystal and the sound card's drift apart; without the
// correction the buffer slowly runs dry or overflows), a fade over every
// gap, and a peak limiter at -6 dBFS before the output. It reports its fill,
// the corrections, underruns and overruns, and when a burst after quiet
// left it (the latency probe).
"use strict";

const SW_LIMIT = 0.5; // -6 dBFS
const SW_FADE = 0.004; // seconds of fade over a gap or an underrun

// TRLC-LINKS: REQ-SDS-265
class SoundPlayer extends AudioWorkletProcessor {
  // TRLC-LINKS: REQ-SDS-265
  constructor(opts) {
    super();
    const o = (opts && opts.processorOptions) || {};
    this.srcRate = o.srcRate || 61035.15625;
    this.target = o.targetMs || 120;
    this.size = 1 << 17;
    this.L = new Float32Array(this.size);
    this.R = new Float32Array(this.size);
    this.w = 0; // write count
    this.r = 0; // read position (fractional, in source samples)
    this.primed = false;
    this.ratio = 1; // the rate correction
    this.gain = 1; // the limiter's gain
    this.fade = 0; // 0..1 fade-in progress
    this.gapAt = -1;
    this.under = 0;
    this.over = 0;
    this.quiet = 0;
    this.lastReport = 0;
    this.port.onmessage = (e) => this.onMsg(e.data);
  }

  // TRLC-LINKS: REQ-SDS-265
  onMsg(m) {
    if (m.reset) { this.w = 0; this.r = 0; this.primed = false; return; }
    if (m.targetMs) this.target = m.targetMs;
    if (!m.l) return;
    const n = m.l.length;
    if (m.gap) this.gapAt = this.w; // a discontinuity before these samples
    for (let i = 0; i < n; i++) {
      const k = (this.w + i) & (this.size - 1);
      this.L[k] = m.l[i];
      this.R[k] = m.r ? m.r[i] : m.l[i];
    }
    this.w += n;
    // Overrun: more than four targets queued (a stalled tab caught up) -
    // drop to the target, with a fade.
    const fillMs = (this.w - this.r) / this.srcRate * 1000;
    if (fillMs > 4 * this.target + 200) {
      this.r = this.w - this.target / 1000 * this.srcRate;
      this.fade = 0;
      this.over++;
    }
  }

  // TRLC-LINKS: REQ-SDS-265
  process(inputs, outputs) {
    const out = outputs[0];
    const oL = out[0];
    const oR = out[1] || out[0];
    const n = oL.length;
    const fill = this.w - this.r;
    const targetS = this.target / 1000 * this.srcRate;
    if (!this.primed) {
      oL.fill(0); if (oR !== oL) oR.fill(0);
      if (fill >= targetS) { this.primed = true; this.fade = 0; }
      this.report(fill);
      return true;
    }
    // The rate follows the fill: half a percent at most, slowly.
    const err = (fill - targetS) / targetS;
    const want = 1 + Math.max(-0.005, Math.min(0.005, 0.01 * err));
    this.ratio += (want - this.ratio) * 0.02;
    const step = this.srcRate / sampleRate * this.ratio;
    const fadeStep = 1 / (SW_FADE * sampleRate);
    let burst = false;
    for (let i = 0; i < n; i++) {
      if (this.w - this.r < 2) { // ran dry: silence until refilled
        for (let j = i; j < n; j++) { oL[j] = 0; if (oR !== oL) oR[j] = 0; }
        this.primed = false;
        this.under++;
        break;
      }
      const ri = Math.floor(this.r), f = this.r - ri;
      if (this.gapAt >= 0 && ri + 1 >= this.gapAt && ri < this.gapAt + 1) this.fade = 0;
      const k0 = ri & (this.size - 1), k1 = (ri + 1) & (this.size - 1);
      let l = this.L[k0] + (this.L[k1] - this.L[k0]) * f;
      let rr = this.R[k0] + (this.R[k1] - this.R[k0]) * f;
      if (this.fade < 1) { this.fade = Math.min(1, this.fade + fadeStep); l *= this.fade; rr *= this.fade; }
      // Peak limiter: instant attack, 50 ms release.
      const pk = Math.max(Math.abs(l), Math.abs(rr));
      if (pk * this.gain > SW_LIMIT) this.gain = SW_LIMIT / pk;
      else this.gain = Math.min(1, this.gain + (1 - this.gain) * (1 / (0.05 * sampleRate)));
      l *= this.gain; rr *= this.gain;
      oL[i] = l; if (oR !== oL) oR[i] = rr;
      if (pk > 0.05) { if (this.quiet > 0.2 * sampleRate) burst = true; this.quiet = 0; } else this.quiet++;
      this.r += step;
    }
    if (this.gapAt >= 0 && this.r > this.gapAt + 2) this.gapAt = -1;
    if (burst) this.port.postMessage({ burst: currentTime });
    this.report(fill);
    return true;
  }

  // TRLC-LINKS: REQ-SDS-265
  report(fill) {
    if (currentTime - this.lastReport < 0.25) return;
    this.lastReport = currentTime;
    this.port.postMessage({ fillMs: fill / this.srcRate * 1000, ratio: this.ratio, under: this.under, over: this.over,
      primed: this.primed, gain: this.gain, t: currentTime });
  }
}

registerProcessor("sound-player", SoundPlayer);
