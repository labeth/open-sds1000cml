// Real-browser e2e: a serial trigger armed elsewhere (the LCD, a phone, another
// page) is shown, never replaced (argv[2]=URL). The Go side arms the fake scope
// on 55 AA, address any. With this page's decoding on, it shows the scope's
// match and posts no serial settings of its own (it used to push its empty
// match once a second, turning the trigger to "any byte"); a match typed here
// is this page's, and then it pushes. No page errors. SKIP/exit 0 without browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const { page, po, browser, pageErrors } = await openScope(process.argv[2]);
  t.browser = browser;

  const posts = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && /\/api\/serial/.test(r.url())) posts.push(r.postData() || "");
  });

  await po.ws("bus");
  await po.waitFor(() => !busDetecting, null, 10000);
  await po.setSelect("decProto", "uart");
  await t.until(async () => (await po.eval(() => document.getElementById("stBytes").value)) === "55 AA",
    "the page shows the match the scope is armed with (55 AA)", 6000);
  t.ok(await po.hasClass("stArm", "on"), "…and the trigger as armed");
  await t.until(async () => (await po.text("trigCap")).includes("55 AA"), "the strip's caption names it too", 4000);
  await new Promise((r) => setTimeout(r, 2500));
  t.ok(posts.length === 0, "the page posts no serial settings of its own: " + JSON.stringify(posts));

  // typed here, the match is this page's: it is pushed
  await po.eval(() => { const el = document.getElementById("stBytes"); el.value = "48 69"; el.dispatchEvent(new Event("change", { bubbles: true })); });
  await t.until(async () => posts.some((p) => /"bytes":\[72,105\]/.test(p)), "a match typed here is pushed", 4000);
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join("; "));
});
