// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser check that the web UI says when the scope's converters do not
// convert, as the LCD's notice does: /api/status's adc_fault shows in the
// status line, and goes when it clears. Driven over the REAL ui.html against
// httptest+fakeScope (URL in argv[2]), whose status carries an ADC fault.
// Output protocol: "SKIP: ..."+exit0 when the browser is absent, else
// "ALL PASS".
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { po, browser } = await openScope(process.argv[2]);
  t.browser = browser;
  await po.waitFor(() => typeof st !== "undefined" && st && st.adc_fault);
  await t.until(async () => /ADC fault: converter E2 not converting - restart the scope/.test(await po.statusLine()),
    "the status line names the converter: " + await po.statusLine(), 8000);
  await po.eval(() => { delete st.adc_fault; updateStatusLine(); });
  t.ok(!/ADC fault/.test(await po.statusLine()), "a status without the fault shows none");
});
