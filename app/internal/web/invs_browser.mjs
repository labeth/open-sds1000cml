// Real-browser e2e for the display-level INVS (SCPI Cn:INVS) trace invert:
// with st.inv1/st.inv2 set (the /api/status fields fed by the SCPI shadow),
// the RENDERED Y-T trace mirrors about the display centre — per channel,
// independently — while the other channel stays put. Driven over the REAL
// ui.html + WebGL draw path against httptest+fakeScope (URL in argv[2]); the
// server serves C1 flat at code 178 (above centre) and C2 flat at code 88.
// Output protocol: "SKIP: ..."+exit0 when the browser is absent, else
// "ALL PASS".
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { po, browser } = await openScope(process.argv[2]);
  t.browser = browser;

  // The status poll must deliver the inv fields (present even when false).
  await po.waitFor(() => typeof st !== "undefined" && st && "inv1" in st && "inv2" in st);
  t.ok(true, "/api/status carries inv1/inv2");

  // traceY: set the invert flags, redraw and read back the coverage-weighted
  // centroid row of the pixels lying on the bg→trace-colour axis — ALL inside
  // one synchronous eval, so a status poll can never clobber the st override
  // mid-measurement. The antialiased 1.4 px line lands BETWEEN pixel rows, so
  // no pixel is the pure trace colour: a pixel is classified by projecting
  // (px − bg) onto (colour − bg) — t is its coverage — and rejecting anything
  // off-axis (grid, markers, text, the OTHER trace). The scan skips the
  // left/right 20% (channel/trigger edge markers).
  const traceY = (which, inv1, inv2) => po.eval(([w1, i1, i2]) => {
    st.inv1 = i1; st.inv2 = i2;
    redraw();
    const colHex = w1 === 1 ? C1COL : C2COL;
    const gl = GLR.gl, cv = document.getElementById("scope");
    const w = cv.width, h = cv.height;
    const px = new Uint8Array(w * h * 4);
    gl.readPixels(0, 0, w, h, gl.RGBA, gl.UNSIGNED_BYTE, px);
    const bg = [5, 8, 12]; // --screen #05080c (the glBeginFrame clear colour)
    const d = [parseInt(colHex.slice(1, 3), 16) - bg[0],
               parseInt(colHex.slice(3, 5), 16) - bg[1],
               parseInt(colHex.slice(5, 7), 16) - bg[2]];
    const dd = d[0] * d[0] + d[1] * d[1] + d[2] * d[2];
    let sum = 0, wsum = 0, n = 0;
    const x0 = Math.floor(w * 0.2), x1 = Math.floor(w * 0.8);
    for (let y = 0; y < h; y++)
      for (let x = x0; x < x1; x++) {
        const i = (y * w + x) * 4;
        const p = [px[i] - bg[0], px[i + 1] - bg[1], px[i + 2] - bg[2]];
        const t = (p[0] * d[0] + p[1] * d[1] + p[2] * d[2]) / dd;
        if (t < 0.3 || t > 1.3) continue;
        const rx = p[0] - t * d[0], ry = p[1] - t * d[1], rz = p[2] - t * d[2];
        if (Math.sqrt(rx * rx + ry * ry + rz * rz) > 25) continue; // off-axis: not this trace
        sum += t * y; wsum += t; n++;
      }
    return { y: wsum ? sum / wsum : -1, n, h };
  }, [which, inv1, inv2]);

  const c1Off = await traceY(1, false, false);
  const c2Off = await traceY(2, false, false);
  t.ok(c1Off.n > 100, `C1 trace painted (upright): ${c1Off.n} px`);
  t.ok(c2Off.n > 100, `C2 trace painted (upright): ${c2Off.n} px`);

  // inv1 flips C1 about the centre; C2 must not move.
  const c1On = await traceY(1, true, false);
  const c2Still = await traceY(2, true, false);
  t.ok(c1On.n > 100, `C1 trace painted (inverted): ${c1On.n} px`);
  t.ok(Math.abs(c1On.y - c1Off.y) > 0.25 * c1On.h,
    `C1 moved on invert (${c1Off.y.toFixed(1)} -> ${c1On.y.toFixed(1)} of ${c1On.h})`);
  t.near(c1On.y + c1Off.y, c1On.h, 8,
    "C1 inverted row mirrors the upright row about the display centre");
  t.near(c2Still.y, c2Off.y, 3, "C2 stays put while only C1 is inverted");

  // inv2 flips C2 too.
  const c2On = await traceY(2, true, true);
  t.ok(Math.abs(c2On.y - c2Off.y) > 0.25 * c2On.h,
    `C2 moved on invert (${c2Off.y.toFixed(1)} -> ${c2On.y.toFixed(1)})`);
  t.near(c2On.y + c2Off.y, c2On.h, 8,
    "C2 inverted row mirrors the upright row about the display centre");

  // Back off: both return exactly to the upright rows.
  const c1Back = await traceY(1, false, false);
  t.near(c1Back.y, c1Off.y, 2, "invert off restores the upright C1 trace");
});
