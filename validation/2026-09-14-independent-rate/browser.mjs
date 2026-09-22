import {openScope,run} from '../../app/internal/web/scope_po.mjs';
run(async t=>{
 const {browser,page,pageErrors}=await openScope(process.argv[2]);t.browser=browser;
 await page.locator('#acq').selectOption('4');
 await page.locator('#precisionRate').selectOption('15625000');
 await page.waitForFunction(()=>frame.decimation===32 && st.precision_rate_hz===15625000);
 const before=await page.evaluate(()=>({rate:1/frame.sample_s,bw:frame.passband_hz,depth:frame.capture_depth}));
 await page.locator('#tdiv').selectOption('0.00001');
 await page.waitForFunction(()=>frame.tdiv_s===1e-5);
 const after=await page.evaluate(()=>({rate:1/frame.sample_s,bw:frame.passband_hz,depth:frame.capture_depth}));
 t.ok(JSON.stringify(before)===JSON.stringify(after),'Zoom leaves sample rate, passband and capture depth unchanged: '+JSON.stringify(after));
 await page.locator('#precisionRate').selectOption('31250000');
 await page.waitForFunction(()=>frame.decimation===16 && st.precision_rate_hz===31250000);
 t.ok(await page.locator('#tdiv').inputValue()==='0.00001','Rate change leaves time/div unchanged');
 await page.locator('#tdiv').selectOption('5e-7');
 await page.waitForFunction(()=>frame.tdiv_s===5e-7 && frame.decimation===16);
 await page.screenshot({path:'validation/2026-09-14-independent-rate/web.png'});
 t.ok(pageErrors.length===0,'No page errors: '+pageErrors.join(';'));
});
