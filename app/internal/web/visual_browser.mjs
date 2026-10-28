// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Real-browser e2e for BUS's Visual view (argv[2]=URL, argv[3]=midi|audio).
// midi: a scope serving a MIDI bus (notes C4 and E4 on, C4 off): entering BUS
// finds it, the Visual view shows the keyboard with E4 held, the messages, the
// canvas drawn, and Sound plays the notes. audio: a scope serving I2S records
// while the bus samples are leased: Digital audio captures one, reads 48 kHz
// with the left channel at -6 dBFS and the right at -12, draws the waveforms
// and plays them looped. SKIP/exit 0 without a browser.
import { openScope, run } from "./scope_po.mjs";

run(async (t) => {
  const mode = process.argv[3];
  const { page, po, browser, pageErrors } = await openScope(process.argv[2], { viewport: { width: 1500, height: 950 } });
  t.browser = browser;
  // TRLC-LINKS: REQ-SDS-278
  const ev = (fn, arg) => page.evaluate(fn, arg);
  await page.click("#tab-bus");
  if (mode === "midi") {
    await t.until(async () => (await po.value("decProto")) === "midi", "entering BUS finds the MIDI bus", 12000);
    await page.click('#busView button[data-value="visual"]');
    await t.until(async () => await ev(() => ws.busView === "visual" && !document.querySelector('.bview[data-view="visual"]').hidden), "the Visual view shows", 3000);
    await t.until(async () => (await ev(() => vis.s && vis.s.roll.length)) >= 2, "the notes reach the piano roll", 6000);
    const st = await ev(() => ({ held: [...vis.s.held.values()].map((n) => n.note), msgs: vis.s.msgs, title: document.getElementById("visTitle").textContent,
      fig: document.getElementById("visFig").textContent }));
    t.ok(st.held.includes(64) && !st.held.includes(60), "E4 held, C4 released: " + JSON.stringify(st.held));
    t.ok(st.title === "MIDI keyboard", "the view names what it draws: " + st.title);
    t.ok(/held/.test(st.fig) && /E4/.test(st.fig) && /messages/.test(st.fig), "the figures list the notes held and the messages: " + st.fig.slice(0, 120));
    // the canvas drew: E4's key lights in channel 1's colour as it plays, and
    // the notes rise away above the keys in it
    // TRLC-LINKS: REQ-SDS-278
    const px = (rows) => ev((rows) => {
      visDraw(true); // read in the task that drew (the drawing buffer is not kept)
      const cv = document.getElementById("visCv"), gl = cv.getContext("webgl2") || cv.getContext("webgl");
      if (!gl) return -1;
      const w = cv.width, h = cv.height, line = new Uint8Array(w * 4);
      let n = 0;
      for (const yf of rows) { // fractions of the height from the bottom (GL's origin)
        gl.readPixels(0, Math.floor(h * yf), w, 1, gl.RGBA, gl.UNSIGNED_BYTE, line);
        for (let i = 0; i < w; i++) if (Math.abs(line[4 * i] - 0x4f) < 3 && Math.abs(line[4 * i + 1] - 0xc3) < 3 && Math.abs(line[4 * i + 2] - 0xf7) < 3) n++;
      }
      return n;
    }, rows);
    t.ok((await ev(() => vis.midiLead)) === 0, "live by default: a note sounds as it comes and rises away");
    t.ok(await ev(() => vis.autoStream), "MIDI asks the scope for its gap-free stream");
    // the scope goes on its stream; this one has nothing in it: back to its captures for the rest
    await t.until(async () => await ev(() => busScopeStreaming()), "the scope streams the bus", 8000);
    await t.until(async () => await ev(() => vis.stream.on), "the view follows the stream", 3000);
    // Packets does not follow the stream: leaving Visual for it ends the stream; Visual again starts it
    await page.click('#busView button[data-value="packets"]');
    await t.until(async () => await ev(() => !busScopeStreaming()), "leaving Visual for Packets ends the scope's stream", 8000);
    await page.click('#busView button[data-value="visual"]');
    await t.until(async () => await ev(() => busScopeStreaming()), "back in Visual, MIDI streams again", 8000);
    // Digital audio reads a captured record: opening it ends the stream; Back to the bus
    await page.click("#visAudio");
    await t.until(async () => await ev(() => !busScopeStreaming()), "Digital audio ends the scope's stream (a streaming scope captures no record)", 8000);
    await page.click("#visAOff");
    await page.click('#busView button[data-value="packets"]');
    await page.click('#busView button[data-value="visual"]');
    await t.until(async () => await ev(() => busScopeStreaming()), "Visual again: MIDI streams", 8000);
    await page.click("#visGapless");
    await t.until(async () => await ev(() => !busScopeStreaming() && !vis.stream.on), "Gap-free off goes back to the captures", 8000);
    await t.until(async () => (await px([0.05, 0.12])) > 0, "E4's key lights in channel 1's colour as it plays", 9000);
    // TRLC-LINKS: REQ-SDS-278
    const above = () => ev(() => {
      visDraw(true);
      const cv = document.getElementById("visCv"), gl = cv.getContext("webgl2") || cv.getContext("webgl");
      const w = cv.width, h = cv.height, all = new Uint8Array(w * h * 4);
      gl.readPixels(0, 0, w, h, gl.RGBA, gl.UNSIGNED_BYTE, all);
      let n = 0;
      for (let y = Math.floor(h * 0.4); y < h; y++) for (let x = 0; x < w; x++) { // the upper 60 %: above the keys
        const i = 4 * (y * w + x);
        if (Math.abs(all[i] - 0x4f) < 30 && Math.abs(all[i + 1] - 0xc3) < 30 && Math.abs(all[i + 2] - 0xf7) < 30) n++;
      }
      return n;
    });
    await t.until(async () => (await above()) > 0, "the notes rise above the keys in their channel's colour", 6000);
    await page.click("#visSound");
    await t.until(async () => await ev(() => vis.sound && vis.sy && vis.sy.played > 0), "Sound plays the notes", 6000);
    const sy = await ev(() => ({ state: vis.sy.ctx.state, played: vis.sy.played }));
    t.ok(sy.state === "running", "the page's audio runs: " + JSON.stringify(sy));
    // read in the click's own task: the next notes keep coming (and are scheduled to land) after it
    t.ok(await ev(() => { document.getElementById("visPanic").click(); return vis.sy.voices.size === 0; }),
      "All notes off silences every voice, those waiting to land too");
    // falling: the notes land on their keys the seconds chosen on, and the sound waits as long
    await page.selectOption("#visLead", "3");
    await t.until(async () => await ev(() => vis.midiLead === 3 && vis.sy.lead === 3), "fall 3 s: the notes and their sound wait 3 s", 3000);
  } else {
    await page.click('#busView button[data-value="visual"]');
    await t.until(async () => await ev(() => !!document.getElementById("visAudio")), "the Visual view offers Digital audio", 4000);
    await page.click("#visAudio");
    await page.selectOption("#visAKind", "i2s");
    await page.click("#visACap");
    await t.until(async () => await ev(() => vis.audio.res && vis.audio.res.ok), "a record is captured and decodes as I2S", 15000);
    const a = await ev(() => ({ fs: vis.audio.res.fs, slot: vis.audio.res.slot, l: vis.audio.res.stats[0].peakDb, r: vis.audio.res.stats[1].peakDb,
      msg: vis.audio.msg, fig: document.getElementById("visFig").textContent }));
    t.ok(Math.abs(a.fs - 48000) < 100 && a.slot === 32, "48 kHz, 32-bit slots: " + JSON.stringify(a).slice(0, 160));
    t.ok(Math.abs(a.l + 6.02) < 0.3 && Math.abs(a.r + 12.04) < 0.3, `the left at -6 dBFS (${a.l.toFixed(2)}), the right at -12 (${a.r.toFixed(2)})`);
    t.ok(/dBFS/.test(a.fig) && /48/.test(a.fig), "the figures show the level and the rate");
    t.ok(/I2S/.test(a.msg), "the status names the source: " + a.msg);
    await page.click("#visAPlay");
    await t.until(async () => await ev(() => !!vis.audio.play), "Play loops the audio", 3000);
    await page.click("#visAOff");
    t.ok(await ev(() => !vis.audio.on && !vis.audio.play), "Back to the bus stops it");
  }
  t.ok(pageErrors.length === 0, "no page errors: " + pageErrors.join(" | "));
});
