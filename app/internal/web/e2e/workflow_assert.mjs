// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// workflow_assert.mjs — GUI-read assertion helpers shared by the workflow fixtures.
// TRLC-LINKS: REQ-SDS-208
export function near(v, target, tolFrac, absTol = 0) {
  return v != null && isFinite(v) && Math.abs(v - target) <= Math.abs(target) * tolFrac + absTol;
}
// TRLC-LINKS: REQ-SDS-208
export function assert(cond, msg) { if (!cond) throw new Error(msg); }
