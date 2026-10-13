// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-019
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const lib=require('./superres.js'),{decodeUART}=require('./decode.js');
const source=fs.readFileSync('app_superres.js','utf8');
const code=source.slice(source.indexOf('function srEvtDecode('),source.indexOf('function srIngest('));
for(const parity of ['none','even','odd']){
 const value=0x48,spb=20,dt=1/(115200*spb),bits=[1,1,1,1,0];
 for(let i=0;i<8;i++)bits.push((value>>i)&1);
 if(parity!=='none')bits.push(parity==='even'?0:1);
 bits.push(1,1,1,1,1);
 const sig=Float32Array.from(bits.flatMap(b=>Array(spb).fill(b?200:60)));
 const c={...lib,decodeUART,srGateFeed:globalThis.srGateFeed,dcfg:{proto:'uart',baud:115200,bits:8,parity,auto:true},
  sr:{alignCh:0,evtByte:value,stopVal:0},
  // TRLC-LINKS: REQ-SDS-019
  $:id=>({value:id==='srK'?'4':id==='srKernel'?'interp':''}),
  // TRLC-LINKS: REQ-SDS-019
  srUpdateStats(){},
  // TRLC-LINKS: REQ-SDS-019
  srStatus(){},
  // TRLC-LINKS: REQ-SDS-019
  srStop:s=>{throw Error(s)}};
 vm.createContext(c);vm.runInContext(code,c);
 const frame={cols:sig.length,c1:sig,c2:sig,sample_s:dt,tdiv_s:.0001};
 // software never triggers: an untriggered record, or one the FPGA triggered
 // elsewhere, stacks nothing; the FPGA's UART trigger on the byte (its point
 // where the byte's data begins, 5 bits in) seeds the stack
 c.srEvtIngest(frame);
 assert.equal(c.sr.st,undefined,'a record the FPGA did not trigger must not stack');
 c.srEvtIngest({...frame,trigd:true,trigger_kind:'hardware-uart',edge_x:12*spb});
 assert.equal(c.sr.st,undefined,'a trigger away from the byte must not stack it');
 c.srEvtIngest({...frame,trigd:true,trigger_kind:'hardware-uart',edge_x:5*spb});
 assert.ok(c.sr.st,'event reference must seed');
 const result=lib.srResult(c.sr.st),decoded=decodeUART(result.mean,dt/4,{baud:115200,bits:8,parity,guard:4});
 assert.deepEqual(decoded.bytes,[value],parity+' stacked review must retain the complete UART frame');
 assert.equal(decoded.spans[0].kind,'data');
}
console.log('ALL PASS');
