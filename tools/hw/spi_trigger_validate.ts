// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
// Bench prerequisite: AU SPI mode 0, 200 kHz, MSB first. Blue A2/C1 clock, yellow A11/C2 data.
// Message: 48 69 20 55 AA 0F F0 0A. TRIGGER_PROTOCOL=sent uses the 3 us SENT fixture.
// Leaves serial qualification disabled and restores the initial timebase/mode.
import {mkdirSync, writeFileSync} from 'node:fs';
const base=process.env.SCOPE_URL || 'http://192.168.1.209:8080';
const out=process.argv[2];
if(!out)throw Error('usage: spi_trigger_validate.ts output-directory');
mkdirSync(out,{recursive:true});
const pause=(ms:number)=>new Promise(r=>setTimeout(r,ms));
const status=async()=>await(await fetch(base+'/api/status',{signal:AbortSignal.timeout(5000)})).json();
const post=async(path:string,body:unknown)=>{
 const r=await fetch(base+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),signal:AbortSignal.timeout(5000)});
 const result=await r.json();if(!r.ok || !result.ok)throw Error(JSON.stringify(result));return result;
};
const set=(control:string,value:number)=>post('/api/set',{control,value});
const sent=process.env.TRIGGER_PROTOCOL==="sent";
const usb=process.env.TRIGGER_PROTOCOL==="usbls";
const mil=process.env.TRIGGER_PROTOCOL==="mil1553";
const protocol=usb?"usbls":mil?"mil1553":sent?"sent":"spi";
const hardware=usb || mil || sent || process.env.SPI_HARDWARE==="1";
const initial=await status(),results:any[]=[];
async function advanced(before:any,count:number){
 const deadline=Date.now()+45000;
 while(Date.now()<deadline){const s=await status();if(s.bus_errors!==before.bus_errors)throw Error('bus errors increased');if(s.frames>=before.frames+count)return s;await pause(200);}
 throw Error('capture progress timed out');
}
try{
 await set('serialmode',0);await set('norm',0);await set('tdiv',.0005);await set('run',1);
 for(const [name,bytes,expectMatch] of [
  ['match',usb?[0xff,0x55]:mil?[0xa55a]:sent?[5,10]:[0x48,0x69],true],['reject',mil?[0xdead]:sent?[14,15]:[0xde,0xad],false],['resume',usb?[0xff,0x55]:mil?[0xa55a]:sent?[5,10]:[0x48,0x69],true],
 ] as const){
  await set('serialmode',0);await set('norm',0);
  await advanced(await status(),3);
  await post('/api/serial',{proto:usb?9:mil?7:sent?5:3,baud:usb?1500000:mil?1000000:0,tickNs:sent?3000:0,nibbles:8,spiClockHz:hardware?200000:0,chA:0,chB:1,cpol:false,cpha:false,msb:true,inverted:true,haveThr:true,threshold:103,bytes});
  await set('serialmode',1);await set('norm',1);
  await pause(1000);
  const a=await status();
  if(a.serial_backend!==(hardware?"hardware-"+protocol:"software"))throw Error("requested SPI backend was not selected");
  // Software NORM may capture candidates, but must not publish absent data.
  let z:any;
  if(expectMatch){z=await advanced(a,10);if(z.seq<=a.seq)throw Error('matching bytes did not publish');}
  else{await pause(4000);z=await status();if(z.seq!==a.seq)throw Error('absent bytes published a frame');}
  if(z.bus_errors!==a.bus_errors || z.wedged)throw Error('acquisition health failed');
  const row={name,before:a,after:z};results.push(row);console.log(JSON.stringify({name,publications:z.seq-a.seq,backend:z.serial_backend}));
 }
 await set('serialmode',0);await set('norm',0);
 const a=await status(),z=await advanced(a,10);
 if(z.seq<=a.seq)throw Error('edge capture did not resume');
 results.push({name:'edge-resume',before:a,after:z});
}finally{
 writeFileSync(out+"/"+protocol+(hardware?"-hardware-validation.json":"-software-validation.json"),JSON.stringify({initial,results},null,2));
 await set('serialmode',0);await set('norm',initial.norm?1:0);await set('tdiv',initial.tdiv_s);await set('run',initial.running?1:0);
}
