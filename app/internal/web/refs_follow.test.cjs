// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-227
// The page follows the scope's references: after this page hides REF B (its
// own request, answered with both slots) the scope showing it again is
// followed, whatever reads of /api/refs were on their way meanwhile.
// argv[2], argv[3]: the page sources (default app_compare.js, app_draw.js).
"use strict";
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const compare = fs.readFileSync(process.argv[2] || 'app_compare.js', 'utf8');
const draw = fs.readFileSync(process.argv[3] || 'app_draw.js', 'utf8');
// refAct, the page's store/show/hide/clear, as app_draw.js has it
const refAct = /async function refAct\([\s\S]*?\n}\n/.exec(draw)[0];

// TRLC-LINKS: REQ-SDS-227
const slot = (show) => ({has: true, show, gen: 7, cols: 4, c1: [128, 128, 128, 128], vdiv: [1, 1], off: [0, 0], tdiv_s: 1e-3, pos_frac: 0.5});
let scope = [{has: false}, slot(true)];
const held = []; // GET /api/refs answers held back, as on a loaded network
let holdGets = false;
// TRLC-LINKS: REQ-SDS-227
const el = () => ({textContent: '', className: '', dataset: {}, classList: {toggle() {}}, onclick: null});
const c = {
  // TRLC-LINKS: REQ-SDS-227
  $: () => el(),
  WS_TICKS: [], WS_HOOKS: [], ws: {cur: 'scope'}, st: null, refs: {A: null, B: null}, performance: {now: () => 0},
  // TRLC-LINKS: REQ-SDS-227
  setInterval: () => 0,
  // TRLC-LINKS: REQ-SDS-227
  updateRefRows: () => {},
  // TRLC-LINKS: REQ-SDS-227
  redraw: () => {},
  // TRLC-LINKS: REQ-SDS-227
  toast: () => {},
  // TRLC-LINKS: REQ-SDS-227
  fetch: (url, opt) => {
    if (opt && opt.method === 'POST') { // the scope acts, then answers with both slots
      const r = JSON.parse(opt.body);
      if (r.op === 'hide' || r.op === 'show') scope[1] = slot(r.op === 'show');
      const body = {ok: true, refs: JSON.parse(JSON.stringify(scope))};
      // TRLC-LINKS: REQ-SDS-227
      return Promise.resolve({json: async () => body});
    }
    const body = {ok: true, refs: JSON.parse(JSON.stringify(scope))};
    // TRLC-LINKS: REQ-SDS-227
    const answer = {json: async () => body};
    if (!holdGets) return Promise.resolve(answer);
    // TRLC-LINKS: REQ-SDS-227
    return new Promise((r) => held.push(() => r(answer)));
  }};
vm.createContext(c);
vm.runInContext(compare + '\n' + refAct, c);
// TRLC-LINKS: REQ-SDS-227
const settle = () => new Promise((r) => setTimeout(r, 5));
// TRLC-LINKS: REQ-SDS-227
const tag = () => ({panel: {Refs: scope.map((s) => ({Has: !!s.has, Show: !!(s.has && s.show), Gen: s.gen || 0}))}});
// TRLC-LINKS: REQ-SDS-227
const shown = () => vm.runInContext('refs.B && refs.B.show', c);

(async () => {
  await settle();
  c.st = tag(); await c.refsFollow(); await settle();
  assert.equal(shown(), true, 'the page shows the scope\'s REF B');

  // Show on the page hides REF B; a status read meanwhile sends a GET that
  // answers after the page's own request did
  holdGets = true;
  const pending = vm.runInContext('refAct("B", "hide")', c);
  c.st = tag(); const following = c.refsFollow(); // tag: hidden
  await pending; await settle();
  assert.equal(shown(), false, 'the page hid REF B');
  holdGets = false; held.splice(0).forEach((f) => f()); await following; await settle();

  // the scope shows REF B again (its row, the LCD, the phone)
  scope[1] = slot(true);
  for (let i = 0; i < 3; i++) { c.st = tag(); await c.refsFollow(); await settle(); }
  assert.equal(shown(), true, 'the scope shows REF B again: the page follows');
  console.log('refs follow PASS');
})().catch((e) => { console.error(e); process.exit(1); });
