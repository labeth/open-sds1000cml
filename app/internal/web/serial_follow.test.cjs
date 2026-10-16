// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-206
// The web follows the scope: with a serial trigger this page armed (owns),
// a clock rate set on the scope (its Clock rate row, AUTO in BUS) is shown
// here and never sent back; a decode change followed from the scope sends
// nothing; a match changed on the scope ends the page's ownership; an edit
// made here reaches the scope once, over the scope's own settings.
// argv[2]: the page source to test (default app_serialtrig.js).
"use strict";
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(process.argv[2] || 'app_serialtrig.js', 'utf8');

let scope = {proto: 3, chA: 1, chB: 0, baud: 115200, spiClockHz: 0, spiDelayNs: 7.5, fine: false, bits: 8, parity: 'none', inverted: false,
  cpol: false, cpha: false, msb: true, ieee: true, threshold: 0, haveThr: false, addr: -1, rw: 2, bytes: null,
  pattern: [{kind: 1, value: 0, mask: 0}, {kind: 2, value: 0, mask: 0}]};
const posts = [], sheets = [];
const els = {};
for (const id of ['stArm', 'stStats', 'stAddr', 'stRW', 'stBytes', 'dsClock', 'decThr']) {
  const classes = new Set();
  els[id] = {id, value: '', placeholder: '', textContent: '', classList: {
    // TRLC-LINKS: REQ-SDS-206
    contains: k => classes.has(k),
    // TRLC-LINKS: REQ-SDS-206
    add: k => classes.add(k),
    // TRLC-LINKS: REQ-SDS-206
    remove: k => classes.delete(k),
    // TRLC-LINKS: REQ-SDS-206
    toggle(k, on) { if (on) classes.add(k); else classes.delete(k); }}};
}
els.stRW.value = '2';
els.dsClock.value = '10400000'; // what the page's field held: Hz, a digit short of 104 MHz
let tick;
const c = {
  // TRLC-LINKS: REQ-SDS-206
  $: id => els[id],
  dcfg: {proto: 'spi', clk: 2, data: 1, bits: 8, msb: true, auto: true, baud: 115200},
  st: {serial_mode: 0}, superseded: false,
  document: {activeElement: null, hidden: false},
  // TRLC-LINKS: REQ-SDS-206
  send: async (control, value) => { if (control === 'serialmode') c.st.serial_mode = value; return {ok: true}; },
  // TRLC-LINKS: REQ-SDS-206
  sheetSet: async (row, value) => { sheets.push([row, value]); if (row === 'bus.spi_clock') scope.spiClockHz = value; return {ok: true}; },
  // TRLC-LINKS: REQ-SDS-206
  fetch: async (url, opt) => {
    if (opt && opt.method === 'POST') { const p = JSON.parse(opt.body); posts.push(p); scope = {...p}; }
    const body = opt && opt.method === 'POST' ? {ok: true} : {ok: true, params: JSON.parse(JSON.stringify(scope)), mode: c.st.serial_mode};
    return {ok: true,
      // TRLC-LINKS: REQ-SDS-206
      json: async () => body};
  },
  // TRLC-LINKS: REQ-SDS-206
  setInterval: f => { tick = f; }};
vm.createContext(c);
vm.runInContext(source, c);
// TRLC-LINKS: REQ-SDS-206
const settle = () => new Promise(r => setTimeout(r, 5));
// TRLC-LINKS: REQ-SDS-206
async function seconds(n) { for (let i = 0; i < n; i++) { tick(); await settle(); } }

(async () => {
  await settle();
  await els.stArm.onclick(); // armed here: the page owns the trigger
  await settle();
  assert.ok(vm.runInContext('stOwn', c), 'arming here owns the trigger');
  assert.equal(posts.length, 1, 'arming sends the trigger once');
  assert.equal(posts[0].spiClockHz, 0, 'arming keeps the scope\'s Clock rate (Auto), not the page field\'s: ' + posts[0].spiClockHz);
  assert.equal(posts[0].spiDelayNs, 7.5, 'arming keeps the scope\'s data delay');

  // the scope's Clock rate row set to 104 MHz, then AUTO back to Auto; the
  // data delay Auto moves, which the page follows (decFollowScope)
  for (const hz of [104e6, 0]) {
    scope.spiClockHz = hz; scope.spiDelayNs += 0.0625;
    c.stOnDecodeChange();
    await seconds(6);
    assert.equal(posts.length, 1, 'a clock rate set on the scope is not sent back: ' + JSON.stringify(posts.slice(1)));
    assert.equal(scope.spiClockHz, hz, 'the scope keeps its clock rate');
    assert.equal(els.dsClock.value, hz ? '104' : '', 'the page shows the scope\'s clock rate (MHz; empty: Auto)');
  }

  // an edit made here reaches the scope once: the clock rate through its row
  els.dsClock.value = '50'; await els.dsClock.onchange(); await seconds(3);
  assert.deepEqual(sheets, [['bus.spi_clock', 50e6]], 'the clock rate typed here goes to the Clock rate row, once');
  assert.equal(posts.length, 1, '…and not as a trigger push');
  // the match typed here: one push, over the scope's settings
  els.stBytes.value = '9F'; els.stBytes.onchange(); await seconds(4);
  assert.equal(posts.length, 2, 'a match typed here is sent once: ' + posts.length);
  assert.deepEqual(posts[1].bytes, [0x9f]);
  assert.equal(posts[1].spiClockHz, 50e6, 'with the scope\'s clock rate');
  assert.equal(posts[1].spiDelayNs, scope.spiDelayNs, 'and its data delay');

  // AUTO in BUS on the scope: a new match ends this page's ownership
  scope = {...scope, bytes: null, pattern: [{kind: 1, value: 0, mask: 0}], spiClockHz: 0};
  await seconds(3);
  assert.ok(!vm.runInContext('stOwn', c), 'a match changed on the scope ends the page\'s ownership');
  assert.equal(els.stBytes.value, 'SOF', 'the page shows the scope\'s match');
  c.stOnDecodeChange(); await seconds(3);
  assert.equal(posts.length, 2, 'and sends nothing after');
  console.log('serial follow PASS');
})().catch(e => { console.error(e); process.exit(1); });
