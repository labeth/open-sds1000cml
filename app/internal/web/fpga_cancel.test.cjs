// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-141
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync('app_superres.js','utf8');
const reset=source.slice(source.indexOf('$("srReset").onclick'),source.indexOf('\n',source.indexOf('$("srReset").onclick')));
const run=source.slice(source.indexOf('$("srFpga").onclick'));
(async()=>{
 const els={};for(const id of ['srReset','srFpga','srCh','srK','srFpgaN','srFpgaMatch','srFpgaPts','srFpgaTol'])els[id]={value:'',checked:false,classList:{add(){},remove(){}}};
 let message,shown=false,signal;
 const c={$:id=>els[id],sr:{fpga:{old:true}},frame:{c1:[20,200],tdiv_s:1},FPGA_WINDOW:32,AbortController,
 srExitView(){},srStop(){},srStatus:s=>message=s,srFpgaLevel:()=>({level:100,hysteresis:10}),srFpgaShow:()=>shown=true,
 fetch:(_url,opts)=>new Promise((_resolve,reject)=>{signal=opts.signal;signal.addEventListener('abort',()=>reject(new Error('aborted')));})};
 vm.createContext(c);vm.runInContext(reset+'\n'+run,c);
 const pending=els.srFpga.onclick();assert.equal(c.sr.fpgaBusy,true);
 els.srReset.onclick();await pending;
 assert.equal(signal.aborted,true);assert.equal(c.sr.fpgaBusy,false);assert.equal(c.sr.fpga,null);assert.equal(message,'idle');assert.equal(shown,false);
 console.log('ALL PASS');
})().catch(e=>{console.error(e);process.exit(1)});
