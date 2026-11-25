// The extension loaded by pi itself, in an agent session with pi's scripted
// test provider and a real `gnosh bridge`: what the provider is sent after
// a pop. GNOSH_BIN names the gnosh program; without it the test is skipped.

// These tests assert gnosh's full set of prompt nudges (the mem line and the
// rest), so they run with lean off; the lean default is covered by the Go
// bridge tests (internal/bridge/lean_test.go).
process.env.PI_GNOSH_LEAN = "0";
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

const program = process.env.GNOSH_BIN;
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

type Script = ReturnType<typeof fauxAssistantMessage>[];

/**
 * session runs pi with the extension and the faux provider: the model's
 * replies are script's, in order, and the side requests gnosh's checks
 * make are answered by judge, told the request's purpose. sent holds what
 * each of the model's own requests was sent.
 */
async function withSession(
  script: Script,
  judge: (purpose: "name" | "close-review" | "goal-judge") => string,
  body: (s: { prompt(text: string): Promise<void>; sent: string[][]; asked: string[] }) => Promise<void>,
) {
  const home = mkdtempSync(join(tmpdir(), "pi-gnosh-"));
  const oldEnv = { ...process.env };
  process.env.PI_GNOSH_PROGRAM = program;
  process.env.GNOSH_DATA_DIR = join(home, "data");
  process.env.XDG_CONFIG_HOME = join(home, "config");
  process.env.XDG_CACHE_HOME = join(home, "cache");
  const oldCwd = process.cwd();
  const cwd = join(home, "project");
  const agentDir = join(home, "agent");
  mkdirSync(cwd);
  process.chdir(cwd);
  mkdirSync(agentDir);
  const faux = fauxProvider();
  const runtime = await ModelRuntime.create({ authPath: join(agentDir, "auth.json"), modelsPath: null, refreshOnCreate: false });
  runtime.registerNativeProvider(faux.provider);
  const settingsManager = SettingsManager.inMemory();
  const loader = new DefaultResourceLoader({ cwd, agentDir, settingsManager, additionalExtensionPaths: [here], noSkills: true, noPromptTemplates: true });
  await loader.reload();
  const sent: string[][] = [];
  const asked: string[] = [];
  const replies = [...script];
  const respond = (c: { messages: unknown[] }) => {
    // The system prompt is the transcript's system message.
    const system = (c.messages as Sent[]).find((m) => m.role === "system");
    const sys = typeof system?.content === "string" ? system.content : (system?.content ?? []).map((p) => p.text ?? "").join("");
    const purpose = sys.startsWith("You name notes")
      ? "name"
      : sys.startsWith("You check whether an AI coding agent did the todo")
        ? "close-review"
        : sys.startsWith("You check whether an AI coding agent has reached the goal")
          ? "goal-judge"
          : undefined;
    if (purpose) {
      asked.push(purpose);
      return fauxAssistantMessage(fauxText(judge(purpose)));
    }
    sent.push(show(c.messages as Sent[]));
    const next = replies.shift();
    assert.ok(next, `the model was asked more than the script holds:\n${sent.at(-1)?.join("\n")}`);
    return next;
  };
  faux.setResponses(Array.from({ length: 60 }, () => respond));
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
    await body({ prompt: (t) => session.prompt(t), sent, asked });
  } finally {
    // pi's runtime says session_shutdown before it disposes of a session.
    await (session.extensionRunner as unknown as { emit(e: unknown): Promise<unknown> }).emit({ type: "session_shutdown", reason: "quit" });
    session.dispose();
    process.chdir(oldCwd);
    process.env = oldEnv;
    rmSync(home, { recursive: true, force: true });
  }
}

const run = (code: string, name?: string) => fauxAssistantMessage(fauxToolCall("gnovm_run", name ? { name, code } : { code }), { stopReason: "toolUse" });
const ok = JSON.stringify({ ok: true, basis: "evidence", reason: "the result shows it" });

test("pi sends no popped work after a mem pop, and the pop's result says what left", { skip: !program && "GNOSH_BIN is not set" }, async () => {
  const script = [
    run('mem.Todo("fix the port").Push()', "mem"),
    run("1 + 1"),
    run('"lots of work"'),
    run('mem.Pop("the port is picked free in setup")', "mem"),
    fauxAssistantMessage(fauxText("done")),
    fauxAssistantMessage(fauxText("ok")),
  ];
  await withSession(script, (p) => (p === "name" ? "port-fix" : ok), async ({ prompt, sent, asked }) => {
    await prompt("fix the port");
    await prompt("thanks");
    assert.equal(sent.length, 6);
    // The close was reviewed by the model as it was made, so the turn's end
    // has nothing left to judge.
    assert.equal(asked.filter((a) => a === "close-review").length, 1, asked.join(","));
    assert.ok(!asked.includes("goal-judge"), asked.join(","));
    // Before the pop, the work is sent.
    assert.ok(sent[3].some((m) => m.includes("lots of work")));
    // The pop's own result says what left the context.
    assert.ok(sent[4].some((m) => /^toolResult: .*Everything since you entered `[a-z-]+` was removed from your context/s.test(m)), sent[4].join("\n"));
    // After it, the work is not sent; the push's answer says it came, and
    // the prefix through the push is what it was.
    for (const s of [sent[4], sent[5]]) {
      assert.ok(!s.some((m) => m.includes("1 + 1") || m.includes("lots of work")), s.join("\n"));
      assert.equal(s[0], "user: fix the port");
      assert.match(s[1], /^user: <gnosh>Use mem and Gno/); // the mem line after the prompt
      assert.ok(s[3].startsWith(sent[1][3]));
      assert.match(s[3].replace(" | ", ""), /removed from your context when you popped it\.\]$/);
      assert.match(s[4], /^assistant: call mem\.Pop/);
    }
    assert.deepEqual(sent[5].slice(0, sent[4].length), sent[4]);
  });
});

test("gnosh's judge sends the model back when a top todo it closed does not hold", { skip: !program && "GNOSH_BIN is not set" }, async () => {
  const script = [
    run('mem.Todo("ship the release").Push()', "mem"),
    run('mem.Todo("run the tests").Push()', "mem"),
    run('"go test ./... ok"'),
    run('mem.Pop("go test ./... passes")', "mem"),
    run('"published"'),
    run('mem.Pop("released")', "mem"),
    fauxAssistantMessage(fauxText("Released.")),
    fauxAssistantMessage(fauxText("I could not publish it: there is no registry here.")),
  ];
  const judge = (p: string) =>
    p === "goal-judge" ? JSON.stringify({ ok: false, basis: "evidence", reason: "nothing shows the release was published" }) : p === "name" ? "a-name" : ok;
  await withSession(script, judge, async ({ prompt, sent, asked }) => {
    await prompt("ship the release");
    assert.equal(sent.length, 8, sent.map((s) => s.at(-1)).join("\n"));
    assert.equal(asked.filter((a) => a === "goal-judge").length, 1, asked.join(","));
    // The request after "Released." ends with gnosh's words for the judge's verdict.
    assert.match(sent[7].at(-1) ?? "", /The check of `[a-z/-]+` found it not met: nothing shows the release was published/);
  });
});
