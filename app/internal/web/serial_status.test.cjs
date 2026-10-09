const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('app_serialtrig.js', 'utf8');
const status = source.slice(source.indexOf('function stStatus('), source.indexOf('// called by the decode panel'));
const el = {textContent: ''};
const c = {$: () => el, stProtoNum: () => 1, stParams: () => ({}), stConfigError: '', stArmed: () => true, stBandInactive: () => false};
vm.createContext(c); vm.runInContext(status, c);
for (const [backend, label] of [['hardware-uart','hardware UART'],['hardware-i2c','hardware I²C'],['hardware-spi','hardware SPI'],['hardware-can-sequence','hardware CAN sequence'],['hardware-mil1553-sequence','hardware MIL-1553 sequence'],['hardware-usbls','hardware USB LS'],['software','software matching']]) {
  c.st = {serial_backend: backend, serial_matches: 7}; c.stStatus();
  assert.equal(el.textContent, `armed · ${label} · 7 matches`);
}
console.log('ALL PASS');

const paramsContext = {dcfg:{proto:'spi',clk:1,data:2,bits:8,msb:true,auto:false}, $: id=>({value: {stAddr:'',stBytes:'55 AA',stRW:'2',decThr:'128',dsClock:'200000'}[id]})};
vm.createContext(paramsContext);
vm.runInContext(source.slice(0,source.indexOf('let stLastSig')),paramsContext);
assert.equal(paramsContext.stParams().spiClockHz,200000);
assert.equal(paramsContext.stParams().chA,0);
assert.equal(paramsContext.stParams().chB,1);

// A pre-arm poll must not erase pending state; Decode Off cancels an in-flight arm.
(async () => {
  const els = {};
  for (const id of ['stArm','stStats','stAddr','stRW','stBytes','dsClock','decThr']) {
    const classes = new Set();
    els[id] = {value:'',textContent:'',classList:{contains:k=>classes.has(k),add:k=>classes.add(k),remove:k=>classes.delete(k),toggle(k,on){if(on)classes.add(k);else classes.delete(k);}}};
  }
  els.stRW.value='2';
  const sent=[];let tick,release;
  const c={$:id=>els[id],dcfg:{proto:'uart',bits:8,baud:115200,auto:true},st:{serial_mode:0},
    send:async(control,value)=>{sent.push([control,value]);return {ok:true};},
    fetch:async()=>({ok:true,json:async()=>({ok:true})}),setInterval:f=>tick=f};
  vm.createContext(c);vm.runInContext(source,c);
  await els.stArm.onclick();tick();
  assert.ok(els.stArm.classList.contains('on'),'old status must not erase pending arm');
  c.dcfg.proto='off';c.stOnDecodeChange();
  assert.deepEqual(sent.at(-1),['serialmode',0],'Decode Off must disarm before status catches up');
  c.st.serial_mode=1;tick();assert.ok(!els.stArm.classList.contains('on'));
  c.st.serial_mode=0;tick();
  c.dcfg.proto='uart';
  c.fetch=()=>new Promise(r=>release=()=>r({ok:true,json:async()=>({ok:true})}));
  const arming=els.stArm.onclick();c.dcfg.proto='off';c.stOnDecodeChange();release();await arming;
  assert.deepEqual(sent.at(-1),['serialmode',0],'an in-flight config must not rearm after Decode Off');
  console.log('serial mode race PASS');
})().catch(e=>{console.error(e);process.exit(1)});
