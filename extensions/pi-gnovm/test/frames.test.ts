import assert from "node:assert/strict";
import { test } from "node:test";
import { cutPopped, frameLine, framesOf, type Msg, RUN_TOOL, turnStart } from "../src/frames.ts";

const S = "s1";
const user = (text: string): Msg => ({ role: "user", content: [{ type: "text", text }] });
const say = (text: string): Msg => ({ role: "assistant", content: [{ type: "text", text }], stopReason: "stop" });
const call = (...calls: [string, string][]): Msg => ({
  role: "assistant",
  content: calls.map(([id, name]) => ({ type: "toolCall", id, name, arguments: {} })),
  stopReason: "toolUse",
});
const result = (id: string, name: string, text = "ok", frames?: unknown[]): Msg => ({
  role: "toolResult",
  toolCallId: id,
  toolName: name,
  content: [{ type: "text", text }],
  details: frames ? { vm: "mem", frames } : undefined,
  isError: false,
});
const push = (id: string, frame: string) => [call([id, RUN_TOOL]), result(id, RUN_TOOL, "pushed", [{ kind: "push", id: frame, purpose: frame, session: S }])];
const pop = (id: string, frame: string) => [call([id, RUN_TOOL]), result(id, RUN_TOOL, "popped", [{ kind: "pop", id: frame, result: "done", session: S }])];
const texts = (msgs: Msg[]) =>
  msgs.map((m) => `${m.role}:${(m.content as { type: string; text?: string; id?: string }[]).map((p) => p.text ?? p.id).join("|")}`);

test("a pop cuts what came between its push and itself, and notes it on the push's answer", () => {
  const msgs = [
    user("fix it"),
    ...push("c1", "fix"),
    call(["c2", "read"]),
    result("c2", "read", "file text"),
    call(["c3", "edit"], ["c4", "bash"]),
    result("c3", "edit"),
    result("c4", "bash"),
    ...pop("c5", "fix"),
  ];
  const f = framesOf(msgs, S);
  assert.deepEqual(f.stack, []);
  assert.deepEqual(f.pops, [{ frame: "fix", pushCallID: "c1", popCallID: "c5" }]);
  const cut = cutPopped(msgs, f.pops);
  assert.deepEqual(texts(cut), [
    "user:fix it",
    "assistant:c1",
    "toolResult:pushed\n[Then came the work in it, 3 tool calls (bash 1, edit 1, read 1), removed from your context when you popped it.]",
    "assistant:c5",
    "toolResult:popped",
  ]);
  // The session's own messages are not changed.
  assert.equal((msgs[2].content as { text: string }[])[0].text, "pushed");
  // Every request after it cuts the same, and the prefix through the push
  // is what it was before the pop.
  const later = [...msgs, say("done"), user("next")];
  const cut2 = cutPopped(later, framesOf(later, S).pops);
  assert.deepEqual(texts(cut2).slice(0, cut.length), texts(cut));
  assert.deepEqual(cut.slice(0, 2), msgs.slice(0, 2));
});

test("the user's words in the pop's own turn stay; earlier turns' go", () => {
  const msgs = [
    user("task"),
    ...push("c1", "t"),
    call(["c2", "bash"]),
    result("c2", "bash"),
    say("half way, shall I go on?"),
    user("yes, and use port 8080"),
    call(["c3", "bash"]),
    result("c3", "bash"),
    ...pop("c4", "t"),
  ];
  const cut = cutPopped(msgs, framesOf(msgs, S).pops);
  assert.deepEqual(texts(cut), [
    "user:task",
    "assistant:c1",
    "toolResult:pushed\n[Then came the work in it, 2 tool calls (bash 2), removed from your context when you popped it.]",
    "user:yes, and use port 8080",
    "assistant:c4",
    "toolResult:popped",
  ]);
  assert.equal(turnStart(msgs, 9), 6);
});

test("a prompt steered into a running turn belongs to it", () => {
  const msgs = [user("go"), call(["c1", "bash"]), result("c1", "bash"), user("also this"), call(["c2", "bash"])];
  assert.equal(turnStart(msgs, 4), 0);
});

test("nested pops cut the union, with one note", () => {
  const msgs = [
    user("go"),
    ...push("c1", "a"),
    ...push("c2", "a/b"),
    call(["c3", "bash"]),
    result("c3", "bash"),
    ...pop("c4", "a/b"),
    call(["c5", "bash"]),
    result("c5", "bash"),
    ...pop("c6", "a"),
  ];
  const f = framesOf(msgs, S);
  assert.equal(f.pops.length, 2);
  const cut = cutPopped(msgs, f.pops);
  assert.deepEqual(texts(cut), [
    "user:go",
    "assistant:c1",
    "toolResult:pushed\n[Then came the work in it, 4 tool calls (bash 2, gnovm_run 2), removed from your context when you popped it.]",
    "assistant:c6",
    "toolResult:popped",
  ]);
});

test("an open frame is the stack, and cuts nothing", () => {
  const msgs = [user("go"), ...push("c1", "a"), ...push("c2", "a/b"), call(["c3", "bash"]), result("c3", "bash")];
  const f = framesOf(msgs, S);
  assert.deepEqual(
    f.stack.map((s) => s.id),
    ["a", "a/b"],
  );
  assert.equal(cutPopped(msgs, f.pops), msgs);
  assert.match(frameLine(f.stack), /^You are in `a\/b` "a\/b" \(your stack: `a`, then `a\/b`\): pop it/);
});

test("another session's frames are not this one's", () => {
  const msgs = [
    call(["c1", RUN_TOOL]),
    result("c1", RUN_TOOL, "x", [{ kind: "push", id: "theirs", purpose: "", session: "s2" }]),
  ];
  assert.deepEqual(framesOf(msgs, S).stack, []);
});

test("a push behind a compaction cuts from the model's first message on", () => {
  const branch = [user("go"), ...push("c1", "a"), call(["c2", "bash"]), result("c2", "bash"), ...pop("c3", "a")];
  const sent = [user("summary of earlier work"), call(["c2", "bash"]), result("c2", "bash"), ...pop("c3", "a")];
  const cut = cutPopped(sent, framesOf(branch, S).pops);
  assert.deepEqual(texts(cut), ["user:summary of earlier work", "assistant:c3", "toolResult:popped"]);
});

test("a push and pop in one call cut nothing", () => {
  const msgs = [
    user("go"),
    call(["c1", RUN_TOOL]),
    result("c1", RUN_TOOL, "x", [
      { kind: "push", id: "a", purpose: "", session: S },
      { kind: "pop", id: "a", session: S },
    ]),
  ];
  assert.equal(cutPopped(msgs, framesOf(msgs, S).pops), msgs);
});
