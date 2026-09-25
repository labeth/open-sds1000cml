// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
// Live general-image integration: AU I2C/SPI on C1/A2 and C2/A11; UART on C2/A11.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-033
import { mkdirSync, writeFileSync, openSync, writeSync, closeSync, readFileSync } from 'node:fs';
import {createContext,runInContext} from 'node:vm';
import { get } from 'node:http';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
const {decodeI2C,decodeSPI,decodeUART}=createRequire(import.meta.url)('../../app/internal/web/decode.js');
const {decodeSENT}=createRequire(import.meta.url)('../../app/internal/web/decode_sent.js');
const milContext=createContext({});
for(const file of ['decode.js','decode_manchester.js','decode_mil1553.js','decode_usbls.js'])runInContext(readFileSync(new URL('../../app/internal/web/'+file,import.meta.url),'utf8'),milContext);
const decodeMIL1553=milContext.decodeMIL1553,decodeUSBLS=milContext.decodeUSBLS;
const protocol=process.env.DECODED_PROTOCOL || 'i2c';
if(!['i2c','spi','uart','sent','mil1553','usbls'].includes(protocol))throw Error('unsupported live protocol fixture');
const protocolID=protocol==='usbls'?9:protocol==='mil1553'?7:protocol==='sent'?5:protocol==='uart'?1:protocol==='spi'?3:2;
const expected=protocol==='sent'?[0,1,2,5,10,3,4]:protocol==='i2c'?[0x48,0x55,0xaa,0x0f,0xf0]:[0x48,0x69,0x20,0x55,0xaa,0x0f,0xf0,0x0a];
const matchBytes=protocol==='usbls'?[0xff,0x55]:protocol==='mil1553'?[0xa55a]:protocol==='sent'?[5,10]:protocol==='i2c'?[0x55,0xaa]:[0x48,0x69];
const base=process.env.SCOPE_URL || 'http://192.168.1.209:8080';
const out=process.argv[2];
if(!out)throw Error('output directory required');
mkdirSync(out,{recursive:true});
const results:any[]=[];
// TRLC-LINKS: REQ-SDS-013
async function request(path:string,body?:unknown){
 const r=await fetch(base+path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(20000)});
 if(!r.ok)throw Error(`${path}: ${r.status} ${await r.text()}`);
 return r;
}
// TRLC-LINKS: REQ-SDS-013
async function pause(ms:number){await new Promise(resolve=>setTimeout(resolve,ms));}
// TRLC-LINKS: REQ-SDS-013
function saveEvidence(){
 const summaries=results.map(({events,...result},index)=>{
  if(!events)return result;
  const file=`events-${index}.json`,fd=openSync(out+'/'+file,'w');
  try{
   writeSync(fd,'[');
   for(let offset=0;offset<events.length;offset+=1024){
    if(offset)writeSync(fd,',');
    writeSync(fd,JSON.stringify(events.slice(offset,offset+1024)).slice(1,-1));
   }
   writeSync(fd,']');
  }finally{closeSync(fd);}
  return {...result,eventCount:events.length,eventsFile:file};
 });
 writeFileSync(out+'/decoded-capture.json',JSON.stringify({protocol,initial,results:summaries},null,2));
}
// TRLC-LINKS: REQ-SDS-013
async function stream(query:string,signal:AbortSignal,events:any[]){
 const response:any=await new Promise((resolve,reject)=>{get(base+'/api/decoded/events.bin?'+query,{signal},resolve).on('error',reject);});
 if(response.statusCode!==200)throw Error(`decoded HTTP ${response.statusCode}`);
 let pending=Buffer.alloc(0),sequence=0;
 for await(const value of response){
  pending=Buffer.concat([pending,Buffer.from(value)]);
  while(pending.length>=32){
   const b=pending.subarray(0,32);pending=pending.subarray(32);
   if(b[0]!==1 || b.readUInt32LE(8)!==sequence++)throw Error('decoded framing or sequence discontinuity');
   const event={kind:b[1],protocol:b[2],flags:b[3],epoch:b.readUInt32LE(4),sequence:b.readUInt32LE(8),sample:b.readBigUInt64LE(12).toString(),value:b.readUInt32LE(24),count:b.readUInt32LE(28)};
   if(event.epoch!==Number(new URLSearchParams(query).get('epoch')))throw Error('wrong event epoch');
   events.push(event);
  }
 }
 throw Error(response.trailers['x-decoded-error'] || 'unexpected decoded stream EOF');
}
const initial=await(await request('/api/status')).json();
try{
 for(const match of [false,true]){
  let id:any;const abort=new AbortController(),events:any[]=[];let streamError='';let reading:Promise<void>|undefined;
  const result:any={match,events};results.push(result);
  try{
   const decoder=protocol==='usbls'
    ? {usbls:{enabled:true,channel:0,bit_ticks_q8:21333,pattern:match?0xff55:0xdead,length:2}}
    : protocol==='mil1553'
    ? {mil1553:{enabled:true,channel:0,inverted:true,bit_ticks:125,pattern:match?0xa55a:0xdead,match_any:false}}
    : protocol==='sent'
    ? {sent:{enabled:true,channel:0,inverted:true,tick_ticks:375,nibbles:8,pattern:match?0x050a:0x0e0f,length:2}}
    : protocol==='uart'
    ? {uart:{enabled:true,channel:1,inverted:true,bit_ticks:1085,pattern:match?0x4869:0xdead,length:2}}
    : protocol==='spi'
    ? {spi:{enabled:true,clock_channel:0,inverted:true,cpol:false,cpha:false,msb:true,gap_ticks:938,pattern:match?0x4869:0xdead,length:2}}
    : {i2c:{enabled:true,clock_channel:0,inverted:true,address:match?0x24:0x25,direction:0,pattern:0x55aa,length:2}};
   id=await(await request('/api/decoded/start',{source:0,normal:true,pre_words:262144,post_words:262144,trigger_level:103,trigger_hysteresis:4,...decoder})).json();
   result.identity=id;const query=`epoch=${id.epoch}&record=${id.record}`;
   reading=stream(query,abort.signal,events).catch(error=>{if(!abort.signal.aborted){streamError=String(error);result.streamError=streamError;}});
   await pause(Number(process.env.DECODED_DWELL_MS || 1200));
   const metadata=await(await request('/api/decoded/status?'+query)).json();result.metadata=metadata;
   if(metadata.data_fault || metadata.event_epoch!==id.epoch || metadata.record_id!==id.record)throw Error('invalid capture identity or data fault');
   if(match ? (!metadata.frozen || !metadata.ready || !metadata.triggered) : (!metadata.running || metadata.triggered))throw Error('incorrect trigger predicate result');
   if(!events.some(e=>e.kind===2 && e.protocol===protocolID && e.value===(protocol==='mil1553'?0xa55a:protocol==='sent'?5:0x55)))throw Error('no expected protocol data in live transcript');
   const count=events.length;
   if(match){
    if(!metadata.has_sample_timeline)throw Error('frozen raw record has no ADC sample timeline');
    if(metadata.sample_last-metadata.sample_first+1!==metadata.words*2)throw Error('invalid retained ADC sample range');
    const path='/api/decoded/record.bin?'+query+'&offset=0&words='+metadata.words;
    const first=Buffer.from(await(await request(path)).arrayBuffer());
    const second=Buffer.from(await(await request(path)).arrayBuffer());
    if(first.length!==metadata.words*4 || !first.equals(second))throw Error('retained SRAM changed or wrong length');
    writeFileSync(out+'/retained.bin',first);
    const c1=new Uint8Array(first.length/2),c2=new Uint8Array(c1.length);
    for(let i=0;i<c1.length;i++){c1[i]=first[2*i];c2[i]=first[2*i+1];}
    const decoded=protocol==='usbls'
     ? decodeUSBLS(c1,2e-9,{threshold:103,bitrate:1500000})
     : protocol==='mil1553'
     ? decodeMIL1553(c1,2e-9,{inverted:true,threshold:103,bitrate:1000000})
     : protocol==='sent'
     ? decodeSENT(c1,2e-9,{inverted:true,threshold:103,tickNs:3000,nibbles:8})
     : protocol==='uart'
     ? decodeUART(c2,2e-9,{inverted:true,threshold:103,baud:115200,bits:8,parity:'none',bitOrder:'lsb'})
     : (protocol==='spi'?decodeSPI:decodeI2C)(c1,c2,2e-9,{inverted:true,threshold:103,cpol:false,cpha:false,bitOrder:'msb'});
    if(!decoded.ok || !decoded.bytes.some((v:number,i:number)=>matchBytes.every((b,j)=>decoded.bytes[i+j]===b)))throw Error("retained raw waveform lacks the expected protocol message");
    result.rawDecode={bytes:decoded.bytes,text:decoded.text};
    const triggerSample=metadata.trigger_index*2;
    // UART accepts at the stop-bit midpoint; its display span ends at stop-bit start.
    const decisionOffset=protocol==='mil1553'?0.75*500+1:protocol==='uart'?0.5*500e6/115200:0;
    let endings=decoded.spans.filter((span:any,index:number)=>(protocol==='mil1553' ? span.kind==='data' && span.val===0xa55a && decoded.spans[index+1]?.kind!=='frame-error' : protocol==='sent' ? span.kind==='crc' && span.val===1 : span.kind==="data" && span.val===matchBytes[1])).map((span:any)=>triggerSample-span.i1-decisionOffset);
    if(protocol==='usbls'){
     let frame:number[]=[];const decisions:number[]=[];
     for(const e of events){if(e.kind===1)frame=[];else if(e.kind===2)frame.push(e.value);else if(e.kind===3 && frame.some((v,i)=>v===0xff && frame[i+1]===0x55))decisions.push(Number(BigInt(e.sample)-BigInt(metadata.sample_first)));else if(e.kind===4)frame=[];}
     endings=decisions.map(s=>triggerSample-s);
    }
    result.triggerDeltaSamples=endings.sort((a:number,b:number)=>Math.abs(a)-Math.abs(b))[0];
    if(Math.abs(result.triggerDeltaSamples)>64 || !Number.isFinite(result.triggerDeltaSamples))throw Error("trigger is displaced from the matching protocol byte");
    // Compare the FPGA's event ordinals against independent decoding of the
    // retained analog samples. I2C DATA is emitted at ACK; UART at stop midpoint.
    const completeUART=new Set<number>();
    if(protocol==='uart')for(let i=0;i<decoded.spans.length;i++){
     // UART has no explicit packet boundary. A record beginning inside a
     // character can misframe its prefix; use complete valid fixture messages.
     if(expected.every((v,j)=>decoded.spans[i+j]?.kind==='data' && decoded.spans[i+j]?.val===v))
      expected.forEach((_,j)=>completeUART.add(i+j));
    }
    const usbGood=new Set<number>();let usbValid=false;
    if(protocol==='usbls')decoded.spans.forEach((span:any,index:number)=>{if(span.kind==='start')usbValid=false;if(span.kind==='addr')usbValid=true;if(span.kind==='frame-error')usbValid=false;if(usbValid && span.kind==='data')usbGood.add(index);});
    // USB hardware recenters at every observed edge. Locate each final bit
    // from the retained waveform's hysteretic edge, not a packet-wide nominal
    // clock that accumulates oscillator and span-rounding error.
    const usbEdges:number[]=[];let usbEdgeIndex=0;
    if(protocol==='usbls'){
     let high=c1[0]>=103;
     for(let i=1;i<c1.length;i++){
      const next=high?c1[i]>99:c1[i]>=107;
      if(next!==high)usbEdges.push(i);
      high=next;
     }
    }
    const candidates=decoded.spans.flatMap((span:any,index:number)=>{
     if(protocol==='usbls'){
      if(!usbGood.has(index))return [];
      const period=500e6/1500000,nominal=span.i1+1-period/2;
      while(usbEdgeIndex+1<usbEdges.length && usbEdges[usbEdgeIndex+1]<=nominal)usbEdgeIndex++;
      const edge=usbEdges[usbEdgeIndex],cells=Math.round((nominal-edge)/period-0.5);
      const sample=edge+(cells+0.5)*period;
      if(cells<0 || cells>6 || Math.abs(sample-nominal)>period/4)throw Error('USB raw edge cannot anchor final payload bit');
      return [{value:span.val,sample,kind:2}];
     }
     if(protocol==='mil1553')return span.kind==='data'
      ? [{value:span.val,sample:span.i1+0.75*500+1,kind:decoded.spans[index+1]?.kind==='frame-error'?4:2}] : [];
     if(protocol==='uart' && !completeUART.has(index))return [];
     if(protocol==='i2c')return span.kind==='data' && ['ack','nak'].includes(decoded.spans[index+1]?.kind)
      ? [{value:span.val,sample:decoded.spans[index+1].i1,kind:2}] : [];
     if(protocol==='sent')return ['data','crc','frame-error'].includes(span.kind)
      ? [{value:span.val,sample:span.i1,kind:span.kind==='data'?2:span.kind==='crc'?3:4}] : [];
     return span.kind==='data' ? [{value:span.val,sample:protocol==='uart'?span.i0+(Math.floor(1085/2)+9*1085)*4:span.i1,kind:2}] : [];
    });
    const deltas:number[]=[];
    for(const event of events){
     const relative=Number(BigInt(event.sample)-BigInt(metadata.sample_first));
     if(relative<0 || relative>=metadata.words*2)continue;
     if(protocol==='i2c' && (event.count&1))continue;
     if(![2,3,4].includes(event.kind) || (!['sent','mil1553'].includes(protocol) && event.kind!==2))continue;
     const nearest=candidates.filter((c:any)=>c.value===event.value && c.kind===event.kind)
      .map((c:any)=>relative-c.sample).sort((a:number,b:number)=>Math.abs(a)-Math.abs(b))[0];
     // Incomplete messages touching the retained record boundary are omitted
     // by the independent decoder; require matches within its complete range.
     if(relative<(candidates[0]?.sample ?? Infinity) || relative>(candidates.at(-1)?.sample ?? -Infinity)+12)continue;
     if(!Number.isFinite(nearest) || Math.abs(nearest)>12)throw Error(`event/sample mismatch value=${event.value} kind=${event.kind} relative=${relative} delta=${nearest}`);
     deltas.push(nearest);
    }
    if(deltas.length<4)throw Error('too few event-to-raw sample correspondences');
    result.timeline={matchedEvents:deltas.length,minDelta:Math.min(...deltas),maxDelta:Math.max(...deltas),first:metadata.sample_first,last:metadata.sample_last};
    result.rawBytes=first.length;result.sha256=createHash('sha256').update(first).digest('hex');
   }
   await pause(300);
   if(events.length<=count)throw Error('decoder stopped while capture remained active/frozen');
   if(streamError)throw Error(streamError);
   for(let i=1;i<events.length;i++)if(BigInt(events[i].sample)<BigInt(events[i-1].sample))throw Error('event sample ordinal moved backwards');
   if(Number(process.env.DECODED_DWELL_MS)>=10000 && BigInt(events.at(-1)?.sample || 0)<0x100000000n)throw Error('live run did not exercise sample-counter wrap');
   if(match){
    const after=await(await request('/api/decoded/status?'+query)).json();
    if(!after.has_sample_timeline || after.sample_first!==metadata.sample_first || after.sample_last!==metadata.sample_last)throw Error('frozen sample timeline changed during recall');
   }
   result.lossEvents=events.filter(e=>e.kind===5).length;
   if(result.lossEvents)throw Error(`FPGA transcript loss: ${result.lossEvents} markers, ${events.filter(e=>e.kind===5).reduce((n,e)=>n+e.count,0)} reported dropped events`);
   let frame:number[]|null=null,complete=0,uartIndex=-1,badCRC=0;
   for(const event of events){
    if(protocol==='usbls'){
     if(event.protocol!==9 || event.count!==0)throw Error('unexpected USB event identity');
     if(event.kind===4){if(event.value!==0xc2 || event.flags&4)throw Error('unexpected USB error');badCRC++;frame=null;}
     else if(event.kind===1){if(event.value!==0xc3 || !(event.flags&4))throw Error('invalid USB packet started');frame=[];}
     else if(event.kind===2 && frame)frame.push(event.value);
     else if(event.kind===3 && frame){if(!((frame.length===9 && frame.slice(0,7).join(',')==='255,85,170,15,240,0,248')||(frame.length===6 && frame.slice(0,4).join(',')==='0,17,34,51')))throw Error('corrupt USB payload '+frame);complete++;frame=null;}
     else throw Error('unexpected USB event ordering');
     continue;
    }
    if(protocol==='mil1553'){
     if(event.protocol!==7 || ![2,4].includes(event.kind))throw Error('unexpected MIL event');
     // An epoch can start inside a sync-like run. The receiver explicitly
     // rejects the incomplete first bit; this is not a complete parity word.
     // Permit only sequence zero, no payload, and less than one word elapsed.
     if(event.kind===4 && event.sequence===0 && event.value===0 && event.count<=1 && !(event.flags&4) && BigInt(event.sample)<10000n){result.startupFrameErrors=1;continue;}
     if(event.kind===4){if(event.value!==0xa55a || event.count!==1 || event.flags&4)throw Error('unexpected MIL parity error');badCRC++;}
     else {if(![0xa55a,0x5aa5].includes(event.value) || event.count!==(event.value===0xa55a?1:0) || !(event.flags&4))throw Error('corrupt MIL word or sync');complete++;}
     continue;
    }
    if(protocol==='uart'){
     if(event.kind===4 && uartIndex>=0)throw Error('UART framing error after message alignment');
     if(event.kind!==2)continue;
     if(uartIndex<0){if(event.value!==expected[0])continue;uartIndex=0;}
     if(event.value!==expected[uartIndex])throw Error(`corrupt streamed UART byte ${event.value} at ${uartIndex}`);
     uartIndex=(uartIndex+1)%expected.length;if(uartIndex===0)complete++;
     continue;
    }
    if(protocol==='sent' && event.kind===4){
     if(event.count!==7 || event.value!==2 || event.flags&4 || !frame || JSON.stringify(frame)!==JSON.stringify(expected))throw Error('unexpected SENT frame error');
     badCRC++;frame=null;continue;
    }
    if(protocol==='sent' && event.kind===2 && frame && event.count!==frame.length)throw Error('wrong SENT nibble index');
    if(event.kind===1)frame=[];
    else if(event.kind===5 || event.kind===4)frame=null;
    else if(event.kind===2 && frame)frame.push(event.value);
    else if(event.kind===3 && frame){
     if(JSON.stringify(frame)!==JSON.stringify(expected))throw Error(`corrupt streamed ${protocol} transaction ${frame}`);
     if(protocol==='sent' && (event.value!==1 || event.count!==7 || !(event.flags&4)))throw Error('SENT accepted invalid CRC');
     complete++;frame=null;
    }
   }
   result.completeTransactions=complete;
   if(protocol==='sent'){result.badCRCFrames=badCRC;if(badCRC<10)throw Error('missing bad-CRC fixture coverage');}
   if(protocol==='usbls'){result.badPIDPackets=badCRC;if(badCRC<10)throw Error('missing bad-PID fixture coverage');}
   if(protocol==='mil1553'){result.badParityWords=badCRC;if(badCRC<10)throw Error('missing bad-parity fixture coverage');}
   if(complete<10)throw Error("too few complete decoded transactions");
   result.pass=result.lossEvents===0;
   console.log(JSON.stringify({match,events:events.length,lossEvents:result.lossEvents,rawBytes:result.rawBytes,pass:result.pass}));
  }finally{
   abort.abort();await reading;
   if(id)try{await request(`/api/decoded/end?epoch=${id.epoch}&record=${id.record}`,{});}catch(error){result.cleanupError=String(error);if(!streamError)throw error;}
   if(streamError)result.streamError=streamError;
  }
 }
 if(results.some(r=>!r.pass))throw Error("live decoded transcript lost events");
}catch(error){results.push({error:String(error)});throw error;}
finally{
 try{saveEvidence();}finally{await request('/api/set',{control:'run',value:initial.running?1:0});}
}
