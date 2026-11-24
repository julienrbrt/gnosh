// The demo (demo/run.ts) as a test: in the same small window and on the
// same script, plain pi summarizes and pi-gnosh slides; the pop takes the
// frame's work out of what is sent; the sum is worked out on a Gno machine.
// GNOSH_BIN names the gnosh program; without it the test is skipped.

import assert from "node:assert/strict";
import { test } from "node:test";
import { runDemo } from "../demo/run.ts";

const program = process.env.GNOSH_BIN;

test("pi-gnosh slides where plain pi summarizes", { skip: !program && "GNOSH_BIN is not set" }, async () => {
  const plain = await runDemo({ gnosh: false, program });
  assert.ok(plain.compactions >= 1, `plain pi made ${plain.compactions} summaries`);

  const g = await runDemo({ gnosh: true, program });
  assert.equal(g.compactions, 0, "pi-gnosh made a summary");
  assert.ok(g.slides >= 1, "pi-gnosh made no slide");
  assert.ok(g.requests[0].system.includes("<memory>"), "the system prompt has no part about mem");
  assert.ok(g.requests[0].messages.some((m) => m.includes("Use mem and Gno")), "the prompt has no mem line");

  const all = g.requests.flatMap((r) => r.messages);
  assert.ok(all.some((m) => m.includes("left your context to make room")), "the model was not told of the slide");
  assert.ok(all.some((m) => m.startsWith("toolResult: 395")), "the sum was not worked out on a machine");

  // The last request, after the pop: none of the frame's work, and fewer
  // tokens than at the peak.
  const lastReq = g.requests.at(-1)!;
  assert.ok(!lastReq.messages.some((m) => m.startsWith("toolResult: work work")), lastReq.messages.join("\n"));
  assert.ok(lastReq.messages.some((m) => m.includes("mem.Pop")));
  const peak = Math.max(...g.requests.map((r) => r.tokens));
  assert.ok(lastReq.tokens < peak, `${lastReq.tokens} >= ${peak}`);

  // The pop's close was reviewed by the model, as gnosh reviews a close.
  assert.ok(g.sides.some((sd) => sd.startsWith("You check whether an AI coding agent did the todo")), g.sides.join("\n"));
  assert.equal(plain.sides.length, 0);
});

test("work mem holds no record of is summarized, not slid", { skip: !program && "GNOSH_BIN is not set" }, async () => {
  const g = await runDemo({ gnosh: true, program, think: false });
  assert.ok(g.compactions >= 1, `pi-gnosh made ${g.compactions} summaries of work mem had no record of`);
  const all = g.requests.flatMap((r) => r.messages);
  assert.ok(!all.some((m) => m.includes("The start of `port-fix` left your context")), "the frame's unrecorded work slid");
});
