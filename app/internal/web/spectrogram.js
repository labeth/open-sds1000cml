// Spectrogram ("FFT over time") waterfall for the web. Each frame's Hann-
// windowed magnitude spectrum (peaks.js spectrum()) becomes a colour-mapped
// ROW; rows scroll down (newest on top). Colour parity with the Go LCD heat().
// Shared helpers exported for a node parity test.

// sgHeat maps t∈[0,1] to an inferno-like [r,g,b] ramp — monotone in brightness
// so higher dB always reads hotter. Identical stops to the Go heat().
function sgHeat(t) {
  if (!(t > 0)) return [0, 0, 0]; // NaN or <=0 → black (total, matches Go heat())
  if (t >= 1) return [255, 255, 255];
  const stops = [[0, 0, 0], [40, 0, 90], [130, 20, 90], [200, 50, 20], [240, 150, 10], [250, 220, 80], [255, 255, 255]];
  const s = t * 6;
  let i = Math.floor(s);
  if (i < 0) i = 0; else if (i > 5) i = 5; // keep the stops[] index in range
  const f = s - i;
  const a = stops[i], b = stops[i + 1];
  return [Math.round(a[0] + (b[0] - a[0]) * f), Math.round(a[1] + (b[1] - a[1]) * f), Math.round(a[2] + (b[2] - a[2]) * f)];
}

// sgNew allocates a w×h waterfall (w = frequency columns, h = time rows).
function sgNew(w, h) {
  return { w, h, data: new Uint8ClampedArray(w * h * 4), floorDb: -60, rows: 0, nyq: 0 };
}

// sgPushRow scrolls the image down one row and paints the newest spectrum on
// top. mags/half/peak come from peaks.js spectrum(); nyq is its Nyquist (Hz).
function sgPushRow(sg, mags, half, peak, nyq) {
  const { w, h, data } = sg;
  if (!(half > 0) || !(peak > 0)) return;
  data.copyWithin(w * 4, 0, w * 4 * (h - 1)); // scroll down 1 row
  const floor = sg.floorDb < 0 ? sg.floorDb : -60; // NaN/>=0 → Inf/NaN paint
  const invFloor = 1 / (-floor);
  for (let x = 0; x < w; x++) {
    const k = Math.min(half - 1, Math.floor(x * half / w));
    const db = 20 * Math.log10(mags[k] / peak + 1e-12);
    const t = Math.max(0, Math.min(1, 1 + db * invFloor));
    const [r, g, b] = sgHeat(t);
    const o = x * 4;
    data[o] = r; data[o + 1] = g; data[o + 2] = b; data[o + 3] = 255;
  }
  if (nyq) sg.nyq = nyq;
  if (sg.rows < h) sg.rows++;
}

function sgClear(sg) { sg.data.fill(0); sg.rows = 0; }

// sgBlit draws the waterfall onto a GL 2D-facade context sized cw×ch, plus a
// frequency axis and a dB colour key. The caller clears the background (via
// R.begin) and flushes (R.end); the waterfall itself is a texture blit.
function sgBlit(g, cw, ch, sg, textColor) {
  if (!sg || sg.rows === 0) {
    g.fillStyle = textColor || "#9ab"; g.textAlign = "center"; g.font = "11px system-ui";
    g.fillText("FFT over time — needs a triggered signal", cw / 2, ch / 2);
    return;
  }
  // the heatmap occupies the top (ch-16) px; the bottom 16 px carry the axis
  const plotH = ch - 16;
  // upload the waterfall RGBA straight to a texture, scaled to the plot area
  g.blit(sg.data, sg.w, sg.h, 0, 0, cw, plotH, false);
  // frequency axis
  g.fillStyle = textColor || "#9ab"; g.font = "10px system-ui"; g.textAlign = "center"; g.textBaseline = "top";
  for (let i = 0; i <= 4; i++) {
    const x = i * cw / 4;
    const f = sg.nyq * i / 4;
    g.fillText(f >= 1e6 ? (f / 1e6).toPrecision(3).replace(/\.?0+$/, "") + "M" : (f / 1e3).toFixed(0) + "k", Math.min(cw - 14, Math.max(14, x)), plotH + 3);
  }
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { sgHeat, sgNew, sgPushRow, sgClear };
}
