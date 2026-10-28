// ENGMODEL-OWNER-UNIT: FU-WEB-APP-VISUAL
// app_visual_audio.js — the Visual view's digital audio: S/PDIF, I2S or PDM
// taken from one whole sampled record (/api/record.bin, the timebase stepped
// so the record fits and put back after), decoded (audio_decode.js),
// measured (rate, level, clipping, DC, bits used), drawn as its waveforms and
// played looped. Classic script; shares app.js globals.
"use strict";

// visAudioControls are the side panel while the audio is shown.
// TRLC-LINKS: REQ-SDS-280
function visAudioControls() {
  const a = vis.audio;
  return '<div class="r"><label class="hl"><span class="lbl">source</span><select id="visAKind" aria-label="audio source">' +
    [["auto", "decoded S/PDIF, else I2S"], ["spdif", "S/PDIF (the decoded line)"], ["i2s", "I2S: WS on C1, SD on C2"], ["i2s21", "I2S: WS on C2, SD on C1"], ["lj", "Left-justified: WS C1, SD C2"],
      ["pdm", "PDM: clock C1, data C2"], ["pdm21", "PDM: clock C2, data C1"]].map(([v, t]) => `<option value="${v}"${a.kind === v ? " selected" : ""}>${t}</option>`).join("") +
    '</select></label></div><div class="r"><button type="button" id="visACap" class="xs">Capture</button><button type="button" id="visALive" class="xs tog' + (a.live ? " on" : "") + '">Live</button>' +
    '<button type="button" id="visAPlay" class="xs tog' + (a.play ? " on" : "") + '">Play (loop)</button><label class="row-tight"><span class="lbl">vol</span><input type="range" id="visAGain" min="0" max="1" step="0.05" value="' + a.gain + '" aria-label="audio volume"></label>' +
    '<button type="button" id="visAOff" class="xs">Back to the bus</button></div>' +
    '<div class="r"><label class="hl"><span class="lbl">show</span><select id="visAView" aria-label="the audio drawn as"><option value="wave"' + (a.view !== "spec" ? " selected" : "") + '>waveform</option><option value="spec"' + (a.view === "spec" ? " selected" : "") + '>spectrum</option></select></label></div>' +
    '<div id="visAMsg" class="sub">' + escHTML(a.msg) + '</div><div id="visFig"></div>';
}

// visAudioWire wires the audio's controls (on(id, event, handler)).
// TRLC-LINKS: REQ-SDS-280
function visAudioWire(on) {
  on("visAOff", "onclick", () => visAudioOff());
  on("visAKind", "onchange", () => { vis.audio.kind = $("visAKind").value; vis.audio.sampleS = 0; });
  on("visACap", "onclick", () => visAudioCapture());
  on("visALive", "onclick", () => { vis.audio.live = !vis.audio.live; $("visALive").classList.toggle("on", vis.audio.live); if (vis.audio.live) visAudioCapture(); });
  on("visAPlay", "onclick", () => visAudioPlay(!vis.audio.play));
  on("visAView", "onchange", () => { vis.audio.view = $("visAView").value; visDraw(true); });
  on("visAGain", "oninput", () => { vis.audio.gain = +$("visAGain").value; if (vis.audio.play) vis.audio.play.g.gain.value = vis.audio.gain; });
}

// VIS_RECORD_WORDS is about the words a channel's record holds; a capture
// picks the timebase whose 1.5 screens fit it at the sample interval wanted.
const VIS_RECORD_WORDS = 500000;

// visAudioPlan is the sample interval a source wants: S/PDIF four samples a
// half cell (48 kHz until the first capture measured the rate), I2S four a
// data bit (64 bits a frame), PDM six a clock period (3 MHz).
// TRLC-LINKS: REQ-SDS-280
function visAudioPlan(kind, res) {
  const fs = res && res.ok && res.fs > 0 ? res.fs : 48000;
  if (kind === "spdif") return Math.max(2e-9, 1 / (128 * fs) / 4);
  if (kind.startsWith("pdm")) return Math.max(2e-9, (res && res.ok ? 1 / (fs * PDM_DECIM) : 1 / 3.072e6) / 6);
  const slot = res && res.slot ? res.slot : 32;
  return Math.max(2e-9, 1 / (2 * slot * fs) / 4); // four samples a bit: each read at its centre, the record four times as long as at eight
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
    // the engine takes two words a sample interval wanted over the screen (the decimation steps by
    // two): a screen it cannot hold so is sampled finer, over less time
    for (const t of tds) if (2 * 15 * t / want <= VIS_RECORD_WORDS && t > td) td = t;
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
    res.spec = res.ch.map((x) => audioSpectrum(x, res.fs));
    res.durS = res.ch[0].length / res.fs;
    visAudioMsg(res.kind + " · " + eng(res.fs, "Hz", 4) + " · " + eng(res.durS, "s", 3) + " of audio from a record of " + rec.n + " samples" + (res.still ? " · " + res.still : ""));
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
  if (r.spec && r.spec[0].ok) {
    // TRLC-LINKS: REQ-SDS-280
    const sp = (f) => r.spec.map((s) => (s.ok ? f(s) : "-"));
    // TRLC-LINKS: REQ-SDS-280
    const num = (v, u, d) => Number.isFinite(v) ? v.toFixed(d) + u : "-";
    rows.push(["tone", ...sp((s) => eng(s.toneHz, "Hz", 4) + " " + s.toneDb.toFixed(1) + " dBFS")], ["THD", ...sp((s) => num(s.thdDb, " dB", 1))],
      ["SINAD", ...sp((s) => num(s.sinadDb, " dB", 1))], ["ENOB", ...sp((s) => num(s.enob, " bits", 1))], ["resolution", ...sp((s) => eng(s.bin, "Hz", 3) + " a bin, " + s.n + " points")]);
    if (r.spec[0].why) rows.push([r.spec[0].why, "", ""]);
  }
  if (r.errs) rows.push(["parity errors", r.errs, ""]);
  return '<table class="meas"><tbody>' + rows.map((x, i) => "<tr>" + x.map((c) => i ? `<td>${c}</td>` : `<th>${c}</th>`).join("") + "</tr>").join("") + "</tbody></table>";
}

// visDrawAudio draws the capture's two channels' waveforms (min and max a
// column), the left above the right, full scale the band's edges.
// TRLC-LINKS: REQ-SDS-280
function visDrawAudio(g, W, H) {
  const r = vis.audio.res;
  if (!r || !r.ok) return visBig(g, W, H, vis.audio.busy ? "capturing..." : "no audio yet", vis.audio.msg || "Capture takes a record of the bus");
  if (vis.audio.view === "spec" && r.spec) return visDrawSpectrum(g, W, H, r);
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


// visDrawSpectrum draws each channel's spectrum (dBFS over a logarithmic
// frequency, 20 Hz to half the rate), the left above the right, its tone marked.
// TRLC-LINKS: REQ-SDS-283
function visDrawSpectrum(g, W, H, r) {
  const bh = H / 2, fLo = 20, fHi = r.fs / 2, dbLo = -140, padL = 34 * dpr;
  // TRLC-LINKS: REQ-SDS-283
  const xOf = (f) => padL + (Math.log10(Math.max(fLo, f) / fLo) / Math.log10(fHi / fLo)) * (W - padL - 4 * dpr);
  r.spec.forEach((s, c) => {
    const y0 = c * bh, h = bh - 14 * dpr;
    // TRLC-LINKS: REQ-SDS-283
    const yOf = (db) => y0 + 4 * dpr + (Math.min(0, Math.max(dbLo, db)) / dbLo) * (h - 4 * dpr);
    g.fillStyle = "#0b1016"; g.fillRect(0, y0 + dpr, W, bh - 2 * dpr);
    g.font = Math.round(9 * dpr) + "px monospace"; g.textAlign = "right"; g.textBaseline = "middle";
    for (let db = 0; db >= dbLo; db -= 20) { g.fillStyle = "#1c2631"; g.fillRect(padL, Math.round(yOf(db)), W - padL, Math.max(1, dpr)); g.fillStyle = "#6b7884"; g.fillText(String(db), padL - 3 * dpr, yOf(db)); }
    g.textAlign = "center"; g.textBaseline = "top";
    for (const f of [100, 1000, 10000]) if (f < fHi) { g.fillStyle = "#1c2631"; g.fillRect(Math.round(xOf(f)), y0 + 4 * dpr, Math.max(1, dpr), h - 4 * dpr); g.fillStyle = "#6b7884"; g.fillText(f >= 1000 ? f / 1000 + "k" : String(f), xOf(f), y0 + h + 1 * dpr); }
    if (!s.ok) return;
    g.strokeStyle = c ? "#ffb74d" : "#4fc3f7"; g.lineWidth = 1.2 * dpr; g.beginPath();
    let first = true, lastX = -1, lo = 0, hi = dbLo;
    for (let k = 1; k < s.db.length; k++) { // the most of each column (a log axis packs the high bins)
      const x = Math.round(xOf(k * s.bin)), v = s.db[k];
      if (x === lastX) { hi = Math.max(hi, v); continue; }
      if (lastX >= 0) { if (first) { g.moveTo(lastX, yOf(hi)); first = false; } else g.lineTo(lastX, yOf(hi)); }
      lastX = x; hi = v; lo = v;
    }
    if (lastX >= 0 && !first) g.lineTo(lastX, yOf(hi));
    g.stroke();
    const tx = xOf(s.toneHz);
    g.fillStyle = "#e6edf3"; g.textAlign = "left"; g.textBaseline = "top"; g.font = Math.round(10 * dpr) + "px monospace";
    g.fillText((c ? "right " : "left ") + eng(s.toneHz, "Hz", 4) + "  THD " + (Number.isFinite(s.thdDb) ? s.thdDb.toFixed(1) + " dB" : "-") + "  ENOB " + (Number.isFinite(s.enob) ? s.enob.toFixed(1) : "-") + "  (" + eng(s.bin, "Hz", 3) + " a bin)", padL + 4 * dpr, y0 + 4 * dpr);
    g.fillStyle = "#fff176"; g.fillRect(Math.round(tx), yOf(s.toneDb) - 6 * dpr, Math.max(1, dpr), 6 * dpr);
  });
}
