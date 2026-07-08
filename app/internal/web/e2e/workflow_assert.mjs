// workflow_assert.mjs — GUI-read assertion helpers shared by the workflow fixtures.
export function near(v, target, tolFrac, absTol = 0) {
  return v != null && isFinite(v) && Math.abs(v - target) <= Math.abs(target) * tolFrac + absTol;
}
export function assert(cond, msg) { if (!cond) throw new Error(msg); }
