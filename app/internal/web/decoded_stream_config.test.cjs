// Stream + trigger takes what the decoder found on auto settings: its
// threshold, a UART's bit rate (snapped to a standard one) and an SPI clock;
// without a decode it still asks for a manual threshold.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('app_decoded_capture.js', 'utf8');
const el = {value: '', textContent: '', disabled: false};
const auto = {proto: 1, chA: 0, chB: 0, baud: 0, spiClockHz: 0, bits: 8, parity: 'none', inverted: false, cpol: false, cpha: false,
  msb: true, threshold: 0, haveThr: false, addr: -1, rw: 2, bytes: [0xAA], pattern: []};
const c = {
  // TRLC-LINKS: REQ-SDS-206
  $: () => el,
  // TRLC-LINKS: REQ-SDS-206
  setInterval: () => 0,
  // TRLC-LINKS: REQ-SDS-206
  stParams: () => ({...auto}),
  dcfg: {result: null}, frame: null,
  // TRLC-LINKS: REQ-SDS-206
  frameDtS: () => 2e-9};
vm.createContext(c);
vm.runInContext(source, c);

assert.throws(() => c.dsConfig(), /Set a manual decode threshold/, 'no decode: a threshold is still needed');

c.dcfg.result = {ok: true, meta: {threshold: 131.6, baud: 115382}};
const u = c.dsConfig();
assert.equal(u.trigger_level, 132, 'the decoder\'s threshold');
assert.equal(u.uart.bit_ticks, Math.round(125e6 / 115200), 'the measured rate, snapped to 115200');
assert.equal(u.uart.pattern, 0xAA);

// TRLC-LINKS: REQ-SDS-206
c.stParams = () => ({...auto, proto: 3, chA: 0, chB: 1, bytes: [0x55]});
c.dcfg.result = {ok: true, meta: {threshold: 120, colsPerClock: 50}};
c.frame = {c1: new Array(1000)};
el.value = '';
const s = c.dsConfig();
assert.equal(s.spi.gap_ticks, Math.round(1.5 * 125e6 / 10e6), 'the SPI clock the decode measured (10 MHz)');

// ARINC 429 at the decode's bit rate; CAN's from its samples per bit (1 us per sample here)
// TRLC-LINKS: REQ-SDS-206
c.stParams = () => ({...auto, proto: 8, bytes: []});
c.dcfg.result = {ok: true, meta: {threshold: 128, bitrate: 100039, samplesPerBit: 10}};
assert.equal(c.dsConfig().packet.ticks, Math.round(125e6 / 100039), 'ARINC at the decoded rate');
// TRLC-LINKS: REQ-SDS-206
c.stParams = () => ({...auto, proto: 6, bytes: [0x55]});
c.dcfg.result = {ok: true, meta: {threshold: 128, samplesPerBit: 2}};
// TRLC-LINKS: REQ-SDS-206
c.frameDtS = () => 1e-6;
assert.equal(c.dsConfig().packet.ticks, Math.round(125e6 * 256 / 500000), 'CAN at 500 kbit/s from 2 samples a bit of 1 us');
// TRLC-LINKS: REQ-SDS-206
c.frameDtS = () => 2e-9;

// The newest frame unread (the BUS entry's detect stepped the timebase): the last decode that read this
// bus gives the figures, with its own sample interval; another line's does not.
// TRLC-LINKS: REQ-SDS-206
c.stParams = () => ({...auto});
// TRLC-LINKS: REQ-SDS-206
c.decKey = () => 'uart,1';
c.dcfg.result = {ok: false, error: 'UART not resolved at this timebase - faster time/div, or SINGLE'};
c.dcfg.good = {key: 'uart,1', r: {ok: true, meta: {threshold: 140.2, samplesPerBit: 4340}}, dt: 2e-9};
// TRLC-LINKS: REQ-SDS-206
c.frameDtS = () => 1e-6; // the unread frame's interval: not the one the figures came from
const g = c.dsConfig();
assert.equal(g.trigger_level, 140, 'the last good decode\'s threshold');
assert.equal(g.uart.bit_ticks, Math.round(125e6 / 115200), 'its rate from its own interval (4340 x 2 ns: 115207 Bd)');
// TRLC-LINKS: REQ-SDS-206
c.decKey = () => 'uart,2';
assert.throws(() => c.dsConfig(), /Set a manual decode threshold/, 'another line\'s decode does not count');
c.dcfg.good = null;
// TRLC-LINKS: REQ-SDS-206
c.frameDtS = () => 2e-9;

// TRLC-LINKS: REQ-SDS-206
c.stParams = () => ({...auto, proto: 1, threshold: 90, haveThr: true, baud: 9600});
const m = c.dsConfig();
assert.equal(m.trigger_level, 90, 'a manual threshold wins');
assert.equal(m.uart.bit_ticks, Math.round(125e6 / 9600), 'a set baud wins');
// Past the general receiver (5 MBd with a transmitter's 2 %), or not 8N1: the
// fast image's receiver on the samples, a bit time in sixteenths of a ns.
// TRLC-LINKS: REQ-SDS-206, REQ-SDS-229
c.stParams = () => ({...auto, proto: 1, threshold: 90, haveThr: true, baud: 62500000});
assert.deepEqual(JSON.parse(JSON.stringify(c.dsConfig().uart)), {enabled: true, channel: 0, inverted: false, bit_ticks: 0, period16: 256, bits: 8, parity: 0, resync: true, pattern: 0xAA, length: 1});
// TRLC-LINKS: REQ-SDS-206, REQ-SDS-229
c.stParams = () => ({...auto, proto: 1, threshold: 90, haveThr: true, baud: 9600, bits: 7, parity: 'even', bytes: [0x41]});
assert.equal(c.dsConfig().uart.period16, Math.round(16e9 / 9600), '7E1 at 9600 Bd: the receiver on the samples');
// TRLC-LINKS: REQ-SDS-206, REQ-SDS-229
c.stParams = () => ({...auto, proto: 1, threshold: 90, haveThr: true, baud: 5000000});
assert.equal(c.dsConfig().uart.bit_ticks, 25, '5 MBd 8N1 stays on the general receiver');
// High-speed I2C (the clock the decode measured: 147 samples of 2 ns) takes the receiver on the samples.
// TRLC-LINKS: REQ-SDS-206, REQ-SDS-229
c.stParams = () => ({...auto, proto: 2, chA: 0, chB: 1, bytes: []});
c.dcfg.result = {ok: true, meta: {threshold: 128, colsPerClock: 147}};
const hs = c.dsConfig().i2c;
assert.equal(hs.samples, true); assert.equal(hs.delay, -23);
c.dcfg.result = {ok: true, meta: {threshold: 128, colsPerClock: 1250}};
assert.equal(c.dsConfig().i2c.samples, undefined, '400 kHz stays on the general receiver');
console.log('ALL PASS');
