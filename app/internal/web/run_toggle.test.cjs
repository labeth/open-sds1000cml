// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-204
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('app_controls.js', 'utf8');
const code = source.slice(source.indexOf('$("run").onclick'), source.indexOf('$("single").onclick'));
(async () => {
  const button = {};
  let running = false;
  const context = {
    $: () => button, autosetBusy: false,
    st: {running:true, single:true}, applyStatus() {},
    send: async (control, value) => {
      if (control === 'runtoggle') running = !running;
      else if (control === 'run') running = !!value;
      else throw Error(control);
      return {ok:true, applied:Number(running)};
    }
  };
  vm.createContext(context);vm.runInContext(code, context);
  await button.onclick();
  assert.equal(running,true,'stale running status must not stop a completed single');
  assert.equal(context.st.single,false);
  await button.onclick();
  assert.equal(running,false,'a second click stops continuous acquisition');
  console.log('ALL PASS');
})().catch(e=>{console.error(e);process.exit(1);});
