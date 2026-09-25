// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
// AU I2C fixture: deliberately abandon the event consumer, then recover retained SRAM.
import {mkdirSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
const base=process.env.SCOPE_URL || 'http://192.168.1.209:8080';
const out=process.argv[2];if(!out)throw Error('output directory required');mkdirSync(out,{recursive:true});
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-025
async function request(path:string,body?:unknown){
 const r=await fetch(base+path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(20000)});
 if(!r.ok)throw Error(`${path}: ${r.status} ${await r.text()}`);return r;
}
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-025
async function pause(ms:number){await new Promise(resolve=>setTimeout(resolve,ms));}
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-025
async function failedStream(query:string){
 const r=await fetch(base+'/api/decoded/events.bin?'+query,{signal:AbortSignal.timeout(5000)});
 const error=await r.text();
 if(r.status!==409 || !error.includes('host buffer overflow'))throw Error(`expected explicit overflow, got ${r.status}: ${error}`);
 return error;
}
const initial=await(await request('/api/status')).json();let id:any;
const result:any={initial};
try{
 id=await(await request('/api/decoded/start',{source:0,normal:true,pre_words:262144,post_words:262144,trigger_level:103,trigger_hysteresis:4,i2c:{enabled:true,clock_channel:0,inverted:true,address:0x24,direction:0,pattern:0x55aa,length:2}})).json();
 result.identity=id;const query=`epoch=${id.epoch}&record=${id.record}`;
 // The 131072-event host queue now absorbs several seconds of this fixture.
 // Eight seconds deliberately exceeds it without consuming any transcript.
 await pause(8000);result.error=await failedStream(query);
 const metadata=await(await request('/api/decoded/status?'+query)).json();result.metadata=metadata;
 if(!metadata.ready || !metadata.frozen || !metadata.triggered || metadata.data_fault)throw Error('retained capture is not valid');
 if(!metadata.has_sample_timeline)throw Error('retained raw capture has no sample timeline');
 const rawPath='/api/decoded/record.bin?'+query+'&offset=0&words='+metadata.words;
 const before=Buffer.from(await(await request(rawPath)).arrayBuffer());
 console.log('Transcript overflow confirmed; checking retained capture after this validator stays idle for 60 seconds.');
 await pause(60000);
 const afterMetadata=await(await request('/api/decoded/status?'+query)).json();result.afterMetadata=afterMetadata;
 if(afterMetadata.record_id!==id.record || afterMetadata.event_epoch!==id.epoch)throw Error('capture identity changed after transcript failure');
 if(!afterMetadata.has_sample_timeline || afterMetadata.sample_first!==metadata.sample_first || afterMetadata.sample_last!==metadata.sample_last)throw Error('retained timeline changed after transcript failure');
 if(await failedStream(query)!==result.error)throw Error('original transcript error changed');
 const after=Buffer.from(await(await request(rawPath)).arrayBuffer());
 if(before.length!==2097152 || !before.equals(after))throw Error('retained SRAM changed after transcript failure');
 result.rawBytes=after.length;result.sha256=createHash('sha256').update(after).digest('hex');result.pass=true;
 console.log(JSON.stringify({pass:true,rawBytes:after.length,identity:id}));
}catch(error){result.failure=String(error);throw error;}
finally{
 try{if(id)await request(`/api/decoded/end?epoch=${id.epoch}&record=${id.record}`,{});}
 finally{writeFileSync(out+'/decoded-failure.json',JSON.stringify(result,null,2));await request('/api/set',{control:'run',value:initial.running?1:0});}
}
