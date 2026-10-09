// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-014
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const text=fs.readFileSync('app_zonemask.js','utf8');
const code=text.slice(text.indexOf('$("zmBuild").onclick'),text.indexOf('\n};',text.indexOf('$("zmBuild").onclick'))+3);
async function run(reject){
 let seq=0,body,message;
 const els={zmBuild:{},zmN:{value:4},zmTolT:{value:0},zmTolV:{value:0},zmCh:{value:0}};
 const c={st:{win_cols:312500,trig_pos_frac:.5},zm:{},$:id=>els[id],zmCplOK:()=>true,zmVctx:()=>null,redraw:()=>{},zmStatus:t=>message=t,
 decodeBinFrame:f=>f,fetch:async(url,opts)=>{
 if(url==='/api/mask'){body=JSON.parse(opts.body);return {ok:true,json:async()=>({ok:!reject,err:'rejected for test'})};}
 return {arrayBuffer:async()=>({seq:++seq,edge_x:2,sample_s:1e-6,tdiv_s:1e-3,cols:4,win_cols:4,c1:[60.25,60.25,190.75,190.75]})};
 }};
 vm.createContext(c);vm.runInContext(code,c);await els.zmBuild.onclick();
 assert.equal(body.win,4);assert.deepEqual(body.lo,[60,60,190,190]);assert.deepEqual(body.hi,[61,61,191,191]);
 if(reject){assert.match(message,/failed.*rejected/);assert.equal(c.zm.mask,undefined);}else{assert.match(message,/mask built from 4/);assert.equal(c.zm.mask.win,4);}
}
(async()=>{await run(false);await run(true);console.log('ALL PASS')})().catch(e=>{console.error(e);process.exit(1)});
