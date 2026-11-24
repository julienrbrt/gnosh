import assert from "node:assert/strict";
import { test } from "node:test";
import { Check, MAX_CHECKS, Stack } from "../src/check.ts";

const push = (id: string, session = "S") => ({ kind: "push" as const, id, purpose: id, session });
const pop = (id: string, result = "r", session = "S") => ({ kind: "pop" as const, id, purpose: id, result, session });

test("a run that closed its top todo is sent back once to check it", () => {
  const s = new Stack();
  s.add([push("fix"), push("fix/a")], "S");
  s.add([pop("fix/a"), pop("fix")], "S");
  assert.deepEqual(s.open, []);
  assert.deepEqual(s.closed.map((f) => f.id), ["fix"]); // only top-level closes
  const c = new Check();
  const n = c.decide(s, "stop");
  assert.match(n ?? "", /you closed `fix` "fix" as done/);
  assert.match(n ?? "", /against the files and commands themselves/);
  assert.equal(c.decide(s, "stop"), undefined, "the same close is checked once");
  s.add([push("more"), pop("more")], "S");
  assert.match(c.decide(s, "stop") ?? "", /`more`/, "a new close is checked");
});

test("a run that stops with a frame open is sent back to it once", () => {
  const s = new Stack();
  s.add([push("t"), push("t/x")], "S");
  const c = new Check();
  assert.match(c.decide(s, "stop") ?? "", /`t` "t" is still open in mem/);
  assert.equal(c.decide(s, "stop"), undefined);
});

test("no frames, other sessions, failed replies and the cap end the run", () => {
  const c = new Check();
  const s = new Stack();
  assert.equal(c.decide(s, "stop"), undefined);
  s.add([push("o", "other"), pop("o", "r", "other")], "S");
  assert.equal(c.decide(s, "stop"), undefined, "another session's frames");
  s.add([push("a"), pop("a")], "S");
  assert.equal(c.decide(s, "error"), undefined);
  assert.equal(c.decide(s, "aborted"), undefined);
  let sent = 0;
  for (let i = 0; i < 5; i++) {
    s.add([push(`k${i}`), pop(`k${i}`)], "S");
    if (c.decide(s, "stop")) sent++;
  }
  assert.equal(sent, MAX_CHECKS);
  c.reset();
  s.add([push("z"), pop("z")], "S");
  assert.ok(c.decide(s, "stop"), "a new prompt may be sent back again");
});
