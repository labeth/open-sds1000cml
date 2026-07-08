// app_gl.js — a small 2D renderer built on native WebGL. Every graphics box in
// the UI (the scope, the navigator, the eye/spectrogram/bode cards) is a WebGL
// canvas — no 2D canvas anywhere, so there is exactly ONE viewport and ONE
// coordinate system per box (no 2D↔GL alignment drift). The side panel stays
// HTML. Shares app.js globals.
//
// Design: EVERYTHING is a triangle. Filled rects are 2 tris; lines are quads
// (which also gives real, sub-pixel line widths that gl.LINES can't); text is
// textured quads sampling a glyph atlas; images (eye/waterfall heatmaps) sample
// their own RGBA texture. One shader: solids/text take their colour from the
// vertex and their coverage (alpha) from the atlas; images take their colour
// from the texture. A per-vertex `mode` flag selects between the two, so solids,
// text and images all batch together and flush only when the texture or scissor
// changes — preserving paint order (painter's algorithm) exactly like 2D canvas.
"use strict";

// ---- glyph atlas: baked ONCE into a texture (invisible offscreen raster; not a
// rendering surface, so no alignment concerns). Monospace so layout is trivial.
const GL_GLYPH_W = 8, GL_GLYPH_H = 14, GL_FIRST = 32, GL_LAST = 126;
let glAtlas = null; // { canvas-derived pixels, texW, texH, cols }

function glBuildAtlas() {
  if (glAtlas) return glAtlas;
  const n = GL_LAST - GL_FIRST + 1;
  const cols = 16, rows = Math.ceil((n + 1) / cols); // +1 slot for the white texel
  const cw = GL_GLYPH_W, ch = GL_GLYPH_H;
  const texW = cols * cw, texH = rows * ch;
  const oc = document.createElement("canvas");
  oc.width = texW; oc.height = texH;
  const g = oc.getContext("2d");
  g.clearRect(0, 0, texW, texH);
  // slot 0 = a solid white block (solids/lines sample its centre)
  g.fillStyle = "#fff"; g.fillRect(0, 0, cw, ch);
  g.font = (ch - 3) + "px monospace";
  g.textBaseline = "middle"; g.textAlign = "center";
  g.fillStyle = "#fff";
  for (let i = 0; i < n; i++) {
    const s = i + 1; // glyph slots start after the white block
    const cx = (s % cols) * cw, cy = Math.floor(s / cols) * ch;
    g.fillText(String.fromCharCode(GL_FIRST + i), cx + cw / 2, cy + ch / 2 + 0.5);
  }
  const px = g.getImageData(0, 0, texW, texH).data; // RGBA; solids/text use the alpha
  // white-texel UV (centre of slot 0)
  const whiteU = (cw * 0.5) / texW, whiteV = (ch * 0.5) / texH;
  glAtlas = { px, texW, texH, cols, cw, ch, whiteU, whiteV, first: GL_FIRST };
  return glAtlas;
}

// ---- a renderer bound to one canvas -----------------------------------------
function glRenderer(canvas) {
  let gl;
  try {
    gl = canvas.getContext("webgl", { antialias: true, premultipliedAlpha: false, depth: false, alpha: false });
  } catch (e) { return null; }
  if (!gl) return null;

  const vs =
    "attribute vec2 aPos; attribute vec2 aUV; attribute vec4 aCol; attribute float aMode;" +
    "uniform vec2 uRes;" +
    "varying vec2 vUV; varying vec4 vCol; varying float vMode;" +
    "void main(){ vec2 c=(aPos/uRes)*2.0-1.0; gl_Position=vec4(c.x,-c.y,0.0,1.0); vUV=aUV; vCol=aCol; vMode=aMode; }";
  const fs =
    "precision mediump float; uniform sampler2D uTex;" +
    "varying vec2 vUV; varying vec4 vCol; varying float vMode;" +
    // mode 0 (solids/text): colour = vertex, coverage = texture alpha.
    // mode 1 (images):      colour = texture RGB × vertex, alpha = both.
    "void main(){ vec4 t=texture2D(uTex,vUV); vec3 rgb=vCol.rgb*mix(vec3(1.0),t.rgb,vMode); gl_FragColor=vec4(rgb, vCol.a*t.a); }";

  const atlas = glBuildAtlas();
  const FLOATS = 9;                 // x,y,u,v,r,g,b,a,mode
  let verts = new Float32Array(1 << 16);
  let vN = 0;                       // float count
  let curMode = 0;                  // 0 = solid/text, 1 = image (set inside R.image/R.blit)

  // GL resources — held in closure vars so they can be REBUILT after a context
  // loss (initGL re-runs on webglcontextrestored). Everything downstream reads
  // the current values, so a rebuild is transparent to the draw methods.
  let prog, aPos, aUV, aCol, aMode, uRes, buf, atlasTex, curTex, blitTex;

  function initGL() {
    prog = glCompile(gl, vs, fs);
    if (!prog) return false;
    aPos = gl.getAttribLocation(prog, "aPos");
    aUV = gl.getAttribLocation(prog, "aUV");
    aCol = gl.getAttribLocation(prog, "aCol");
    aMode = gl.getAttribLocation(prog, "aMode");
    uRes = gl.getUniformLocation(prog, "uRes");
    buf = gl.createBuffer();
    atlasTex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, atlasTex);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, atlas.texW, atlas.texH, 0, gl.RGBA, gl.UNSIGNED_BYTE, new Uint8Array(atlas.px));
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.enable(gl.BLEND);
    gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
    curTex = atlasTex;
    blitTex = null;                 // dynamic heatmap texture; lazily (re)created
    return true;
  }
  if (!initGL()) return null;

  const R = {
    gl, w: 0, h: 0,
    lw: 1,                          // line width (device px)
    xf: [1, 0, 0, 1, 0, 0],         // affine 2x3: [a b c d e f]
    stack: [],
    scissor: null,                  // {x,y,w,h} in device px (top-left origin) or null
  };

  function ensure(floats) {
    if (vN + floats <= verts.length) return;
    let cap = verts.length; while (vN + floats > cap) cap <<= 1;
    const nv = new Float32Array(cap); nv.set(verts.subarray(0, vN)); verts = nv;
  }
  function tx(x, y) { const m = R.xf; return [m[0] * x + m[2] * y + m[4], m[1] * x + m[3] * y + m[5]]; }
  function vert(x, y, u, v, c) {
    const p = tx(x, y);
    verts[vN++] = p[0]; verts[vN++] = p[1]; verts[vN++] = u; verts[vN++] = v;
    verts[vN++] = c[0]; verts[vN++] = c[1]; verts[vN++] = c[2]; verts[vN++] = c[3];
    verts[vN++] = curMode;
  }
  // a triangle (untransformed uv), pushing 3 verts
  function tri(x0, y0, x1, y1, x2, y2, u0, v0, u1, v1, u2, v2, c) {
    ensure(FLOATS * 3);
    vert(x0, y0, u0, v0, c); vert(x1, y1, u1, v1, c); vert(x2, y2, u2, v2, c);
  }
  function quad(x0, y0, x1, y1, x2, y2, x3, y3, u0, v0, u1, v1, u2, v2, u3, v3, c) {
    tri(x0, y0, x1, y1, x2, y2, u0, v0, u1, v1, u2, v2, c);
    tri(x0, y0, x2, y2, x3, y3, u0, v0, u2, v2, u3, v3, c);
  }
  function flush() {
    if (vN === 0 || gl.isContextLost()) { vN = 0; return; }
    gl.useProgram(prog);
    gl.uniform2f(uRes, R.w, R.h);
    gl.bindBuffer(gl.ARRAY_BUFFER, buf);
    gl.bufferData(gl.ARRAY_BUFFER, verts.subarray(0, vN), gl.DYNAMIC_DRAW);
    const stride = FLOATS * 4;
    gl.enableVertexAttribArray(aPos); gl.vertexAttribPointer(aPos, 2, gl.FLOAT, false, stride, 0);
    gl.enableVertexAttribArray(aUV); gl.vertexAttribPointer(aUV, 2, gl.FLOAT, false, stride, 8);
    gl.enableVertexAttribArray(aCol); gl.vertexAttribPointer(aCol, 4, gl.FLOAT, false, stride, 16);
    gl.enableVertexAttribArray(aMode); gl.vertexAttribPointer(aMode, 1, gl.FLOAT, false, stride, 32);
    gl.bindTexture(gl.TEXTURE_2D, curTex);
    gl.drawArrays(gl.TRIANGLES, 0, vN / FLOATS);
    vN = 0;
  }
  function useTex(t) { if (t !== curTex) { flush(); curTex = t; } }
  function setScissor(s) {
    flush();
    if (!s) { gl.disable(gl.SCISSOR_TEST); R.scissor = null; return; }
    gl.enable(gl.SCISSOR_TEST);
    gl.scissor(s.x, R.h - (s.y + s.h), s.w, s.h); // GL scissor origin is bottom-left
    R.scissor = s;
  }

  // ---- public API ----
  // The app owns each canvas's backing-store size (scope/nav sized in resize();
  // cards set their own width/height). resize() just syncs the GL viewport.
  R.resize = function () {
    R.w = canvas.width; R.h = canvas.height;
    if (!gl.isContextLost()) gl.viewport(0, 0, R.w, R.h);
  };
  R.lost = function () { return gl.isContextLost(); };
  R.begin = function (bg) {
    R.w = canvas.width; R.h = canvas.height; gl.viewport(0, 0, R.w, R.h);
    R.xf = [1, 0, 0, 1, 0, 0]; R.stack.length = 0; R.lw = 1; setScissor(null);
    const c = bg ? glCol(bg) : [0, 0, 0, 1];
    gl.clearColor(c[0], c[1], c[2], c[3] == null ? 1 : c[3]);
    gl.clear(gl.COLOR_BUFFER_BIT);
    curTex = atlasTex; curMode = 0; vN = 0;
  };
  R.end = function () { flush(); };
  R.save = function () { R.stack.push(R.xf.slice()); R.stack.push(R.lw); };
  R.restore = function () { R.lw = R.stack.pop(); R.xf = R.stack.pop(); };
  R.translate = function (x, y) { const m = R.xf; m[4] += m[0] * x + m[2] * y; m[5] += m[1] * x + m[3] * y; };
  R.rotate = function (r) { const m = R.xf, s = Math.sin(r), c = Math.cos(r); const a = m[0], b = m[1], cc = m[2], d = m[3]; m[0] = a * c + cc * s; m[1] = b * c + d * s; m[2] = a * -s + cc * c; m[3] = b * -s + d * c; };
  R.clip = function (x, y, w, h) { setScissor(w > 0 && h > 0 ? { x, y, w, h } : null); };
  R.unclip = function () { setScissor(null); };

  R.fillRect = function (x, y, w, h, col) {
    useTex(atlasTex);
    const c = glCol(col), u = atlas.whiteU, v = atlas.whiteV;
    quad(x, y, x + w, y, x + w, y + h, x, y + h, u, v, u, v, u, v, u, v, c);
  };
  R.triangle = function (x0, y0, x1, y1, x2, y2, col) {
    useTex(atlasTex);
    const c = glCol(col), u = atlas.whiteU, v = atlas.whiteV;
    tri(x0, y0, x1, y1, x2, y2, u, v, u, v, u, v, c);
  };
  // a stroked segment as a quad of width R.lw
  R.line = function (x0, y0, x1, y1, col, wOpt) {
    useTex(atlasTex);
    const c = glCol(col), u = atlas.whiteU, v = atlas.whiteV;
    let dx = x1 - x0, dy = y1 - y0; const len = Math.hypot(dx, dy) || 1;
    const hw = (wOpt || R.lw) * 0.5, nx = -dy / len * hw, ny = dx / len * hw;
    quad(x0 + nx, y0 + ny, x1 + nx, y1 + ny, x1 - nx, y1 - ny, x0 - nx, y0 - ny, u, v, u, v, u, v, u, v, c);
  };
  R.polyline = function (pts, col, wOpt) { // pts: flat [x,y,x,y,...]; NaN x = pen-up
    for (let i = 2; i < pts.length; i += 2) {
      if (isNaN(pts[i - 2]) || isNaN(pts[i])) continue;
      R.line(pts[i - 2], pts[i - 1], pts[i], pts[i + 1], col, wOpt);
    }
  };
  R.dashedLine = function (x0, y0, x1, y1, col, on, off, wOpt) {
    const len = Math.hypot(x1 - x0, y1 - y0) || 1, ux = (x1 - x0) / len, uy = (y1 - y0) / len;
    let d = 0;
    while (d < len) {
      const a = d, b = Math.min(len, d + on);
      R.line(x0 + ux * a, y0 + uy * a, x0 + ux * b, y0 + uy * b, col, wOpt);
      d += on + off;
    }
  };
  R.strokeRect = function (x, y, w, h, col, wOpt) {
    R.line(x, y, x + w, y, col, wOpt); R.line(x + w, y, x + w, y + h, col, wOpt);
    R.line(x + w, y + h, x, y + h, col, wOpt); R.line(x, y + h, x, y, col, wOpt);
  };
  // text at (x,y) top-left; px = glyph height (device px). align: 'l' 'c' 'r'.
  // Returns the drawn width (device px).
  R.text = function (str, x, y, col, px, align) {
    useTex(atlasTex);
    str = String(str);
    const sc = (px || 12) / atlas.ch, gw = atlas.cw * sc, gh = atlas.ch * sc;
    const total = str.length * gw;
    let ox = x; if (align === "c") ox = x - total / 2; else if (align === "r") ox = x - total;
    const c = glCol(col), tw = atlas.texW, th = atlas.texH;
    for (let i = 0; i < str.length; i++) {
      let cc = str.charCodeAt(i);
      if (cc < atlas.first || cc > GL_LAST) cc = 63; // '?'
      const slot = cc - atlas.first + 1;
      const sx = (slot % atlas.cols) * atlas.cw, sy = Math.floor(slot / atlas.cols) * atlas.ch;
      const u0 = sx / tw, v0 = sy / th, u1 = (sx + atlas.cw) / tw, v1 = (sy + atlas.ch) / th;
      const gx = ox + i * gw;
      quad(gx, y, gx + gw, y, gx + gw, y + gh, gx, y + gh, u0, v0, u1, v0, u1, v1, u0, v1, c);
    }
    return total;
  };
  R.textWidth = function (str, px) { return String(str).length * (atlas.cw * ((px || 12) / atlas.ch)); };

  // draw a pre-uploaded RGBA texture (col optionally tints); mode 1 = use tex RGB
  R.image = function (texObj, dstX, dstY, dstW, dstH, col) {
    useTex(texObj);
    const c = col ? glCol(col) : [1, 1, 1, 1];
    curMode = 1;
    quad(dstX, dstY, dstX + dstW, dstY, dstX + dstW, dstY + dstH, dstX, dstY + dstH, 0, 0, 1, 0, 1, 1, 0, 1, c);
    curMode = 0;
  };
  // blit a raw RGBA buffer (eye persistence, spectrogram waterfall) scaled into a
  // dst rect. Uploads to a single reusable texture and draws it immediately, so
  // it acts like a scaled putImageData/drawImage. smooth = LINEAR upscale.
  R.blit = function (rgba, w, h, dx, dy, dw, dh, smooth) {
    flush();                                   // emit anything queued on the atlas first
    if (!blitTex) blitTex = gl.createTexture();
    gl.bindTexture(gl.TEXTURE_2D, blitTex);
    const filt = smooth ? gl.LINEAR : gl.NEAREST;
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, filt);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, filt);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    // WebGL1 texImage2D wants a Uint8Array, not the Uint8ClampedArray ImageData
    // hands back — wrap as a view (no copy).
    const u8 = rgba instanceof Uint8Array ? rgba : new Uint8Array(rgba.buffer, rgba.byteOffset, rgba.byteLength);
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, w, h, 0, gl.RGBA, gl.UNSIGNED_BYTE, u8);
    curTex = blitTex;
    R.image(blitTex, dx, dy, dw, dh);
    flush();                                   // draw now; texture is reused next blit
    curTex = atlasTex;
  };
  R.gl = gl;

  // ---- context-loss recovery: no 2D fallback exists, so the box must self-heal.
  // On loss we cancel the default (which would forbid restore); on restore we
  // rebuild all GL resources and repaint. Dynamic textures (blitTex) are rebuilt
  // by the next card render.
  canvas.addEventListener("webglcontextlost", function (e) { e.preventDefault(); }, false);
  canvas.addEventListener("webglcontextrestored", function () {
    initGL();
    if (typeof scheduleRender === "function") scheduleRender();
  }, false);

  return R;
}

function glCompile(gl, vsSrc, fsSrc) {
  const sh = (type, src) => { const s = gl.createShader(type); gl.shaderSource(s, src); gl.compileShader(s); return gl.getShaderParameter(s, gl.COMPILE_STATUS) ? s : (gl.deleteShader(s), 0); };
  const v = sh(gl.VERTEX_SHADER, vsSrc), f = sh(gl.FRAGMENT_SHADER, fsSrc);
  if (!v || !f) return 0;
  const p = gl.createProgram(); gl.attachShader(p, v); gl.attachShader(p, f); gl.linkProgram(p);
  return gl.getProgramParameter(p, gl.LINK_STATUS) ? p : 0;
}

// ---- colour parsing → [r,g,b,a] 0..1 (cached) ----
const _glColCache = new Map();
function glCol(css, alpha) {
  if (Array.isArray(css) || css instanceof Float32Array) return css;
  const key = css + "|" + (alpha == null ? "" : alpha);
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
    if (m) { const p = m[1].split(",").map(parseFloat); r = p[0] / 255; g = p[1] / 255; b = p[2] / 255; if (p[3] != null && alpha == null) a = p[3]; }
  }
  c = [r, g, b, a]; _glColCache.set(key, c); return c;
}

// ---- glContext2D: a CanvasRenderingContext2D-shaped facade over a GL renderer.
// Draw code keeps its expressive 2D-canvas form; every call becomes GPU
// triangles. Only the subset the UI actually uses is implemented.
function glContext2D(R, canvasEl) {
  const ctx = {
    canvas: canvasEl,
    fillStyle: "#fff", strokeStyle: "#fff", lineWidth: 1, globalAlpha: 1,
    font: "12px monospace", textAlign: "left", textBaseline: "alphabetic",
    lineJoin: "miter", lineCap: "butt", imageSmoothingEnabled: true,
    _path: [], _dash: [], _sv: [],
    _R: R,
  };
  const A = c => { const g = glCol(c); return ctx.globalAlpha < 1 ? [g[0], g[1], g[2], g[3] * ctx.globalAlpha] : g; };
  const px = () => { const m = /(\d+(?:\.\d+)?)px/.exec(ctx.font); return m ? parseFloat(m[1]) : 12; };
  ctx.beginPath = () => { ctx._path = []; };
  ctx.moveTo = (x, y) => { ctx._path.push([x, y]); };
  ctx.lineTo = (x, y) => { let s = ctx._path[ctx._path.length - 1]; if (!s) { s = []; ctx._path.push(s); } s.push(x, y); };
  ctx.closePath = () => { const s = ctx._path[ctx._path.length - 1]; if (s && s.length >= 2) s.push(s[0], s[1]); };
  ctx.rect = (x, y, w, h) => { ctx._path.push([x, y, x + w, y, x + w, y + h, x, y + h, x, y]); };
  ctx.arc = (x, y, r, a0, a1) => { const seg = Math.max(10, Math.ceil(r) * 2), s = []; for (let i = 0; i <= seg; i++) { const a = a0 + (a1 - a0) * i / seg; s.push(x + Math.cos(a) * r, y + Math.sin(a) * r); } ctx._path.push(s); };
  ctx.stroke = () => {
    const c = A(ctx.strokeStyle), w = ctx.lineWidth;
    for (const s of ctx._path) {
      if (ctx._dash.length >= 2) { for (let i = 2; i < s.length; i += 2) R.dashedLine(s[i - 2], s[i - 1], s[i], s[i + 1], c, ctx._dash[0], ctx._dash[1] || ctx._dash[0], w); }
      else R.polyline(s, c, w);
    }
  };
  ctx.fill = () => { const c = A(ctx.fillStyle); for (const s of ctx._path) glFillFan(R, s, c); };
  ctx.fillRect = (x, y, w, h) => R.fillRect(x, y, w, h, A(ctx.fillStyle));
  ctx.strokeRect = (x, y, w, h) => R.strokeRect(x, y, w, h, A(ctx.strokeStyle), ctx.lineWidth);
  ctx.clearRect = () => { /* whole-frame clear is R.begin(bg); partial clears unused */ };
  ctx.fillText = (t, x, y) => {
    const p = px(), al = ctx.textAlign === "center" ? "c" : (ctx.textAlign === "right" || ctx.textAlign === "end") ? "r" : "l";
    let top = y - p * 0.78;                 // alphabetic baseline
    if (ctx.textBaseline === "middle") top = y - p * 0.5;
    else if (ctx.textBaseline === "top" || ctx.textBaseline === "hanging") top = y;
    else if (ctx.textBaseline === "bottom") top = y - p;
    R.text(t, x, top, A(ctx.fillStyle), p, al);
  };
  ctx.strokeText = ctx.fillText;
  ctx.measureText = t => ({ width: R.textWidth(t, px()) });
  ctx.setLineDash = d => { ctx._dash = d || []; };
  ctx.getLineDash = () => ctx._dash;
  ctx.save = () => { R.save(); ctx._sv.push([ctx.fillStyle, ctx.strokeStyle, ctx.lineWidth, ctx.globalAlpha, ctx.font, ctx.textAlign, ctx.textBaseline, ctx._dash.slice()]); };
  ctx.restore = () => { R.restore(); const s = ctx._sv.pop(); if (s) { ctx.fillStyle = s[0]; ctx.strokeStyle = s[1]; ctx.lineWidth = s[2]; ctx.globalAlpha = s[3]; ctx.font = s[4]; ctx.textAlign = s[5]; ctx.textBaseline = s[6]; ctx._dash = s[7]; } };
  ctx.translate = (x, y) => R.translate(x, y);
  ctx.rotate = r => R.rotate(r);
  ctx.scale = () => { /* unused */ };
  ctx.drawImage = (src, dx, dy, dw, dh) => { if (src && src._glTex) R.image(src._glTex, dx || 0, dy || 0, dw || src.width, dh || src.height); };
  // blit a raw RGBA buffer (heatmaps); the app calls this instead of the old
  // putImageData → temp-canvas → drawImage dance.
  ctx.blit = (rgba, w, h, dx, dy, dw, dh, smooth) => R.blit(rgba, w, h, dx, dy, dw, dh, smooth);
  ctx.clip = () => { /* rect clips are applied via R.clip directly where needed */ };
  return ctx;
}

// triangle-fan fill of a flat point list (convex shapes: rects, circles, arrows)
function glFillFan(R, pts, col) {
  if (pts.length < 6) return;
  for (let i = 4; i < pts.length; i += 2) R.triangle(pts[0], pts[1], pts[i - 2], pts[i - 1], pts[i], pts[i + 1], col);
}

// ---- boot: the scope + navigator become WebGL canvases; `ctx`/`navCtx` (app.js
// globals) are their 2D facades ----
let GLR = null;      // main-scope renderer
let NAVR = null;     // navigator renderer
function glInit() {
  const cv = document.getElementById("scope");
  GLR = glRenderer(cv);
  if (!GLR) { console.error("WebGL unavailable — scope cannot render"); return false; }
  ctx = glContext2D(GLR, cv);        // `ctx` (app.js global) now routes 2D calls to GPU
  const nv = document.getElementById("nav");
  NAVR = glRenderer(nv);
  if (NAVR) navCtx = glContext2D(NAVR, nv);
  return true;
}
function glBeginFrame(bg) { if (GLR && !GLR.lost()) GLR.begin(bg); }
function glEndFrame() { if (GLR) GLR.end(); }

// ---- per-card GL box: the analysis cards (eye/hist/spec/bode/spectrogram, and
// the enlarge modal) each own a WebGL context created lazily on first draw.
// glCardCtx(cv,bg) returns the card's 2D facade with a fresh frame begun (cleared
// to bg); pair every successful call with glCardEnd(cv) to flush. Returns null if
// WebGL is unavailable or the context is currently lost (caller skips the frame).
function glCardCtx(cv, bg) {
  if (!cv) return null;
  let box = cv._glbox;
  if (!box) {
    const R = glRenderer(cv);
    if (!R) return null;
    box = cv._glbox = { R, ctx: glContext2D(R, cv) };
  }
  if (box.R.lost()) return null;
  box.R.begin(bg);
  return box.ctx;
}
function glCardEnd(cv) { const box = cv && cv._glbox; if (box) box.R.end(); }
