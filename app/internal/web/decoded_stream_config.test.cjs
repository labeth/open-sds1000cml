// Stream + trigger takes what the decoder found on auto settings: its
// threshold, a UART's bit rate (snapped to a standard one) and an SPI clock;
// without a decode it still asks for a manual threshold (bug hunt #3: it
// refused the decoder's defaults).
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('app_decoded_capture.js', 'utf8');
const el = {value: '', textContent: '', disabled: false};
const auto = {proto: 1, chA: 0, chB: 0, baud: 0, spiClockHz: 0, bits: 8, parity: 'none', inverted: false, cpol: false, cpha: false,
  msb: true, threshold: 0, haveThr: false, addr: -1, rw: 2, bytes: [0xAA], pattern: []};
const c = {$: () => el, setInterval: () => 0, stParams: () => ({...auto}), dcfg: {result: null}, frame: null, frameDtS: () => 2e-9};
vm.createContext(c);
vm.runInContext(source, c);

assert.throws(() => c.dsConfig(), /Set a manual decode threshold/, 'no decode: a threshold is still needed');

c.dcfg.result = {ok: true, meta: {threshold: 131.6, baud: 115382}};
const u = c.dsConfig();
assert.equal(u.trigger_level, 132, 'the decoder\'s threshold');
assert.equal(u.uart.bit_ticks, Math.round(125e6 / 115200), 'the measured rate, snapped to 115200');
assert.equal(u.uart.pattern, 0xAA);

c.stParams = () => ({...auto, proto: 3, chA: 0, chB: 1, bytes: [0x55]});
c.dcfg.result = {ok: true, meta: {threshold: 120, colsPerClock: 50}};
c.frame = {c1: new Array(1000)};
el.value = '';
const s = c.dsConfig();
assert.equal(s.spi.gap_ticks, Math.round(1.5 * 125e6 / 10e6), 'the SPI clock the decode measured (10 MHz)');

// ARINC 429 at the decode's bit rate; CAN's from its samples per bit (1 us per sample here)
c.stParams = () => ({...auto, proto: 8, bytes: []});
c.dcfg.result = {ok: true, meta: {threshold: 128, bitrate: 100039, samplesPerBit: 10}};
assert.equal(c.dsConfig().packet.ticks, Math.round(125e6 / 100039), 'ARINC at the decoded rate');
c.stParams = () => ({...auto, proto: 6, bytes: [0x55]});
c.dcfg.result = {ok: true, meta: {threshold: 128, samplesPerBit: 2}};
c.frameDtS = () => 1e-6;
assert.equal(c.dsConfig().packet.ticks, Math.round(125e6 * 256 / 500000), 'CAN at 500 kbit/s from 2 samples a bit of 1 us');
c.frameDtS = () => 2e-9;

c.stParams = () => ({...auto, proto: 1, threshold: 90, haveThr: true, baud: 9600});
const m = c.dsConfig();
assert.equal(m.trigger_level, 90, 'a manual threshold wins');
assert.equal(m.uart.bit_ticks, Math.round(125e6 / 9600), 'a set baud wins');
console.log('ALL PASS');
