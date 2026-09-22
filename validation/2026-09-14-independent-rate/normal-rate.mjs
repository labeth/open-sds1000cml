import {openScope,run} from '../../app/internal/web/scope_po.mjs';
run(async t=>{
 const {browser,page,pageErrors}=await openScope(process.argv[2]);t.browser=browser;
 await page.locator('#acq').selectOption('0');
 await page.locator('#tdiv').selectOption('1e-9');
 await page.waitForFunction(()=>frame.tdiv_s===1e-9 && frame.sample_s===2e-9);
 const label=await page.locator('#actualSampleRate').textContent();
 t.ok(label.includes('500') && label.includes('MS/s'),'Actual rate at 1 ns/div: '+label);
 t.ok(pageErrors.length===0,'No page errors');
 await page.screenshot({path:'validation/2026-09-14-independent-rate/normal-1ns.png'});
});
