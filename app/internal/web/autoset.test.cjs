// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-204
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
    $: () => ({classList: {add() {}, remove() {}}}),
    goHome() {}, applyStatus() {},
    Date: {now: () => now},
    setTimeout: fn => setImmediate(() => { now += 100; fn(); }),
    awaitFrame: async () => { now += 100; },
    fetch: async (url, options) => {
      if (url === '/api/status') {
        polls++;
        busy = polls >= 20 && polls < 40;
        return {json: async () => ({panel: {AutosetBusy: busy}})};
      }
      if (url === '/api/set') writes.push({busy, command: JSON.parse(options.body)});
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
  sandbox.fetch = async url => ({ok: true, json: async () => url === '/api/status' ? {panel:{AutosetBusy:true}} : {ok:true}});
  const stuck = sandbox.autoset();
  const blocked = sandbox.send('single', 1);
  await stuck;
  assert.equal((await blocked).ok, false);
  console.log('ALL PASS');
})().catch(e => { console.error(e); process.exit(1); });
