// app_gl.js — GPU waveform rendering. The 2D canvas rasterises the trace
// polyline on the CPU per-segment, which is the single largest paint cost (a
// deep record or a 1.3M-point super-res stack janks the whole page). This draws
// the traces as WebGL line primitives on a transparent canvas stacked OVER the
// 2D one (the trace is the topmost, most-visible scope layer); the 2D canvas
// keeps the grid, markers, cursors, decode and text. Falls back to the 2D path
// when WebGL is unavailable (glReady stays false). Shares app.js globals.
"use strict";

let gl = null, glReady = false, glProg = 0, glBuf = 0, glLocPos = 0, glLocRes = 0, glLocColor = 0;
const _glColCache = new Map();

function glInit() {
  const cv = document.getElementById("glscope");
  if (!cv) return false;
  try {
    gl = cv.getContext("webgl", { antialias: true, premultipliedAlpha: true, depth: false, preserveDrawingBuffer: true });
  } catch (e) { gl = null; }
  if (!gl) return false;
  // A lost context (GPU reset, headless quirk, resource limit) must silently
  // drop us back to the 2D trace path — never a blank scope.
  cv.addEventListener("webglcontextlost", (e) => { e.preventDefault(); glReady = false; });
  cv.addEventListener("webglcontextrestored", () => { glReady = false; glInit(); });
  const vs = "attribute vec2 p; uniform vec2 res;" +
    "void main(){ vec2 c = (p / res) * 2.0 - 1.0; gl_Position = vec4(c.x, -c.y, 0.0, 1.0); }";
  const fs = "precision mediump float; uniform vec4 col; void main(){ gl_FragColor = col; }";
  // Only use the GPU path when there's REAL hardware acceleration. Software
  // WebGL (SwiftShader / llvmpipe / a blocklisted GPU) rasterises lines on the
  // CPU too — and for a dense screen-filling trace that is SLOWER than the 2D
  // path — so on those renderers we stay on 2D. Missing renderer info → assume
  // hardware (the overwhelmingly common case).
  try {
    const ext = gl.getExtension("WEBGL_debug_renderer_info");
    if (ext) {
      const r = String(gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) || "");
      if (/swiftshader|llvmpipe|software|basic render|microsoft basic/i.test(r)) { gl = null; return false; }
    }
  } catch (e) { /* keep GL */ }
  const prog = glLink(vs, fs);
  if (!prog) { gl = null; return false; }
  glProg = prog;
  glBuf = gl.createBuffer();
  glLocPos = gl.getAttribLocation(glProg, "p");
  glLocRes = gl.getUniformLocation(glProg, "res");
  glLocColor = gl.getUniformLocation(glProg, "col");
  gl.enable(gl.BLEND);
  gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
  glReady = true;
  return true;
}

function glLink(vsSrc, fsSrc) {
  const sh = (type, src) => {
    const s = gl.createShader(type);
    gl.shaderSource(s, src); gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) { gl.deleteShader(s); return 0; }
    return s;
  };
  const v = sh(gl.VERTEX_SHADER, vsSrc), f = sh(gl.FRAGMENT_SHADER, fsSrc);
  if (!v || !f) return 0;
  const p = gl.createProgram();
  gl.attachShader(p, v); gl.attachShader(p, f); gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) return 0;
  return p;
}

// keep the GL backing store in lockstep with the 2D canvas (CW/CH are device px)
function glResize() {
  if (!glReady) return;
  const cv = document.getElementById("glscope");
  if (cv.width !== CW || cv.height !== CH) { cv.width = CW; cv.height = CH; }
  gl.viewport(0, 0, CW, CH);
}

function glClear() {
  if (!glReady) return;
  if (gl.isContextLost()) { glReady = false; return; } // self-heal → 2D fallback this frame
  glResize();
  gl.clearColor(0, 0, 0, 0);
  gl.clear(gl.COLOR_BUFFER_BIT);
}

// glColor parses a CSS colour (hex or rgb[a]) into premultiplied [r,g,b,a] 0..1.
function glColor(css, alpha) {
  const key = css + "|" + (alpha == null ? 1 : alpha);
  let c = _glColCache.get(key);
  if (c) return c;
  let r = 1, g = 1, b = 1, a = alpha == null ? 1 : alpha;
  const s = String(css).trim();
  if (s[0] === "#") {
    const h = s.slice(1);
    if (h.length === 3) { r = parseInt(h[0] + h[0], 16); g = parseInt(h[1] + h[1], 16); b = parseInt(h[2] + h[2], 16); }
    else { r = parseInt(h.slice(0, 2), 16); g = parseInt(h.slice(2, 4), 16); b = parseInt(h.slice(4, 6), 16); }
    r /= 255; g /= 255; b /= 255;
  } else {
    const m = s.match(/rgba?\(([^)]+)\)/);
    if (m) { const p = m[1].split(",").map(parseFloat); r = p[0] / 255; g = p[1] / 255; b = p[2] / 255; if (p[3] != null) a *= p[3]; }
  }
  c = new Float32Array([r * a, g * a, b * a, a]); // premultiplied
  _glColCache.set(key, c);
  return c;
}

// glLines uploads pixel-space vertex PAIRS (gl.LINES) and draws them in one call.
function glLines(verts, rgba) {
  if (!glReady || !verts || verts.length < 4) return;
  gl.useProgram(glProg);
  gl.uniform2f(glLocRes, CW, CH);
  gl.uniform4fv(glLocColor, rgba);
  gl.bindBuffer(gl.ARRAY_BUFFER, glBuf);
  gl.bufferData(gl.ARRAY_BUFFER, verts, gl.DYNAMIC_DRAW);
  gl.enableVertexAttribArray(glLocPos);
  gl.vertexAttribPointer(glLocPos, 2, gl.FLOAT, false, 0, 0);
  gl.lineWidth(1); // >1 is unsupported on most GPUs; 1 device-px is a crisp trace
  gl.drawArrays(gl.LINES, 0, verts.length >> 1);
}

// glTraceVerts turns a channel's code array into gl.LINES vertex pairs, mirroring
// drawTrace's geometry: the per-pixel min/max envelope for dense records, else a
// point-per-sample polyline, with pen-ups (gaps) at -1/off-screen samples.
function glTraceVerts(cols, zoom) {
  if (!cols || !cols.length) return null;
  const n = cols.length, r = winRange(n), iLo = r[0], iHi = r[1];
  const out = [];
  let px = -1, py = 0;
  const seg = (x, y) => { if (px >= 0) out.push(px, py, x, y); px = x; py = y; };
  const lift = () => { px = -1; };
  if (iHi - iLo > 4 * CW) {
    for (let X = 0; X < CW; X++) {
      const a = Math.max(iLo, Math.ceil(fracForX(X) * (n - 1)));
      const b = Math.min(iHi, Math.ceil(fracForX(X + 1) * (n - 1)) - 1);
      let lo = Infinity, hi = -Infinity;
      for (let i = a; i <= b; i++) { const v = cols[i]; if (v < 0) continue; if (v < lo) lo = v; if (v > hi) hi = v; }
      if (hi < lo) { lift(); continue; }
      const yhi = yFor(hi, zoom), ylo = yFor(lo, zoom);
      seg(X, yhi);      // connect from the previous column's top
      out.push(X, yhi, X, ylo); // the vertical min→max bar at this column
      py = ylo;
    }
  } else {
    for (let i = iLo; i <= iHi; i++) {
      const v = cols[i];
      if (v < 0) { lift(); continue; }
      const x = xForCol(i, n), y = yFor(v, zoom);
      if (y < -4 || y > CH + 4) { lift(); continue; }
      seg(x, y);
    }
  }
  return out.length >= 4 ? new Float32Array(out) : null;
}
