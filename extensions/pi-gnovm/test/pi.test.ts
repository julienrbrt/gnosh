// The extension loaded by pi itself, in an agent session with pi's scripted
// test provider and a real worker: what the provider is sent after a pop.
// Set GNOVM_WORKER to a gnosh-gnovm-worker on PATH.

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { fauxAssistantMessage, fauxProvider, fauxText, fauxToolCall } from "@earendil-works/pi-ai";
import {
  createAgentSession,
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
} from "@earendil-works/pi-coding-agent";

const program = process.env.GNOVM_WORKER;
const here = dirname(dirname(fileURLToPath(import.meta.url)));

interface Sent {
  role: string;
  content: string | { type: string; text?: string; name?: string; arguments?: { code?: string } }[];
}

function show(messages: Sent[]): string[] {
  return messages
    .filter((m) => m.role !== "system")
    .map((m) => {
      if (typeof m.content === "string") return `${m.role}: ${m.content}`;
      const parts = m.content.map((p) => (p.type === "toolCall" ? `call ${p.arguments?.code}` : (p.text ?? p.type)));
      return `${m.role}: ${parts.join(" | ")}`;
    });
}

test("pi sends no popped work after a mem pop", { skip: !program && "GNOVM_WORKER is not set" }, async () => {
  const home = mkdtempSync(join(tmpdir(), "pi-gnovm-"));
  process.env.XDG_DATA_HOME = join(home, "data");
  const oldPath = process.env.PATH;
  process.env.PATH = dirname(program!) + ":" + process.env.PATH;
  const cwd = join(home, "project");
  const agentDir = join(home, "agent");
  mkdirSync(cwd);
  mkdirSync(agentDir);
  const faux = fauxProvider();
  const runtime = await ModelRuntime.create({ authPath: join(agentDir, "auth.json"), modelsPath: null, refreshOnCreate: false });
  runtime.registerNativeProvider(faux.provider);
  const settingsManager = SettingsManager.inMemory();
  const loader = new DefaultResourceLoader({ cwd, agentDir, settingsManager, additionalExtensionPaths: [here], noSkills: true, noPromptTemplates: true });
  await loader.reload();
  const sent: string[][] = [];
  const step = (reply: ReturnType<typeof fauxAssistantMessage>) => (c: { messages: unknown[] }) => {
    sent.push(show(c.messages as Sent[]));
    return reply;
  };
  const run = (code: string, name?: string) => fauxAssistantMessage(fauxToolCall("gnovm_run", name ? { name, code } : { code }), { stopReason: "toolUse" });
  faux.setResponses([
    step(run('mem.Todo("fix the port").Push()', "mem")),
    step(run("1 + 1")),
    step(run('"lots of work"')),
    step(run('mem.Pop("the port is picked free in setup")', "mem")),
    step(fauxAssistantMessage(fauxText("done"))),
    step(fauxAssistantMessage(fauxText("ok"))),
  ]);
  const { session, extensionsResult } = await createAgentSession({
    cwd,
    agentDir,
    modelRuntime: runtime,
    model: faux.getModel(),
    noTools: "builtin",
    resourceLoader: loader,
    sessionManager: SessionManager.inMemory(cwd),
    settingsManager,
  });
  try {
    assert.deepEqual(extensionsResult.errors, []);
    await session.prompt("fix the port");
    await session.prompt("thanks");
    assert.equal(sent.length, 6);
    // Before the pop, the work is sent.
    assert.ok(sent[3].some((m) => m.includes("lots of work")));
    // After it, it is not; the push's answer says it came, and the prefix
    // through the push is what it was.
    for (const s of [sent[4], sent[5]]) {
      assert.ok(!s.some((m) => m.includes("1 + 1") || m.includes("lots of work")), s.join("\n"));
      assert.equal(s[0], "user: fix the port");
      assert.ok(s[2].startsWith(sent[1][2]));
      assert.match(s[2], /\[Then came the work in it, 2 tool calls \(gnovm_run 2\), removed from your context when you popped it\.\]$/);
      assert.match(s[3], /^assistant: call mem\.Pop/);
    }
    assert.deepEqual(sent[5].slice(0, sent[4].length), sent[4]);
  } finally {
    // pi's runtime says session_shutdown before it disposes of a session.
    await (session.extensionRunner as unknown as { emit(e: unknown): Promise<unknown> }).emit({ type: "session_shutdown", reason: "quit" });
    session.dispose();
    process.env.PATH = oldPath;
    rmSync(home, { recursive: true, force: true });
  }
});
