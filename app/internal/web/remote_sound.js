// ENGMODEL-OWNER-UNIT: FU-WEB-REMOTE
// remote_sound.js — the phone remote's Sound tab (ADR-SOUND): the tuner
// (note, cents needle, Hz), the two meters and Listen, so the phone in a
// pocket is a wireless signal tracer: probe with one hand, listen on
// earbuds. RUN/STOP and SINGLE work as on the scope: stopped, or on a
// trigger, the scope shows a take; the tuner and meters read it and
// Listen plays it (looped). The stream and the player are sound_client.js, the analysis
// sound.js, as on the web page. While the tab shows, the audio stream owns
// the scope's fabric (its triggered captures pause); leaving the tab stops
// it.
"use strict";

(function () {
  const { h, button, toast } = window.RU;
  const c = sndClient(() => false, "phone");
  const S = { el: null, tuner: sndTuner(false), hz: 0, timer: 0, settings: { a4: 440, ch: 0, ref: "dbu" }, built: false, pollAt: 0, running: true, single: false,
    held: { pitchKey: "", pitch: null, meterKey: ["", ""], meter: [null, null] } };
  const E = {};

  // TRLC-LINKS: REQ-SDS-274
  function build(root) {
    root.textContent = "";
    E.note = h("div", { class: "snd-note", text: "--" });
    E.cents = h("div", { class: "snd-cents", text: "— c" });
    E.hz = h("div", { class: "snd-hz", text: "no pitch" });
    E.needle = h("i", { class: "snd-needle" });
    const scale = h("div", { class: "snd-scale", "aria-hidden": "true" }, E.needle, h("b", { class: "snd-zero" }));
    E.chSeg = h("div", { class: "snd-seg", role: "radiogroup", "aria-label": "Tuned channel" });
    for (const [v, t] of [[0, "C1"], [1, "C2"]]) {
      // TRLC-LINKS: REQ-SDS-274
      const b = button({ label: t, cls: "seg-b c" + (v + 1), onTap: () => setCh(v) });
      b.dataset.v = String(v);
      E.chSeg.appendChild(b);
    }
    const tuner = h("section", { class: "snd-card snd-tuner", "aria-label": "Tuner" }, h("div", { class: "snd-row" }, E.note, h("div", {}, E.cents, E.hz)), scale, E.chSeg);
    E.meters = [0, 1].map((ch) => {
      const bar = h("i"), pk = h("b"), val = h("div", { class: "snd-mv" }), clip = h("span", { class: "snd-clip", text: "CLIP", hidden: true });
      const el = h("div", { class: "snd-meter c" + (ch + 1) }, h("div", { class: "snd-mh" }, h("b", { text: "C" + (ch + 1) }), clip), h("div", { class: "snd-bar" }, bar, pk), val);
      return { el, bar, pk, val, clip };
    });
    const meters = h("section", { class: "snd-card", "aria-label": "Meters" }, ...E.meters.map((m) => m.el));
    // TRLC-LINKS: REQ-SDS-265
    E.listen = button({ label: "Listen", sub: "tap to play", cls: "primary snd-listen", aria: "Listen", onTap: () => listen(!c.audio.on) });
    E.src = h("div", { class: "snd-seg", role: "radiogroup", "aria-label": "Listen source" });
    for (const [v, t] of [["c1", "C1"], ["c2", "C2"], ["st", "Stereo"]]) {
      // TRLC-LINKS: REQ-SDS-265
      const b = button({ label: t, cls: "seg-b", onTap: () => { c.audio.src = v; c.audio.filt = null; paintSeg(); } });
      b.dataset.v = v;
      E.src.appendChild(b);
    }
    E.vol = h("input", { type: "range", min: "0", max: "1", step: "0.01", value: "0.7", "aria-label": "Volume", class: "snd-vol" });
    E.vol.addEventListener("input", () => { c.audio.vol = +E.vol.value; });
    E.status = h("div", { class: "snd-status", text: "" });
    // TRLC-LINKS: REQ-SDS-277
    E.takeScope = button({ label: "Take the scope", sub: "its keys took it back", cls: "snd-takescope", aria: "Take the scope", onTap: takeScope });
    E.takeScope.hidden = true;
    const listenCard = h("section", { class: "snd-card", "aria-label": "Listen" }, E.listen, E.src, E.vol, E.status, E.takeScope);
    // TRLC-LINKS: REQ-SDS-275
    E.run = button({ label: "STOP", sub: "freeze a take", cls: "snd-run", aria: "Run or stop", onTap: () => ctl("runtoggle", 0) });
    // TRLC-LINKS: REQ-SDS-275
    E.single = button({ label: "SINGLE", sub: "on the trigger", cls: "snd-single", aria: "Single", onTap: () => ctl("single", 1) });
    E.take = h("div", { class: "snd-status", text: "" });
    const takeCard = h("section", { class: "snd-card snd-takecard", "aria-label": "Take" }, h("div", { class: "snd-row" }, E.run, E.single), E.take);
    root.append(tuner, meters, takeCard, listenCard);
    paintSeg();
    S.built = true;
  }

  // TRLC-LINKS: REQ-SDS-274
  function paintSeg() {
    for (const b of E.chSeg.children) b.setAttribute("aria-checked", String(+b.dataset.v === S.settings.ch));
    for (const b of E.src.children) b.setAttribute("aria-checked", String(b.dataset.v === c.audio.src));
  }

  // TRLC-LINKS: REQ-SDS-274
  async function setCh(v) {
    S.settings.ch = v;
    S.tuner = sndTuner(false);
    paintSeg();
    try { await fetch("/api/sound?who=phone", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ settings: { ch: v } }) }); } catch (e) { /* shown by the stream's status */ }
  }

  // TRLC-LINKS: REQ-SDS-265
  async function listen(on) {
    try { await sndClientListen(c, on); } catch (e) { toast("Listen: " + e.message, "err"); c.audio.on = false; }
    E.listen.classList.toggle("on", c.audio.on);
    if (E.listen.setSub) E.listen.setSub(c.audio.on ? "playing · tap to stop" : "tap to play");
  }

  // takeScope streams SOUND again after the scope's keys took it back.
  // TRLC-LINKS: REQ-SDS-277
  async function takeScope() {
    try { await sndTakeScope(c); } catch (e) { toast("SOUND: " + e.message, "err"); }
  }

  // ctl sends RUN/STOP or SINGLE to the scope.
  // TRLC-LINKS: REQ-SDS-275
  async function ctl(control, value) {
    try { await fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control, value }) }); } catch (e) { toast("SOUND: " + e.message, "err"); }
    S.pollAt = 0;
  }

  // poll follows the scope: its run state, the stream's mode and the take.
  // TRLC-LINKS: REQ-SDS-275
  async function poll() {
    try {
      const [snd, stj] = await Promise.all([fetch("/api/sound?who=phone", { cache: "no-store" }).then((r) => r.json()), fetch("/api/status", { cache: "no-store" }).then((r) => r.json())]);
      if (snd.report) { sndClientSync(c, snd.report.info); c.paused = !!snd.report.paused; }
      S.running = !!stj.running; S.single = !!stj.single;
    } catch (e) { /* the stream's status line says why */ }
  }

  // TAKE_SEQ numbers each take object the tick has read.
  const TAKE_SEQ = new WeakMap();
  let takeSeq = 0;
  // heldKey names the samples sndClientLatest(c, ch, n) gives now: the
  // newest of the stream, or the take's around its focus. Stopped, paused or
  // on a take they hold still between ticks, and the pitch and the meters
  // (the tick's cost) are not worked out again.
  // TRLC-LINKS: REQ-SDS-266, REQ-SDS-267
  function heldKey(n, ch) {
    let where = `l${c.next}:${c.validFrom}`;
    if (c.takeOn && c.take) {
      if (!TAKE_SEQ.has(c.take)) TAKE_SEQ.set(c.take, ++takeSeq);
      where = `t${TAKE_SEQ.get(c.take)}:${c.take.n}:${c.focus}:${c.tview.from}:${c.tview.span}`;
    }
    return where + ":" + n + ":" + ch;
  }

  // TRLC-LINKS: REQ-SDS-266, REQ-SDS-267, REQ-SDS-275
  function tick() {
    if (!S.el || S.el.hidden) return;
    const fs = c.rate, set = S.settings, now = performance.now();
    if (now - S.pollAt > 500) { S.pollAt = now; poll(); }
    c.play.loop = true;
    sndTakeFeed(c);
    E.run.setLabel(S.running ? "STOP" : "RUN");
    E.run.setSub(S.running ? "freeze a take" : "live again");
    E.single.classList.toggle("warn", S.single);
    const t = c.take;
    E.take.textContent = c.mode === "live" ? "live" : c.mode === "stopped" ? (t && c.takeOn ? `stopped · take ${(t.n / t.rate).toFixed(1)} s · Listen loops it` : "stopped") :
      c.mode === "armed" ? "armed: waiting for the FPGA's edge" : c.mode === "triggered" ? "triggered" + (t && c.takeOn ? ` · take ${(t.n / t.rate).toFixed(1)} s` : "") : c.mode;
    if (c.next > 0 || c.takeOn) {
      const n = sndTunerWindow(S.hz, fs), pk = heldKey(n, set.ch);
      let p = S.held.pitch;
      if (S.held.pitchKey !== pk) {
        const x = sndClientLatest(c, set.ch, n);
        p = x.length >= n / 2 ? sndPitch(x, fs) : { ok: false, hz: 0 };
        S.held.pitchKey = pk; S.held.pitch = p;
      }
      S.hz = sndTunerAdd(S.tuner, p);
      c.audio.hz = S.hz;
      if (S.hz > 0) {
        const nt = sndNote(S.hz, set.a4), a = Math.abs(nt.cents);
        E.note.textContent = nt.text;
        E.note.className = "snd-note " + (a < 2 ? "in" : a < 10 ? "near" : "off");
        E.cents.textContent = (nt.cents >= 0 ? "+" : "") + nt.cents.toFixed(1) + " c";
        E.hz.textContent = S.hz.toFixed(S.hz >= 1000 ? 1 : 2) + " Hz";
        E.needle.style.left = (50 + Math.max(-50, Math.min(50, nt.cents))).toFixed(1) + "%";
        E.needle.hidden = false;
      } else {
        E.note.textContent = "--"; E.note.className = "snd-note"; E.cents.textContent = "— c"; E.hz.textContent = "no pitch"; E.needle.hidden = true;
      }
      const ref = sndPreset(set.ref), unit = ref.id === "dbu" ? "dBu" : ref.id === "dbv" ? "dBV" : "dB";
      for (let ch = 0; ch < 2; ch++) {
        const mn = Math.round(c.takeOn ? Math.min(c.tview.span, 4 * fs) : 0.2 * fs), mk = heldKey(mn, ch), mt = E.meters[ch];
        if (S.held.meterKey[ch] !== mk) { S.held.meterKey[ch] = mk; S.held.meter[ch] = sndMeasure(sndClientLatest(c, ch, mn)); }
        const m = S.held.meter[ch];
        const db = sndDB(m.rms, ref.vrms), pk = sndDB(m.peak / Math.SQRT2, ref.vrms);
        // TRLC-LINKS: REQ-SDS-266
        const frac = (v) => Math.max(0, Math.min(1, (v + 60) / 66));
        mt.bar.style.width = (100 * frac(db)).toFixed(1) + "%";
        mt.pk.style.left = (100 * frac(pk)).toFixed(1) + "%";
        mt.val.textContent = (Number.isFinite(db) ? db.toFixed(1) : "—") + " " + unit + "   Vpp " + (m.vpp ? m.vpp.toPrecision(3) : "0") + " V";
        mt.clip.hidden = performance.now() - c.clipAt[ch] > 1500;
      }
    }
    const hd = c.hdr, a = c.audio;
    let st = c.err ? "stream: " + c.err : c.paused ? "paused: the scope is in use" : !hd ? "starting the audio stream…" : !hd.active ? "stream: " + hd.state : `gap-free ${(hd.rate / 1000).toFixed(1)} kSa/s`;
    E.takeScope.hidden = !c.paused;
    if (a.on && !c.paused) st += ` · buffer ${(a.stat.fillMs || 0).toFixed(0)} ms · ${a.stat.under || 0} underruns · ` + sndLatencyText(c);
    E.status.textContent = st;
  }

  // show starts the tab: the scope (opening the tab is acting on it, as on
  // the web page), the scope's SOUND settings, the stream, the tick.
  // TRLC-LINKS: REQ-SDS-274, REQ-SDS-277
  async function show(root) {
    S.el = root;
    if (!S.built) build(root);
    sndTakeScope(c).catch(() => {});
    sndClientStart(c);
    clearInterval(S.timer);
    S.timer = setInterval(tick, 120);
    try {
      const j = await (await fetch("/api/sound?lease=1&who=phone", { cache: "no-store" })).json();
      if (j.report && j.report.settings) { Object.assign(S.settings, j.report.settings); paintSeg(); }
    } catch (e) { /* the status line says why the stream is not there */ }
  }

  // hide stops the stream and Listen.
  // TRLC-LINKS: REQ-SDS-274
  function hide() {
    clearInterval(S.timer);
    const was = c.audio.on;
    sndClientStop(c);
    if (was && E.listen) { E.listen.classList.remove("on"); if (E.listen.setSub) E.listen.setSub("tap to play"); }
  }

  window.RSOUND = { show, hide, client: c, state: S };
})();
