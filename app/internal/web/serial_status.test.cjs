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
