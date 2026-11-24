import assert from "node:assert/strict";
import { test } from "node:test";
import { FRAMES, fromView, type PiMessage, toEntries } from "../src/map.ts";

const prompt: PiMessage = { role: "user", content: "fix the port", timestamp: 1 };
const call: PiMessage = { role: "assistant", stopReason: "toolUse", content: [{ type: "thinking", thinking: "t", thinkingSignature: "sig" }, { type: "toolCall", id: "c1", name: "gnovm_run", arguments: { name: "mem", code: "push" } }] };
const result: PiMessage = { role: "toolResult", toolCallId: "c1", content: [{ type: "text", text: "pushed" }], details: { [FRAMES]: [{ kind: "push", id: "f", purpose: "fix", session: "S" }, { kind: "push", id: "x", session: "other" }] } };

test("entries follow bridge.md §7.2", () => {
  const { entries, byUuid } = toEntries([prompt, call, result, { role: "compactionSummary", summary: "before" }, { role: "bashExecution", command: "ls", output: "a", excludeFromContext: true }], "S");
  assert.deepEqual(
    entries.map((e) => [e.uuid, e.type, e.source ?? e.op ?? e.stop]),
    [["u:1", "user", "prompt"], ["a:c1", "assistant", "tool_use"], ["r:c1", "user", "tool_results"], ["f:c1:0", "frame", "push"], ["i:3", "user", "notice"]],
  );
  assert.equal(entries[3].frame, "f");
  assert.equal(entries[3].toolUseId, "c1");
  assert.equal(entries[3].sessionId, "S");
  assert.deepEqual(entries[1].message?.content[1], { type: "tool_use", id: "c1", name: "gnovm_run", input: { name: "mem", code: "push" } });
  assert.deepEqual(byUuid.get("r:c1"), [result]);
});

test("an entry keeps its uuid when the messages before it go", () => {
  const later: PiMessage = { role: "user", content: "and the docs", timestamp: 9 };
  const a = toEntries([prompt, call, result, later], "S").entries.map((e) => e.uuid);
  const b = toEntries([{ role: "compactionSummary", summary: "s", timestamp: 5 }, result, later], "S").entries.map((e) => e.uuid);
  assert.ok(b.includes("r:c1") && b.includes("u:9") && a.includes("u:9"));
});

test("pi's file and command tools go by gnosh's names, with their file as gnosh reads it", () => {
  const edit: PiMessage = {
    role: "assistant",
    content: [
      { type: "toolCall", id: "e1", name: "edit", arguments: { path: "a.go", edits: [] } },
      { type: "toolCall", id: "e2", name: "read", arguments: { path: "b.go" } },
      { type: "toolCall", id: "e3", name: "bash", arguments: { command: "go test" } },
    ],
  };
  const { entries } = toEntries([prompt, edit], "S");
  const uses = entries[1].message?.content ?? [];
  assert.deepEqual(uses.map((b) => b.name), ["Edit", "Read", "Bash"]);
  assert.deepEqual(uses[0].input, { path: "a.go", edits: [], file_path: "a.go" });
  assert.deepEqual(uses[1].input, { path: "b.go", file_path: "b.go" });
});

test("entries carry their message's time, and a frame its result's", () => {
  const timed = { ...result, timestamp: 2000 };
  const { entries } = toEntries([prompt, call, timed], "S");
  assert.equal(entries[0].timestamp, "1970-01-01T00:00:00.001Z");
  assert.equal(entries[1].timestamp, undefined); // a message with no time has none
  assert.equal(entries[3].type, "frame");
  assert.equal(entries[3].timestamp, "1970-01-01T00:00:02.000Z");
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
    sources: [["u:1"], ["a:c1"], ["r:c1"], []],
  };
  const out = fromView(view, byUuid);
  assert.equal(out[0], prompt);
  assert.equal(out[1], call); // signatures and all, the same object
  assert.deepEqual(out[2].content, [{ type: "text", text: "pushed" }, { type: "text", text: "\n[a note]" }]);
  assert.equal(out[2].toolCallId, "c1");
  assert.equal(out[3].role, "user");
  assert.equal(result.content?.length, 1, "the session's own message is not changed");
});
