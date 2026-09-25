// ENGMODEL-OWNER-UNIT: FU-WEB-APP-EYE
// TRLC-LINKS: REQ-SDS-067
// Execute the complete original classic script with isolated DOM/feed stubs.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const source = fs.readFileSync(process.argv[2], 'utf8');
const cases = [];
function fixture() {
  const elements = new Map();
  const state = {armed: true, ch: 1, vpc0: 0.04, st: {records: 9}, incons: 0};
  const calls = {feed: 0, render: 0, status: []};
  const context = vm.createContext({
    ej: state,
    $: id => {
      if (!elements.has(id)) elements.set(id, {value: '1', textContent: '', classList: {add() {}, remove() {}}});
      return elements.get(id);
    },
    ejStatus: text => calls.status.push(text),
    ejFeed: () => { calls.feed++; return calls.disposition || 'locked:20'; },
    ejNew: () => ({records: 0}),
    fetch: () => { throw new Error('unexpected network call'); },
    send: () => { throw new Error('unexpected device command'); },
    setTimeout: () => { throw new Error('unexpected timer'); },
  });
  vm.runInContext(source, context);
  // Rendering is deliberately outside this guard test's scope.
  context.ejRender = () => calls.render++;
  const frame = {c1: new Uint8Array(32), cols: 32, sample_s: 2e-9, vpc1: 0.04};
  return {context, state, calls, frame, elements};
}
function check(name, run) { run(fixture()); cases.push({name, result: 'pass'}); }
check('channel change stops before feeding and preserves accumulated state', f => {
  const old = f.state.st;
  f.context.$('ejCh').value = '2'; f.context.ejIngest(f.frame);
  assert.equal(f.state.armed, false); assert.equal(f.calls.feed, 0); assert.equal(f.state.st, old);
});
check('missing selected signal stops before feed', f => {
  delete f.frame.c1; f.context.ejIngest(f.frame);
  assert.equal(f.state.armed, false); assert.equal(f.calls.feed, 0);
});
check('envelope frame stops before feed', f => {
  f.frame.is_env = true; f.context.ejIngest(f.frame);
  assert.equal(f.state.armed, false); assert.equal(f.calls.feed, 0);
});
check('nonpositive sample period ignores record without stopping', f => {
  f.frame.sample_s = 0; f.context.ejIngest(f.frame);
  assert.equal(f.state.armed, true); assert.equal(f.calls.feed, 0);
});
check('3 percent calibration change stops and retains accumulated state', f => {
  const old = f.state.st; f.frame.vpc1 *= 1.03; f.context.ejIngest(f.frame);
  assert.equal(f.state.armed, false); assert.equal(f.calls.feed, 0); assert.equal(f.state.st, old);
});
check('1 percent calibration change remains accepted', f => {
  f.frame.vpc1 *= 1.01; f.context.ejIngest(f.frame);
  assert.equal(f.state.armed, true); assert.equal(f.calls.feed, 1);
});
check('missing calibration uses fallback and marks units uncalibrated', f => {
  delete f.frame.vpc1; f.context.ejIngest(f.frame);
  assert.equal(f.state.vpc, 1 / 25); assert.equal(f.state.vpcReal, false); assert.equal(f.calls.feed, 1);
});
check('tenth inconsistent UI stops; first nine stay armed', f => {
  f.calls.disposition = 'rejected:ui-inconsistent';
  for (let i = 0; i < 9; i++) { f.context.ejIngest(f.frame); assert.equal(f.state.armed, true); }
  f.context.ejIngest(f.frame); assert.equal(f.state.armed, false); assert.equal(f.state.incons, 10);
});
check('successful lock clears inconsistency streak', f => {
  f.state.incons = 9; f.context.ejIngest(f.frame); assert.equal(f.state.incons, 0);
});
check('optimize calibration transition resets instead of stopping', f => {
  vm.runInContext('ejOptBusy = true', f.context);
  const old = f.state.st; f.frame.vpc1 *= 1.03; f.context.ejIngest(f.frame);
  assert.equal(f.state.armed, true); assert.notEqual(f.state.st, old);
  assert.equal(f.state.st.records, 0); assert.equal(f.calls.feed, 0); assert.equal(f.state.vpc0, f.frame.vpc1);
});
check('optimize inconsistent UI resets instead of accumulating a streak', f => {
  vm.runInContext('ejOptBusy = true', f.context);
  f.calls.disposition = 'rejected:ui-inconsistent'; f.state.incons = 9;
  f.context.ejIngest(f.frame);
  assert.equal(f.state.armed, true); assert.equal(f.state.incons, 0); assert.equal(f.state.st.records, 0);
});
console.log(JSON.stringify({result: 'pass', cases, scope: 'Original ejIngest/ejStop/ejFreshState control flow with feed and rendering stubbed; no DOM layout, real calibration, clock recovery, network, timers or device commands.'}, null, 2));
