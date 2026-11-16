// pi-gnosh: gnosh's Gno machines, mem and frames in pi.
//
// The extension holds none of gnosh's logic. It starts `gnosh bridge` for
// the session's folder, gives pi's model the tools the bridge lists, runs
// their calls through it, and before every request hands the bridge the
// conversation and sends what gnosh's own view keeps of it. mem and its
// frames are gnosh's, built on gnosh: https://gnosh.ai

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { Bridge, BridgeCallError } from "./bridge.ts";
import { FRAMES, fromView, type PiMessage, toEntries } from "./map.ts";
import type { Block, Reply } from "./protocol.ts";

const CLIENT = "pi-gnosh 0.1.0";

function piContent(blocks: Block[] | undefined) {
  return (blocks ?? []).map((b) =>
    b.type === "image" ? { type: "image" as const, data: b.data ?? "", mimeType: b.media_type ?? "image/png" } : { type: "text" as const, text: b.text ?? "" },
  );
}

export default async function (pi: ExtensionAPI) {
  pi.registerFlag("gnosh", { description: "The gnosh program that serves mem (default: gnosh on PATH)", type: "string" });

  const program = () => (pi.getFlag("gnosh") as string | undefined) || process.env.PI_GNOSH_PROGRAM || "gnosh";
  const cwd = process.cwd();
  let bridge: Bridge | undefined;
  let starting: Promise<Bridge> | undefined;
  let session = "";

  async function ensure(): Promise<Bridge> {
    if (bridge?.alive) return bridge;
    starting ??= Bridge.start({ program: program(), cwd, client: CLIENT, log: (l) => process.stderr.write(`gnosh: ${l}\n`) }).finally(() => (starting = undefined));
    bridge = await starting;
    return bridge;
  }

  // The tools are the bridge's; it is asked for them once, at load.
  let tools: Reply["tools"] = [];
  try {
    tools = (await (await ensure()).call({ op: "tools" })).tools ?? [];
  } catch (e) {
    process.stderr.write(`pi-gnosh: gnosh bridge is not available, so mem is not either: ${(e as Error).message}\n`);
    return;
  }

  const sessionOf = (ctx: ExtensionContext) => (session ||= ctx.sessionManager.getSessionId());
  pi.on("session_start", async (_e, ctx) => {
    session = ctx.sessionManager.getSessionId();
  });
  pi.on("session_shutdown", async () => {
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
        return { content: piContent(r.content), details: { [FRAMES]: r.frames ?? [] }, isError: !!r.is_error };
      },
    });
  }

  // What the model is sent: gnosh's view of the conversation. A request
  // with no frame in it is left as pi has it.
  pi.on("context", async (event, ctx) => {
    const msgs = event.messages as unknown as PiMessage[];
    const { entries, byUuid } = toEntries(msgs, sessionOf(ctx));
    if (!entries.some((e) => e.type === "frame")) return undefined;
    const view = await (await ensure()).call({ op: "view", session: sessionOf(ctx), entries });
    return { messages: fromView({ messages: view.messages ?? [], sources: view.sources ?? [] }, byUuid) as unknown as typeof event.messages };
  });
}
