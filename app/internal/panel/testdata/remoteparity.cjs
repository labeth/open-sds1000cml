// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
// remoteparity.cjs - runs the phone remote's hints (internal/web/remote_core.js:
// si, fmtValue, parseNumber, numberHint, parseMatch) on the vectors of
// TestRemoteCoreParity in remote_parity_node_test.go and prints what they say.
// Usage: node remoteparity.cjs <vectors.json> <web dir>.
// TRLC-LINKS: REQ-SDS-220
"use strict";
const fs = require("node:fs");
const path = require("node:path");

const [vecPath, webDir] = process.argv.slice(2);
if (!vecPath || !webDir) {
  console.error("usage: node remoteparity.cjs <vectors.json> <web dir>");
  process.exit(2);
}
const RC = require(path.resolve(webDir, "remote_core.js"));
const v = JSON.parse(fs.readFileSync(vecPath, "utf8"));
const out = {
  si: v.si.map((x) => RC.si(x.v, x.unit)),
  fmt: v.fmt.map((x) => RC.fmtValue(x.row, x.v)),
  parse: v.parse.map((x) => { const p = RC.parseNumber(x.text, x.unit); return p.ok ? { ok: true, value: p.value } : { ok: false, error: p.error }; }),
  range: v.range.map((x) => RC.numberHint(x.row, x.text).error || ""),
  match: v.match.map((x) => { const p = RC.parseMatch(x); return p.ok ? { ok: true, tokens: p.tokens } : { ok: false, error: p.error }; }),
};
process.stdout.write(JSON.stringify(out));
