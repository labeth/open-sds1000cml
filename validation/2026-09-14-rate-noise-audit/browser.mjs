import {openScope,run} from '../../app/internal/web/scope_po.mjs';
run(async t=>{
 const {browser,page,pageErrors}=await openScope(process.argv[2]);t.browser=browser;
 await page.waitForFunction(()=>frame && frame.filter && frame.filter.includes('phase checked'));
 t.ok((await page.locator('#actualSampleRate').textContent()).includes('500 MS/s'),'Normal 10 us/div shows 500 MS/s');
 t.ok((await page.locator('#acquisitionWarning').textContent())==='','Verified calibration active');
 t.ok(await page.locator('#tdiv').inputValue()==='0.00001','Original 10 us/div restored');
 const windowSeconds=await page.evaluate(()=>(view.win.b-view.win.a)*frame.col_span_s);
 t.ok(Math.abs(windowSeconds-1e-4)<1e-9,'Stopped view keeps 100 us screen span: '+windowSeconds);
 await page.screenshot({path:'validation/2026-09-14-rate-noise-audit/web.png'});
 t.ok(pageErrors.length===0,'No browser errors');
});
