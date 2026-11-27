// A scripted pi session, with no model and no network: pi's own faux
// provider plays the model, and the requests pi sends it are recorded. The
// same script runs twice, once in plain pi and once with pi-gnosh, in a
// window small enough that the session must make room.
//
//   GNOSH_BIN=/path/to/gnosh node demo/run.ts
//
// With pi-gnosh the model plans in mem, works inside a frame, keeps what it
// found as a thought, and works out a sum on a Gno machine; when the window
// fills, rounds mem holds a record of slide out of what is sent (no
// summary, no model call), and the frame's work leaves when it is popped.
// Plain pi has to summarize instead.

import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fauxAssistantMessage, fauxProvider, fauxText, fauxToolCall } from "@earendil-works/pi-ai";
import { createAgentSession, DefaultResourceLoader, ModelRuntime, SessionManager, SettingsManager } from "@earendil-works/pi-coding-agent";

const here = dirname(dirname(fileURLToPath(import.meta.url)));

export interface Request {
  system: string;
  messages: string[];
  tokens: number;
}

export interface DemoResult {
  requests: Request[];
  /** The side requests gnosh's checks made, by purpose, with the scripted model's answer. */
  sides: string[];
  compactions: number;
  slides: number;
  stats: Record<string, number>;
  session: string[];
}

type Msg = { role: string; content: string | { type: string; text?: string; name?: string; arguments?: { code?: string } }[] };

function show(m: Msg): string {
  if (typeof m.content === "string") return `${m.role}: ${m.content}`;
  const parts = m.content.map((p) => (p.type === "toolCall" ? `call ${p.name} ${p.arguments?.code ?? (p.arguments as { command?: string })?.command ?? ""}` : (p.text ?? p.type)));
  return `${m.role}: ${parts.join(" | ")}`;
}


export async function runDemo(opts: { gnosh: boolean; program?: string; window?: number; reserve?: number; think?: boolean; logs?: boolean }): Promise<DemoResult> {
  const home = mkdtempSync(join(tmpdir(), "pi-gnosh-demo-"));
  const oldEnv = { ...process.env };
  const oldCwd = process.cwd();
  const statsFile = join(home, "stats.jsonl");
  process.env.PI_GNOSH_PROGRAM = opts.program ?? process.env.GNOSH_BIN ?? "gnosh";
  process.env.PI_GNOSH_RESERVE = String(opts.reserve ?? 2000);
  process.env.PI_GNOSH_STATS = statsFile;
  process.env.GNOSH_DATA_DIR = join(home, "data");
  process.env.XDG_CONFIG_HOME = join(home, "config");
  process.env.XDG_CACHE_HOME = join(home, "cache");
  const cwd = join(home, "project");
  const agentDir = join(home, "agent");
  mkdirSync(cwd);
  mkdirSync(agentDir);
  process.chdir(cwd);

  const faux = fauxProvider({ models: [{ id: "faux-small", contextWindow: opts.window ?? 14000, maxTokens: 1000 }] });
  const runtime = await ModelRuntime.create({ authPath: join(agentDir, "auth.json"), modelsPath: null, refreshOnCreate: false });
  runtime.registerNativeProvider(faux.provider);
  const settingsManager = SettingsManager.inMemory({ compaction: { enabled: true, reserveTokens: opts.reserve ?? 2000, keepRecentTokens: 1500 } });
  const loader = new DefaultResourceLoader({
    cwd,
    agentDir,
    settingsManager,
    additionalExtensionPaths: opts.gnosh ? [here] : [],
    noExtensions: !opts.gnosh,
    noSkills: true,
    noPromptTemplates: true,
  } as ConstructorParameters<typeof DefaultResourceLoader>[0]);
  await loader.reload();

  const requests: Request[] = [];
  const record = (c: { systemPrompt?: string; messages: unknown[] }) => {
    const all = c.messages as Msg[];
    const msgs = all.filter((m) => m.role !== "system");
    const system = all.filter((m) => m.role === "system").map((m) => (typeof m.content === "string" ? m.content : m.content.map((p) => p.text ?? "").join(""))).join("\n");
    const text = JSON.stringify(c.messages);
    requests.push({ system, messages: msgs.map(show), tokens: Math.ceil(text.length / 4) });
  };
  const run = (code: string, name?: string) => fauxAssistantMessage(fauxToolCall("gnovm_run", name ? { name, code } : { code }), { stopReason: "toolUse" });
  // A large read: pi's own bash tool prints n words.
  const read = (word: string, n: number) =>
    fauxAssistantMessage(fauxToolCall("bash", { command: `for i in $(seq 1 ${n}); do printf '${word} '; done` }), { stopReason: "toolUse" });
  // The same size of read as lines of a build log, for a demo whose work
  // must not look like a secret to mem (the bridge's automatic keep).
  const work = (n: number) =>
    opts.logs
      ? fauxAssistantMessage(fauxToolCall("bash", { command: `for i in $(seq 1 ${n / 7}); do echo "step $i of the build passed in 41 ms"; done` }), { stopReason: "toolUse" })
      : read("work", n);
  const script = [
    read("explored", 1400), // large reads before any plan
    read("explored", 1400),
    run('mem.Todo("fix the port").Push()', "mem"),
    work(1400), // work inside the frame
    work(1400),
    ...(opts.think === false ? [] : [run('mem.Think("setup binds a fixed port; picking a free one fixes it")', "mem")]), // what the work found, kept before it can slide
    work(1400),
    run("17 * 23 + 4"), // the sum, worked out on a Gno machine
    run('mem.Pop("the port is picked free in setup; 17*23+4 = 395")', "mem"),
    fauxAssistantMessage(fauxText("done: the port is fixed")),
  ];
  // gnosh's checks ask the model too (the close review, the goal judge,
  // the namer), each with a system prompt of its own: the scripted model
  // answers them as a model would that finds the work done.
  const sides: string[] = [];
  const side = (system: string): string | undefined => {
    if (system.startsWith("You name notes")) return "port-fix";
    if (system.startsWith("You check whether an AI coding agent did the todo")) return '{"ok": true, "reason": "the commands in the todo show it"}';
    if (system.startsWith("You check whether an AI coding agent has reached the goal")) return '{"ok": true, "basis": "evidence", "reason": "the conversation shows it"}';
    return undefined;
  };
  // Each scripted step answers one request; a summary request in between
  // is answered with a summary and the step waits for the next request.
  const steps: ((c: { systemPrompt?: string; messages: unknown[] }) => ReturnType<typeof fauxAssistantMessage>)[] = [];
  let k = 0;
  const next = (c: { systemPrompt?: string; messages: unknown[] }) => {
    const sys = (c.messages as Msg[]).filter((m) => m.role === "system").map((m) => (typeof m.content === "string" ? m.content : m.content.map((p) => p.text ?? "").join(""))).join("\n");
    const answer = side(sys);
    if (answer !== undefined) {
      sides.push(`${sys.slice(0, 60)}… → ${answer}`);
      return fauxAssistantMessage(fauxText(answer));
    }
    record(c);
    const lastMsg = JSON.stringify(c.messages.at(-1) ?? "");
    if (/<conversation>/.test(lastMsg)) {
      // pi asks for a summary: plain pi's compaction.
      return fauxAssistantMessage(fauxText("## Goal\nFix the port.\n## Progress\nExplored the repo; planned the fix."));
    }
    return script[Math.min(k++, script.length - 1)];
  };
  for (let i = 0; i < 60; i++) steps.push(next);
  faux.setResponses(steps);

  const sessionManager = SessionManager.inMemory(cwd);
  const { session, extensionsResult } = await createAgentSession({
    cwd,
    agentDir,
    modelRuntime: runtime,
    model: faux.getModel(),
    resourceLoader: loader,
    sessionManager,
    settingsManager,
  });
  if (extensionsResult.errors.length) throw new Error(JSON.stringify(extensionsResult.errors));
  let shut = false;
  const shutdown = async () => {
    if (shut) return;
    shut = true;
    // pi's runtime says session_shutdown before it disposes of a session.
    await (session.extensionRunner as unknown as { emit(e: unknown): Promise<unknown> }).emit({ type: "session_shutdown", reason: "quit" });
  };
  try {
    await session.prompt("fix the port");
    await shutdown();
    const entries = sessionManager.getEntries() as { type: string; customType?: string }[];
    let stats: Record<string, number> = {};
    try {
      const lines = readFileSync(statsFile, "utf8").trim().split("\n");
      stats = JSON.parse(lines.at(-1) ?? "{}");
    } catch {
      // plain pi writes none
    }
    return {
      requests,
      sides,
      compactions: entries.filter((e) => e.type === "compaction").length,
      slides: entries.filter((e) => e.type === "custom" && e.customType === "gnosh-slides").length,
      stats,
      session: entries.map((e) => e.type + (e.customType ? `:${e.customType}` : "")),
    };
  } finally {
    await shutdown();
    session.dispose();
    process.chdir(oldCwd);
    process.env = oldEnv;
    rmSync(home, { recursive: true, force: true });
  }
}

function report(name: string, r: DemoResult) {
  console.log(`\n=== ${name}: ${r.requests.length} requests, ${r.compactions} compactions, ${r.slides} slides`);
  r.requests.forEach((q, i) => {
    const short = q.messages.map((m) => (m.length > 90 ? m.slice(0, 87) + "..." : m));
    console.log(`--- request ${i + 1} (~${q.tokens} tokens)`);
    for (const m of short) console.log("   " + m);
  });
  for (const sd of r.sides) console.log(`--- side request: ${sd}`);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const plain = await runDemo({ gnosh: false });
  report("plain pi (its own compaction)", plain);
  const g = await runDemo({ gnosh: true });
  report("pi + pi-gnosh (mem frames + slide)", g);
  console.log(`\nplain pi: ${plain.compactions} compactions; pi-gnosh: ${g.compactions} compactions, ${g.slides} slides, stats ${JSON.stringify(g.stats)}`);
}
