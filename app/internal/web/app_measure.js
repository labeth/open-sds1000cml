// ENGMODEL-OWNER-UNIT: FU-WEB-APP-MEASURE
// app_measure.js — the MEASURE workspace: every measurement parameter on both
// channels with running statistics (now, mean, min, max, standard deviation,
// count), a reset, and the all-values view (MEASURE again). As on the scope's
// MEASURE, the statistics count the captures while MEASURE is shown, and
// restart when the timebase, a scale, an offset, a probe, a coupling, the
// trigger, the acquisition mode or its count, or the invert changes (not every
// workspace's captures - the FFT's filtered frames are another record - nor
// across an offset or trigger change).
// Classic script; shares app.js globals.
"use strict";

const MEAS_UNIT = { Vpp: "V", Vmax: "V", Vmin: "V", Vmean: "V", Vrms: "V", ACrms: "V", Vtop: "V", Vbase: "V", Vampl: "V",
  Freq: "Hz", Period: "s", Duty: "%", Rise: "s", Fall: "s", "+Width": "s", "-Width": "s", Overshoot: "%", Preshoot: "%" };
const measStats = new Map();
let measSeq = -1, measEdge = 0, measKeyCfg = "", measRenderT = 0;
// the parameters a live envelope frame takes from its edge windows, as its
// result marks them: the edge shape, the timing and the mean of a signal the
// envelope does not resolve
const MEAS_WINDOW = [["edge_shape", new Set(["Rise", "Fall", "Overshoot", "Preshoot"])],
  ["window_timing", new Set(["Freq", "Period", "Duty", "+Width", "-Width"])],
  ["window_mean", new Set(["Vmean", "Vrms", "ACrms"])]];

// measValue returns a parameter's raw value from a frame's measurement block
// (NaN when the capture cannot give it), the numbers fmtMeas formats.
// TRLC-LINKS: REQ-SDS-217
function measValue(key, m) {
  if (!m) return NaN;
  const t = !!m.has_timing;
  switch (key) {
    case "Vpp": return m.vpp; case "Vmax": return m.vmax; case "Vmin": return m.vmin;
    case "Vmean": return m.env_aliased ? NaN : m.vmean; case "Vrms": return m.env_aliased ? NaN : m.vrms;
    case "ACrms": return m.env_aliased ? NaN : m.vrms_ac;
    case "Vtop": return m.vtop; case "Vbase": return m.vbase; case "Vampl": return m.vampl;
    case "Freq": return t ? m.freq : NaN; case "Period": return t ? m.period : NaN; case "Duty": return t ? m.duty : NaN;
    case "Rise": return m.rise_s > 0 ? m.rise_s : NaN; case "Fall": return m.fall_s > 0 ? m.fall_s : NaN;
    case "+Width": return m.pos_width_s > 0 ? m.pos_width_s : NaN; case "-Width": return m.neg_width_s > 0 ? m.neg_width_s : NaN;
    case "Overshoot": return m.edge_shape || t && !m.env_timing ? m.overshoot : NaN;
    case "Preshoot": return m.edge_shape || t && !m.env_timing ? m.preshoot : NaN;
  }
  return NaN;
}

// TRLC-LINKS: REQ-SDS-217
function measReset() { measStats.clear(); measRenderT = 0; measRender(true); }

// MEAS_IDS are the readouts' statistics keys ("1:Vpp"), per channel, made once.
const MEAS_IDS = [1, 2].map((ch) => MEAS_ALL.map((k) => ch + ":" + k));

// measAccumulate adds a new reading's values once: by the frame measured
// (meas_seq: the server reuses a reading for the frames of the next 100 ms
// while running), and the edge shape by its edge-window read (edge_read:
// carried to the frames between reads).
// TRLC-LINKS: REQ-SDS-217
function measAccumulate() {
  if (!frame || frame.sr_view || ws.cur !== "measure") return;
  const seq = frame.meas_seq || frame.seq;
  if (seq === measSeq) return;
  measSeq = seq;
  const freshEdge = !frame.edge_read || frame.edge_read !== measEdge;
  measEdge = frame.edge_read || 0;
  // the scope's statKey: what changes the measured picture
  const cfg = st ? [st.tdiv_s, st.vdiv1, st.vdiv2, st.off1_v, st.off2_v, st.probe1, st.probe2, st.cpl1, st.cpl2,
    st.trig_source, st.trig_rising, st.trig_type, st.trig_code, st.acq_mode, st.avg_count, st.eres_len, st.inv1, st.inv2].join("|") : "";
  if (cfg !== measKeyCfg) { measKeyCfg = cfg; measStats.clear(); }
  for (const ch of [1, 2]) {
    const m = ch === 1 ? frame.m1 : frame.m2;
    if (!m) continue;
    const ids = MEAS_IDS[ch - 1];
    for (let j = 0; j < MEAS_ALL.length; j++) {
      const k = MEAS_ALL[j];
      if (!freshEdge && MEAS_WINDOW.some(([flag, set]) => m[flag] && set.has(k))) continue;
      const id = ids[j];
      let s = measStats.get(id);
      if (!s) { s = new RunStats(); measStats.set(id, s); }
      s.add(measValue(k, m));
    }
  }
}

// One unit per row (picked from the row's largest value), so the columns hold
// bare numbers that line up: "C1 Freq kHz | 25.20 24.61 24.02 25.33 0.362 120".
const MEAS_PFX = [[1e9, "G"], [1e6, "M"], [1e3, "k"], [1, ""], [1e-3, "m"], [1e-6, "µ"], [1e-9, "n"], [1e-12, "p"]];
// TRLC-LINKS: REQ-SDS-217
function measRowUnit(vals, unit) {
  if (unit === "%") return { k: 1, u: "%" };
  let ref = 0;
  for (const v of vals) if (Number.isFinite(v)) ref = Math.max(ref, Math.abs(v));
  const p = ref > 0 ? (MEAS_PFX.find(([f]) => ref >= f * 0.9995) || MEAS_PFX[MEAS_PFX.length - 1]) : [1, ""];
  return { k: p[0], u: p[1] + unit };
}
// TRLC-LINKS: REQ-SDS-217
function measBare(v, k, digits) {
  if (!Number.isFinite(v)) return "—";
  const x = v / k;
  return Math.abs(x) < 1e-9 ? "0" : x.toPrecision(digits || 4);
}

// measRender draws the statistics table (rows grouped by channel) — only while
// MEASURE shows it, and at most four times a second.
// TRLC-LINKS: REQ-SDS-217
function measRender(force) {
  if (ws.cur !== "measure" || ws.measView !== "stats") return;
  const now = performance.now();
  if (!force && now - measRenderT < 250) return;
  measRenderT = now;
  const sel = +$("measCh").value || 0;
  let html = "", total = 0;
  for (const ch of [1, 2]) {
    if (sel && sel !== ch) continue;
    html += `<tr class="grp"><th colspan="7" class="l cc${ch}">CH${ch}${(ch === 1 ? view.c1 : view.c2) ? "" : " (hidden)"}</th></tr>`;
    for (const k of MEAS_ALL) {
      const s = measStats.get(ch + ":" + k);
      const n = s ? s.n : 0;
      total = Math.max(total, n);
      const vals = s ? [s.last, s.mean, s.min, s.max] : [];
      const U = measRowUnit(vals, MEAS_UNIT[k]);
      // TRLC-LINKS: REQ-SDS-217
      const cell = (v, d) => `<td>${n ? measBare(v, U.k, d) : "—"}</td>`;
      html += `<tr data-stat="${ch}:${k}"><th class="l">${k}<span class="unit">${U.u}</span></th>` +
        `<td>${s ? measBare(s.last, U.k) : "—"}</td>` + cell(s && s.mean) + cell(s && s.min) + cell(s && s.max) +
        `<td>${n > 1 ? measBare(s.sdev(), U.k, 3) : "—"}</td><td class="n">${n}</td></tr>`;
    }
  }
  const body = $("statsBody");
  if (body.dataset.sig !== html) { body.dataset.sig = html; body.innerHTML = html; }
  const sub = total ? total + " captures" : "waiting for captures";
  if ($("measSub").textContent !== sub) $("measSub").textContent = sub;
}

// measSetView switches the measurements pane between statistics and all values
// (MEASURE again).
// TRLC-LINKS: REQ-SDS-217
function measSetView(v) {
  ws.measView = v;
  $("measView").value = v;
  $("statsTable").hidden = v !== "stats";
  $("valuesTable").hidden = v !== "values";
  $("measTitle").textContent = v === "stats" ? "Statistics" : "All values";
  $("measCh").hidden = v !== "stats";
  $("statsReset").hidden = v !== "stats";
  if (v === "values") { $("measSub").textContent = "this capture"; updateMeas(); }
  else measRender(true);
}
// TRLC-LINKS: REQ-SDS-217
$("measView").onchange = () => { if (ws.cur !== "measure") setWs("measure"); measSetView($("measView").value); };
// TRLC-LINKS: REQ-SDS-217
$("measCh").onchange = () => measRender(true);
// TRLC-LINKS: REQ-SDS-217
$("statsReset").onclick = () => { measReset(); toast("Statistics restarted"); };
// TRLC-LINKS: REQ-SDS-217
$("quickMore").onclick = () => { setWs("measure"); if (ws.measView !== "stats") measSetView("stats"); };

// TRLC-LINKS: REQ-SDS-217
WS_HOOKS.push({ enter(id) { if (id === "measure") measSetView(ws.measView); } });
WS_TICKS.push(() => { measAccumulate(); measRender(false); });
