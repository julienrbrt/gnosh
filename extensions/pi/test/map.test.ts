import assert from "node:assert/strict";
import { test } from "node:test";
import { FRAMES, fromView, type PiMessage, toEntries } from "../src/map.ts";

const prompt: PiMessage = { role: "user", content: "fix the port" };
const call: PiMessage = { role: "assistant", stopReason: "toolUse", content: [{ type: "thinking", thinking: "t", thinkingSignature: "sig" }, { type: "toolCall", id: "c1", name: "gnovm_run", arguments: { name: "mem", code: "push" } }] };
const result: PiMessage = { role: "toolResult", toolCallId: "c1", content: [{ type: "text", text: "pushed" }], details: { [FRAMES]: [{ kind: "push", id: "f", purpose: "fix", session: "S" }, { kind: "push", id: "x", session: "other" }] } };

test("entries follow bridge.md §7.2", () => {
  const { entries, byUuid } = toEntries([prompt, call, result, { role: "compactionSummary", summary: "before" }, { role: "bashExecution", command: "ls", output: "a", excludeFromContext: true }], "S");
  assert.deepEqual(
    entries.map((e) => [e.uuid, e.type, e.source ?? e.op ?? e.stop]),
    [["m0", "user", "prompt"], ["m1", "assistant", "tool_use"], ["m2", "user", "tool_results"], ["m3", "frame", "push"], ["m4", "user", "notice"]],
  );
  assert.equal(entries[3].frame, "f");
  assert.equal(entries[3].toolUseId, "c1");
  assert.equal(entries[3].sessionId, "S");
  assert.deepEqual(entries[1].message?.content[1], { type: "tool_use", id: "c1", name: "gnovm_run", input: { name: "mem", code: "push" } });
  assert.deepEqual(byUuid.get("m2"), [result]);
});

test("the view comes back as pi's own messages, with notes added to results", () => {
  const { byUuid } = toEntries([prompt, call, result], "S");
  const view = {
    messages: [
      { role: "user" as const, content: [{ type: "text" as const, text: "fix the port" }] },
      { role: "assistant" as const, content: [] },
      { role: "user" as const, content: [{ type: "tool_result" as const, tool_use_id: "c1", content: [{ type: "text" as const, text: "pushed\n[a note]" }] }] },
      { role: "user" as const, content: [{ type: "text" as const, text: "made up" }] },
    ],
    sources: [["m0"], ["m1"], ["m2"], []],
  };
  const out = fromView(view, byUuid);
  assert.equal(out[0], prompt);
  assert.equal(out[1], call); // signatures and all, the same object
  assert.deepEqual(out[2].content, [{ type: "text", text: "pushed" }, { type: "text", text: "\n[a note]" }]);
  assert.equal(out[2].toolCallId, "c1");
  assert.equal(out[3].role, "user");
  assert.equal(result.content?.length, 1, "the session's own message is not changed");
});
