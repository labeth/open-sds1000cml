// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
// layerparity.cjs — runs the web page's layers (layer_tables.js + layers.js,
// loaded into one script scope as the page loads them) on the records of
// vectors.json and prints each record's notes as JSON, for TestJSLayerParity.
// usage: node layerparity.cjs <vectors.json> <web dir>
"use strict";
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const vecs = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const web = process.argv[3];
const src = ["layer_tables.js", "layers.js"].map((f) => fs.readFileSync(path.join(web, f), "utf8")).join("\n;\n") +
  "\n;globalThis.__ly = { layerApply, layersOn, LAYERS };";
const ctx = vm.createContext({ console });
vm.runInContext(src, ctx, { filename: "layers-bundle.js" });
const L = ctx.__ly;
const out = vecs.map((v) => {
  const ps = v.packets.map((p) => ({ bytes: p.bytes, byteErr: p.byteErr, addr: p.addr, hasAddr: p.hasAddr, read: p.read,
    complete: p.complete, err: p.err, i0: 0, i1: 0 }));
  const shown = L.layerApply(v.id, v.proto, ps, { addr: v.addr, dir: v.dir });
  return { notes: shown.map((p) => p.note), errs: shown.map((p) => !!p.noteErr), n: shown.length,
    layers: L.layersOn(v.proto).map((l) => l[0]) };
});
process.stdout.write(JSON.stringify(out));
