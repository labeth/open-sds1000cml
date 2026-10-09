// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-072
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const src=fs.readFileSync('app_controls.js','utf8'),els={};const c={$:id=>els[id]||=( {value:'stale'}),document:{activeElement:null}};
vm.createContext(c);vm.runInContext(src.slice(src.indexOf('let qualifierQueue'),src.indexOf('for (const id of ["p-lvl"'))+src.slice(src.indexOf('function updateTriggerQualifiers(')),c);
const q={pulse_lvl:.4,pulse_min_ns:105000,pulse_max_ns:120000,pulse_cond:3,slope_lo:.3,slope_hi:.7,slope_min_ns:1200,slope_max_ns:3400,slope_cond:2,video_std:1,video_line:42,video_neg:false};
c.updateTriggerQualifiers(q);assert.equal(els['p-min'].value,105);assert.equal(els['p-max'].value,120);assert.equal(els['p-lvl'].value,40);assert.equal(els['s-min'].value,1.2);assert.equal(els['v-line'].value,42);assert.equal(els['v-neg'].value,0);
c.document.activeElement=els['p-min'];els['p-min'].value='editing';c.updateTriggerQualifiers(q);assert.equal(els['p-min'].value,'editing');console.log('ALL PASS');

(async()=>{
 const sent=[],waiters=[];c.fetch=(_,req)=>{sent.push(JSON.parse(req.body));return new Promise(r=>waiters.push(r));};
 const first=c.sendParams('pulseparams',{min:100,cond:0});const second=c.sendParams('pulseparams',{min:100,cond:3});
 await Promise.resolve();assert.equal(sent.length,1,'serialize aggregate edits');
 c.document.activeElement=null;els['p-min'].value='pending';c.updateTriggerQualifiers(q);assert.equal(els['p-min'].value,'pending','poll cannot clobber an outstanding edit');
 waiters.shift()({ok:true});await first;await Promise.resolve();assert.equal(sent.length,2);assert.equal(sent[1].cond,3);
 waiters.shift()({ok:true});await second;c.updateTriggerQualifiers(q);assert.equal(els['p-min'].value,105);console.log('QUEUE PASS');
})().catch(e=>{console.error(e);process.exit(1)});
