// pi-gnosh: gnosh's mem stack and Gno machines in pi, in place of pi's
// compaction.
//
// The extension holds none of gnosh's logic. It starts `gnosh bridge` for
// the session's folder and, through it (gnosh's spec/gnosh/bridge.md):
//
// - gives pi's model gnosh's machine tools (gnovm_run and the rest) and the
//   words a gnosh session tells its model of mem (prompt);
// - before every request, sends what gnosh's own view keeps of the
//   conversation: the work of a popped frame is gone, and so are the
//   rounds a slide took out (view);
// - where pi would compact, slides instead: the oldest rounds leave the
//   view, mem holding the plan, results and thoughts, with no summary and
//   no model call (room). pi compacts only when nothing can slide, as a
//   gnosh session does;
// - between two rounds adds the notes a gnosh session adds (step).
//
// mem and its frames are gnosh's, built on gnosh: https://gnosh.ai

import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { appendFileSync } from "node:fs";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { Bridge, BridgeCallError } from "./bridge.ts";
import { FRAMES, fromView, type PiMessage, toEntries } from "./map.ts";
import type { Block, Entry, Reply } from "./protocol.ts";

const CLIENT = "pi-gnosh 0.2.0";
/** The custom type of the notes this extension adds to the conversation, and of its kept slides. */
const NOTE = "gnosh";
const SLIDES = "gnosh-slides";

function piContent(blocks: Block[] | undefined) {
  return (blocks ?? []).map((b) =>
    b.type === "image" ? { type: "image" as const, data: b.data ?? "", mimeType: b.media_type ?? "image/png" } : { type: "text" as const, text: b.text ?? "" },
  );
}

/** reserveTokens is pi's own compaction.reserveTokens, read from the settings pi reads. */
function reserveTokens(cwd: string): number {
  const env = Number(process.env.PI_GNOSH_RESERVE);
  if (env > 0) return env;
  const agentDir = process.env.PI_CODING_AGENT_DIR || join(homedir(), ".pi", "agent");
  let reserve = 16384;
  for (const f of [join(agentDir, "settings.json"), join(cwd, ".pi", "settings.json")]) {
    try {
      const r = JSON.parse(readFileSync(f, "utf8"))?.compaction?.reserveTokens;
      if (typeof r === "number" && r > 0) reserve = r;
    } catch {
      // no such file
    }
  }
  return reserve;
}

export default async function (pi: ExtensionAPI) {
  pi.registerFlag("gnosh", { description: "The gnosh program that serves mem (default: gnosh on PATH)", type: "string" });

  const program = () => (pi.getFlag("gnosh") as string | undefined) || process.env.PI_GNOSH_PROGRAM || "gnosh";
  const cwd = process.cwd();
  let bridge: Bridge | undefined;
  let starting: Promise<Bridge> | undefined;
  let session = "";

  // What this extension keeps of the session: the slides gnosh planned (sent
  // back in every view), whether the session has had a prompt yet, the
  // conversation as the last request had it, and whether a slide was made
  // since the model last answered (pi's own count is from before it).
  let slides: Entry[] = [];
  let prompted = false;
  let last: PiMessage[] = [];
  let slidSinceReply = false;
  const stats = { slides: 0, freed: 0, compactions: 0, cancelled: 0, notes: 0, pushes: 0, pops: 0, calls: 0 };
  const statsFile = process.env.PI_GNOSH_STATS;
  const log = (line: string) => {
    process.stderr.write(`pi-gnosh: ${line}\n`);
    if (statsFile) {
      try {
        appendFileSync(statsFile, JSON.stringify({ t: Date.now(), line, ...stats }) + "\n");
      } catch {
        // stats are best effort
      }
    }
  };

  async function ensure(): Promise<Bridge> {
    if (bridge?.alive) return bridge;
    starting ??= Bridge.start({ program: program(), cwd, client: CLIENT, log: (l) => process.stderr.write(`gnosh: ${l}\n`) }).finally(() => (starting = undefined));
    bridge = await starting;
    return bridge;
  }

  // The tools are the bridge's; it is asked for them once, at load.
  let tools: Reply["tools"] = [];
  let ops: string[] = [];
  try {
    const b = await ensure();
    ops = b.hello?.ops ?? [];
    tools = (await b.call({ op: "tools" })).tools ?? [];
  } catch (e) {
    process.stderr.write(`pi-gnosh: gnosh bridge is not available, so mem is not either: ${(e as Error).message}\n`);
    return;
  }
  const has = (op: string) => ops.includes(op);

  const sessionOf = (ctx: ExtensionContext) => (session ||= ctx.sessionManager.getSessionId());
  pi.on("session_start", async (_e, ctx) => {
    session = ctx.sessionManager.getSessionId();
    // A session opened again keeps the slides it had.
    slides = [];
    for (const e of ctx.sessionManager.getEntries() as { type: string; customType?: string; data?: unknown }[]) {
      if (e.type === "custom" && e.customType === SLIDES && Array.isArray(e.data)) slides.push(...(e.data as Entry[]));
    }
    prompted = slides.length > 0 || ctx.sessionManager.getEntries().some((e) => (e as { type: string }).type === "message");
  });
  pi.on("session_shutdown", async () => {
    log(`end ${JSON.stringify(stats)}`);
    bridge?.close();
    bridge = undefined;
  });

  for (const t of tools ?? []) {
    pi.registerTool({
      name: t.name,
      label: t.name,
      description: t.description,
      parameters: Type.Unsafe(t.input_schema),
      async execute(callId, params, _signal, _onUpdate, ctx) {
        const req = { op: "call", session: sessionOf(ctx), call_id: callId, name: t.name, input: params };
        let r: Reply;
        try {
          r = await (await ensure()).call(req);
        } catch (e) {
          if (!(e instanceof BridgeCallError) || e.code !== "confirm") throw e;
          const yes = ctx.hasUI && (await ctx.ui.confirm(t.name, e.reply.error?.summary ?? "This call cannot be undone."));
          if (!yes) {
            return { content: [{ type: "text", text: "The person did not approve this call, so it did not run." }], details: {}, isError: true };
          }
          r = await (await ensure()).call({ ...req, confirmed: true });
        }
        stats.calls++;
        for (const f of r.frames ?? []) f.kind === "push" ? stats.pushes++ : stats.pops++;
        return { content: piContent(r.content), details: { [FRAMES]: r.frames ?? [] }, isError: !!r.is_error };
      },
    });
  }

  /** entriesOf maps the conversation to gnosh's entries, the kept slides after it. */
  const entriesOf = (msgs: PiMessage[], ctx: ExtensionContext) => {
    const m = toEntries(msgs, sessionOf(ctx));
    return { ...m, entries: [...m.entries, ...slides] };
  };

  // The part about mem goes after pi's system prompt, the same bytes every
  // time; the mem line (and the frame line, in a frame) after the prompt.
  if (has("prompt")) {
    pi.on("before_agent_start", async (event, ctx) => {
      const r = await (await ensure()).call({ op: "prompt", session: sessionOf(ctx), first: !prompted, entries: entriesOf(last, ctx).entries });
      prompted = true;
      const parts = (r.parts ?? []).filter((p) => p.trim());
      return {
        systemPrompt: r.system ? `${event.systemPrompt}\n\n${r.system}` : undefined,
        message: parts.length ? { customType: NOTE, content: parts.join("\n\n"), display: false } : undefined,
      };
    });
  }

  // What the model is sent: gnosh's view of the conversation. A request
  // with no frame and no slide in it is left as pi has it.
  pi.on("context", async (event, ctx) => {
    const msgs = event.messages as unknown as PiMessage[];
    last = msgs;
    const { entries, byUuid } = entriesOf(msgs, ctx);
    if (!entries.some((e) => e.type === "frame")) return undefined;
    const view = await (await ensure()).call({ op: "view", session: sessionOf(ctx), entries });
    return { messages: fromView({ messages: view.messages ?? [], sources: view.sources ?? [] }, byUuid) as unknown as typeof event.messages };
  });

  pi.on("message_end", async (event) => {
    if ((event.message as { role?: string }).role === "assistant") slidSinceReply = false;
  });

  /** point is where pi compacts: its window less its reserve. */
  const point = (ctx: ExtensionContext) => {
    const u = ctx.getContextUsage();
    return u ? { window: u.contextWindow, point: u.contextWindow - reserveTokens(cwd), used: u.tokens ?? 0 } : undefined;
  };

  /** slide asks gnosh to make room; it returns the notes for the model, or undefined when nothing slid. */
  async function slide(ctx: ExtensionContext, used: number): Promise<string[] | undefined> {
    const p = point(ctx);
    if (!p || !has("room")) return undefined;
    const need = Math.max(1, used - p.point + Math.floor(p.window / 10));
    const { entries } = entriesOf(last, ctx);
    const prompt = [...entries].reverse().find((e) => e.type === "user" && e.source === "prompt")?.uuid;
    const r = await (await ensure()).call({ op: "room", session: sessionOf(ctx), entries, need, prompt, edit_tools: ["edit", "write"] });
    if (!r.freed || !r.new_slides?.length) return undefined;
    slides.push(...r.new_slides);
    pi.appendEntry(SLIDES, r.new_slides);
    slidSinceReply = true;
    stats.slides++;
    stats.freed += r.freed;
    log(`slid instead of compacting at ~${used} tokens (point ${p.point}): ${r.notice ?? ""}`);
    return r.parts ?? [];
  }

  // Where pi would compact, gnosh slides; pi compacts only when nothing can.
  pi.on("session_before_compact", async (event, ctx) => {
    if (event.reason === "manual") return undefined;
    if (slidSinceReply) {
      // pi's count is from before the slide; the next request is smaller.
      stats.cancelled++;
      return { cancel: true };
    }
    if (event.reason === "threshold") {
      const notes = await slide(ctx, event.preparation.tokensBefore);
      if (notes) {
        stats.cancelled++;
        for (const n of notes) pi.sendMessage({ customType: NOTE, content: n, display: false }, { deliverAs: "steer" });
        stats.notes += notes.length;
        return { cancel: true };
      }
    }
    stats.compactions++;
    log(`pi compacts (${event.reason}): nothing could slide`);
    return undefined;
  });

  // Between two rounds: the slide when over pi's point (before pi's own
  // check), and the notes a gnosh session adds before the next reply.
  pi.on("turn_end", async (event, ctx) => {
    if (!event.toolResults?.length) return undefined;
    const p = point(ctx);
    if (!p) return undefined;
    const notes: string[] = [];
    if (p.used > p.point && !slidSinceReply) {
      // last is the request just answered; the results came after it.
      last = [...last, event.message as unknown as PiMessage, ...(event.toolResults as unknown as PiMessage[])];
      notes.push(...((await slide(ctx, p.used)) ?? []));
    }
    if (has("step")) {
      const msgs = [...last];
      if (!msgs.includes(event.message as unknown as PiMessage)) msgs.push(event.message as unknown as PiMessage, ...(event.toolResults as unknown as PiMessage[]));
      const r = await (await ensure()).call({ op: "step", session: sessionOf(ctx), entries: entriesOf(msgs, ctx).entries, used: p.used, point: p.point });
      notes.push(...(r.parts ?? []));
    }
    if (!notes.length) return undefined;
    stats.notes += notes.length;
    return { entries: notes.map((n) => ({ type: "custom_message" as const, customType: NOTE, content: n, display: false })) };
  });
}
