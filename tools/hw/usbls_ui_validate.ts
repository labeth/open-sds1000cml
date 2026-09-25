// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
// Live Au USB fixture: A2/C1 and A11/C2, 1.5 Mbit/s, good and bad-PID DATA0.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-033
import {mkdirSync,readFileSync,writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {openScope,findPlaywright} from '../../app/internal/web/scope_po.mjs';
const base=process.env.SCOPE_URL || 'http://192.168.1.209:8080';
const out=process.argv[2];if(!out)throw Error('output directory required');
if(!findPlaywright())throw Error('live UI validation requires installed Playwright');
mkdirSync(out,{recursive:true});
const initial=await(await fetch(base+'/api/status')).json();
const result:any={};let ui:any,id:any;
try{
 ui=await openScope(base);const {page,po,pageErrors}=ui;
 await po.setSelect('decProto','usb');await po.setSelect('decLine','1');

 await po.fill('decBaud','1500000');await page.locator('#decBaud').press('Tab');
 if(await po.hasClass('decAuto','on'))await po.click('decAuto');
 await po.setRange('decThr',103);await po.fill('stBytes','FF 55');await page.locator('#stBytes').press('Tab');
 const threshold=await page.locator('#decThrV').boundingBox(),card=await page.locator('#decodeCard').boundingBox();
 if(!threshold || !card || threshold.x+threshold.width>card.x+card.width)throw Error('threshold readout is clipped');
 const start=page.waitForResponse((r:any)=>r.url().endsWith('/api/decoded/start'));
 await po.click('dsStart');const response=await start;
 if(!response.ok())throw Error(await response.text());
 id=await response.json();result.identity=id;result.config=response.request().postDataJSON();
 if(result.config.usbls?.pattern!==0xff55 || result.config.usbls?.bit_ticks_q8!==21333 || result.config.usbls?.length!==2)throw Error('UI lost USB configuration');
 await page.waitForFunction(()=>!document.getElementById('dsRaw').disabled,null,{timeout:15000});
 result.status=await po.text('dsStatus');result.countBefore=await po.text('dsCount');
 for(const n of [1,2]){
  const downloaded=page.waitForEvent('download');await po.click('dsRaw');
  const download=await downloaded;await download.saveAs(out+`/retained-${n}.bin`);
 }
 const first=readFileSync(out+'/retained-1.bin'),second=readFileSync(out+'/retained-2.bin');
 if(first.length!==2097152 || !first.equals(second))throw Error('browser downloads are incomplete or changed');
 result.rawBytes=first.length;result.sha256=createHash('sha256').update(first).digest('hex');
 const dwell=Number(process.env.DECODED_UI_DWELL_MS || 0);
 if(!Number.isFinite(dwell) || dwell<0)throw Error('invalid browser dwell');
 if(dwell)await page.waitForTimeout(dwell);
 result.dwellMs=dwell;
 result.countAfter=await po.text('dsCount');result.events=await po.text('dsEvents');
 if(!/\b0 lost$/.test(result.countAfter) || result.countBefore===result.countAfter || !/DATA (FF|55)/.test(result.events))throw Error('UI transcript stopped, lost events or omitted USB bytes');
 if(pageErrors.length)throw Error('browser errors: '+pageErrors.join('; '));
 await page.locator('#decodeCard').screenshot({path:out+'/controls.png'});
 result.pass=true;console.log(JSON.stringify(result));
}catch(error){result.error=String(error);throw error;}
finally{
 if(id){const r=await fetch(base+`/api/decoded/end?epoch=${id.epoch}&record=${id.record}`,{method:'POST'});result.endStatus=r.status;}
 if(ui)await ui.close();
 const restore=await fetch(base+'/api/set',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({control:'run',value:initial.running?1:0})});
 result.restoreStatus=restore.status;writeFileSync(out+'/ui-validation.json',JSON.stringify(result,null,2));
}
