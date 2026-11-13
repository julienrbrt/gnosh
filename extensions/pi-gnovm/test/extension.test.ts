// The extension against a real worker, through a stand-in for pi's API:
// set GNOVM_WORKER to a gnosh-gnovm-worker (gnosh's `task build:worker`).

import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import extension from "../src/index.ts";
import type { Msg } from "../src/frames.ts";

const program = process.env.GNOVM_WORKER;

test("a mem pop in pi cuts the popped work from the next request", { skip: !program && "GNOVM_WORKER is not set" }, async () => {
  const home = mkdtempSync(join(tmpdir(), "pi-gnovm-"));
  process.env.XDG_DATA_HOME = join(home, "data");
  const handlers = new Map<string, (event: unknown, ctx: unknown) => unknown>();
  const tools = new Map<string, { execute: (...a: unknown[]) => Promise<{ content: { text: string }[]; details: unknown }> }>();
  const pi = {
    registerFlag() {},
    getFlag: (name: string) => (name === "gnovm-worker" ? program : undefined),
    on: (name: string, h: (e: unknown, c: unknown) => unknown) => handlers.set(name, h),
    registerTool: (t: { name: string }) => tools.set(t.name, t as never),
  };
  extension(pi as never);
  const branch: Msg[] = [];
  const ctx = {
    cwd: join(home, "project"),
    sessionManager: {
      getSessionId: () => "pi-test-session",
      getBranch: () => branch.map((message) => ({ type: "message", message })),
    },
  };
  let n = 0;
  async function run(code: string, name?: string) {
    const id = `call-${++n}`;
    branch.push({ role: "assistant", content: [{ type: "toolCall", id, name: "gnovm_run", arguments: { code, name } }] });
    const r = await tools.get("gnovm_run")!.execute(id, { code, name }, undefined, undefined, ctx);
    branch.push({ role: "toolResult", toolCallId: id, toolName: "gnovm_run", content: r.content, details: r.details, isError: false });
    return r.content[0].text;
  }
  try {
    await handlers.get("session_start")!({ type: "session_start", reason: "startup" }, ctx);
    branch.push({ role: "user", content: [{ type: "text", text: "fix the port" }] });
    const pushed = await run('mem.Todo("fix the port").Push()', "mem");
    assert.match(pushed, /in `fix-port`/);
    assert.match(await run("mem.Think(\"the port is 8080\")", "mem"), /You are in `fix-port`/);
    branch.push({ role: "assistant", content: [{ type: "toolCall", id: "b1", name: "bash", arguments: {} }] });
    branch.push({ role: "toolResult", toolCallId: "b1", toolName: "bash", content: [{ type: "text", text: "lots of output" }], isError: false });
    const t0 = performance.now();
    const two = await run("1 + 1");
    const runMs = performance.now() - t0;
    assert.match(two, /^2\n/);
    const popped = await run('mem.Pop("the port is picked free in setup")', "mem");
    assert.match(popped, /popped `fix-port`/);
    const sent = (await handlers.get("context")!({ type: "context", messages: structuredClone(branch) }, ctx)) as { messages: Msg[] };
    assert.ok(sent, "the context was cut");
    assert.deepEqual(
      sent.messages.map((m) => m.role),
      ["user", "assistant", "toolResult", "assistant", "toolResult"],
    );
    assert.match((sent.messages[2].content as { text: string }[]).at(-1)!.text, /\[Then came the work in it, 3 tool calls \(bash 1, gnovm_run 2\)/);
    const list = await tools.get("gnovm_list")!.execute("l1", {}, undefined, undefined, ctx);
    assert.match(list.content[0].text, /- mem: /);
    console.log(`scratch run through the extension: ${runMs.toFixed(2)} ms`);
  } finally {
    await handlers.get("session_shutdown")!({ type: "session_shutdown" }, ctx);
    rmSync(home, { recursive: true, force: true });
  }
});
