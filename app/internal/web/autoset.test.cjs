// ENGMODEL-OWNER-UNIT: FU-APP-WEB
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('app_core.js', 'utf8');
const code = source.slice(source.indexOf('async function send(control, value)'));
(async () => {
  let busy = true, polls = 0, writes = [], now = 0;
  const sandbox = {
    autosetBusy: false, autosetDone: Promise.resolve(true), st: {},
    // Existing valid measurements must not end autoset before the device ends it.
    frame: {m1: {vpp: 3.3, freq: 1e6}},
    // TRLC-LINKS: REQ-SDS-204
    $: () => ({classList: {
      // TRLC-LINKS: REQ-SDS-204
      add() {},
      // TRLC-LINKS: REQ-SDS-204
      remove() {}}}),
    // TRLC-LINKS: REQ-SDS-204
    goHome() {},
    // TRLC-LINKS: REQ-SDS-204
    applyStatus() {},
    // TRLC-LINKS: REQ-SDS-204
    Date: {now: () => now},
    // TRLC-LINKS: REQ-SDS-204
    setTimeout: fn => setImmediate(() => { now += 100; fn(); }),
    // TRLC-LINKS: REQ-SDS-204
    awaitFrame: async () => { now += 100; },
    // TRLC-LINKS: REQ-SDS-204
    fetch: async (url, options) => {
      if (url === '/api/status') {
        polls++;
        busy = polls >= 20 && polls < 40;
        // TRLC-LINKS: REQ-SDS-204
        return {json: async () => ({panel: {AutosetBusy: busy}})};
      }
      if (url === '/api/set') writes.push({busy, command: JSON.parse(options.body)});
      // TRLC-LINKS: REQ-SDS-204
      return {ok: true, json: async () => ({ok: true})};
    }
  };
  vm.createContext(sandbox); vm.runInContext(code, sandbox);
  const setting = sandbox.autoset();
  const single = sandbox.send('single', 1);
  await Promise.all([setting, single]);
  assert.equal(writes.length, 1);
  assert.equal(writes[0].busy, false, 'SINGLE was sent while device autoset could overwrite it');
  assert.ok(polls >= 40, 'queued panel event was mistaken for completed autoset');
  assert.equal(sandbox.autosetBusy, false);
  // A device that never finishes must not release a queued acquisition command.
  polls = 0; writes = []; now = 0;
  // TRLC-LINKS: REQ-SDS-204
  sandbox.fetch = async url => ({ok: true,
    // TRLC-LINKS: REQ-SDS-204
    json: async () => url === '/api/status' ? {panel:{AutosetBusy:true}} : {ok:true}});
  const stuck = sandbox.autoset();
  const blocked = sandbox.send('single', 1);
  await stuck;
  assert.equal((await blocked).ok, false);
  // AUTO pressed while a sweep started on the scope runs cancels it: the sweep
  // can end before the first status read shows it busy. Its end (AutosetDone)
  // releases the page's controls, not the page's wait bound.
  polls = 0; writes = []; now = 0;
  let pressed = false;
  // TRLC-LINKS: REQ-SDS-204
  sandbox.fetch = async (url, options) => {
    if (url === '/api/status') {
      polls++;
      // TRLC-LINKS: REQ-SDS-204
      return {json: async () => ({panel: {AutosetBusy: !pressed, AutosetDone: pressed ? 7 : 6}})};
    }
    if (url === '/api/panel') pressed = true;
    if (url === '/api/set') writes.push(JSON.parse(options.body));
    // TRLC-LINKS: REQ-SDS-204
    return {ok: true, json: async () => ({ok: true})};
  };
  const cancel = sandbox.autoset();
  const queued = sandbox.send('single', 1);
  await Promise.all([cancel, queued]);
  assert.equal(writes.length, 1, 'a cancelling AUTO dropped the control sent meanwhile');
  assert.ok(now < 2000, 'a cancelling AUTO waited ' + now + ' ms');
  // AUTO in BUS on a packet-image bus runs 11 to 14 s (two FPGA image loads):
  // the page waits for its end, as the phone does, and its notice follows.
  polls = 0; writes = []; now = 0;
  // TRLC-LINKS: REQ-SDS-204
  sandbox.fetch = async (url, options) => {
    if (url === '/api/status') {
      polls++;
      // TRLC-LINKS: REQ-SDS-204
      return {json: async () => ({panel: {AutosetBusy: polls < 180, AutosetDone: polls < 180 ? 7 : 8}})};
    }
    if (url === '/api/set') writes.push({polls, command: JSON.parse(options.body)});
    // TRLC-LINKS: REQ-SDS-204
    return {ok: true, json: async () => ({ok: true})};
  };
  const long = sandbox.autoset();
  const after = sandbox.send('single', 1);
  await Promise.all([long, after]);
  assert.equal(writes.length, 1, 'the control sent during an 18 s AUTO was dropped');
  assert.ok(writes[0].polls >= 180, 'the page released its controls before the 18 s AUTO ended');
  console.log('ALL PASS');
})().catch(e => { console.error(e); process.exit(1); });
