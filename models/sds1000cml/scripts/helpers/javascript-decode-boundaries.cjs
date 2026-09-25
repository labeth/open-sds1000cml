// Model characterization for REQ-SDS-018. Preserves observed implementation limits.
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const root = process.argv[2];
const files = ['decode.js','decode_sent.js','decode_canfd.js','decode_arinc429.js','decode_manchester.js','decode_mil1553.js','decode_usbls.js','decode_flexray.js'];
const context = vm.createContext({});
vm.runInContext(files.map(f => fs.readFileSync(path.join(root, 'app/internal/web', f), 'utf8')).join('\n;\n'), context, {timeout: 1000});
const results = [];
function check(name, expression) {
  assert.equal(vm.runInContext(expression, context, {timeout: 1000}), true, name);
  results.push({name, result: 'pass'});
}
check('unsupported_decimal_and_binary_formats_fall_back_to_hex', 'fmtByte(65,"dec") === "41" && fmtByte(65,"bin") === "41"');
check('both_format_uses_middle_dot_and_ascii_masks_wide_words', 'fmtByte(65,"both") === "41·A" && fmtByte(321,"ascii") === "A"');
check('positive_infinite_frame_interval_is_preserved', 'frameDtS({dt_s:Infinity},16) === Infinity && frameSpanS({dt_s:Infinity},16) === Infinity');
check('gap_sentinel_is_not_a_logic_level', 'logicAt({codes:[56,-1,200], n:3, threshold:128},1) === -1');
check('nonfinite_sample_position_returns_zero', 'logicAt({codes:[56,200], n:2, threshold:128},NaN) === 0');
check('missing_c1_dispatch_throws_before_channel_selection', '(() => { try { decode({c2:[56,200]}, {proto:"uart",roles:{rx:2}}); return false; } catch(e) { return e instanceof TypeError; } })()');
console.log(JSON.stringify({result:'pass', cases:results, scope:'Node VM characterization only; no browser or hardware execution.'}));
