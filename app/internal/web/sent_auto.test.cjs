// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Auto-detect reports the SENT tick and nibble count it decoded with, and a
// decode with exactly those settings shows the bytes it announced (bug hunt
// #2: "detected SENT · 28 bytes" over "(no decode)" when the inputs held
// other values).
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const c={};vm.createContext(c);vm.runInContext(fs.readFileSync('decode.js','utf8')+'\n'+fs.readFileSync('decode_sent.js','utf8'),c);
const f=JSON.parse(fs.readFileSync('../decode/testdata/sent_envelope_200us.json','utf8'));
const r=c.autodetect({c1:f.c1,c2:null,dt_s:f.sample_s,cols:f.c1.length},{fmt:'hex'});
assert.equal(r.proto,'sent');
assert.equal(r.cfg.sentNibbles,8);
const shown=c.decodeSENT(f.c1,f.sample_s,{tickNs:r.cfg.sentTickNs,nibbles:r.cfg.sentNibbles,fmt:'hex'});
assert.ok(shown.ok&&shown.bytes.length>0,'decode with the detected settings shows nothing');
assert.equal(shown.bytes.length,r.result.bytes.length);
console.log('SENT auto-detect settings reproduce its decode');
