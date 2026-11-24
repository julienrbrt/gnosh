// pi's messages to gnosh's entries, and gnosh's view back to pi's messages
// (gnosh's spec/gnosh/bridge.md §7.2 and §7.3).
//
// Every pi message keeps its identity. The view only chooses which of them
// go, in what order, and may add a note to a tool result (what a pop or a
// slide took out). So what pi alone knows of a message (a provider's
// signatures, usage, its own metadata) is sent as it was, and the prefix a
// provider cached stays byte for byte the same.

import type { Block, Entry, Frame, Message } from "./protocol.ts";

// The pi messages this file reads. pi's own types are wider; only these
// fields count here.
type Content = { type: string; text?: string; thinking?: string; thinkingSignature?: string; id?: string; name?: string; arguments?: unknown; data?: string; mimeType?: string };
export interface PiMessage {
  role: string;
  content?: string | Content[];
  toolCallId?: string;
  isError?: boolean;
  stopReason?: string;
  details?: unknown;
  summary?: string;
  command?: string;
  output?: string;
  [k: string]: unknown;
}

/** The detail key under which a tool result keeps the frames its call made. */
export const FRAMES = "gnoshFrames";

export function framesOf(m: PiMessage): Frame[] {
  const d = m.details as Record<string, unknown> | undefined;
  const f = d?.[FRAMES];
  return Array.isArray(f) ? (f as Frame[]) : [];
}

function textOf(content: PiMessage["content"]): string {
  if (content === undefined) return "";
  if (typeof content === "string") return content;
  return content
    .filter((c) => c.type === "text")
    .map((c) => c.text ?? "")
    .join("");
}

function userBlocks(content: PiMessage["content"]): Block[] {
  if (typeof content === "string" || content === undefined) return [{ type: "text", text: content ?? "" }];
  return content.map((c): Block => (c.type === "image" ? { type: "image", media_type: c.mimeType, data: c.data } : { type: "text", text: c.text ?? "" }));
}

/** noticeText is what a pi message that is neither a prompt nor a reply nor a result says to the model. */
function noticeText(m: PiMessage): string {
  switch (m.role) {
    case "compactionSummary":
    case "branchSummary":
      return String(m.summary ?? "");
    case "bashExecution":
      return `$ ${m.command ?? ""}\n${m.output ?? ""}`;
    default:
      return textOf(m.content) || `(${m.role})`;
  }
}

export interface Mapped {
  entries: Entry[];
  /** For each entry's uuid, the pi messages it stands for, in order. */
  byUuid: Map<string, PiMessage[]>;
}

/**
 * pi's tools that gnosh has under other names. gnosh's checks read a
 * conversation by its own names: a command run with Bash, a file shown by
 * Read and changed by Edit or Write, named by `file_path` (close-check.md
 * §3), and so does a slide's line of the files edited.
 */
const GNOSH_NAMES: Record<string, string> = { bash: "Bash", read: "Read", edit: "Edit", write: "Write" };
const FILE_TOOLS = new Set(["read", "edit", "write"]);

/** stampOf is a message's time as gnosh writes it, or undefined. */
function stampOf(m: PiMessage): string | undefined {
  const ts = m.timestamp;
  const ms = typeof ts === "number" ? ts : typeof ts === "string" ? Date.parse(ts) : NaN;
  return Number.isFinite(ms) ? new Date(ms).toISOString() : undefined;
}

/**
 * keyOf is a message's own name, the same in every request of the session
 * while the message is in it: a slide names entries by uuid and is sent
 * again in every later view, so a uuid must not move when messages before
 * it go (a pi summary) or come.
 */
function keyOf(m: PiMessage, i: number): string {
  if (m.role === "toolResult" && m.toolCallId) return `r:${m.toolCallId}`;
  if (m.role === "assistant" && Array.isArray(m.content)) {
    const call = m.content.find((c) => c.type === "toolCall" && c.id);
    if (call) return `a:${call.id}`;
  }
  const ts = m.timestamp;
  return typeof ts === "number" || typeof ts === "string" ? `${m.role[0]}:${ts}` : `i:${i}`;
}

/** toEntries maps one request's pi messages to gnosh entries for session. */
export function toEntries(msgs: readonly PiMessage[], session: string): Mapped {
  const entries: Entry[] = [];
  const byUuid = new Map<string, PiMessage[]>();
  const add = (e: Omit<Entry, "uuid">, from: PiMessage[], key: string, at?: PiMessage) => {
    let uuid = key;
    for (let n = 2; byUuid.has(uuid); n++) uuid = `${key}#${n}`;
    const timestamp = stampOf(at ?? from[0] ?? ({} as PiMessage));
    entries.push({ ...e, uuid, ...(timestamp ? { timestamp } : {}) } as Entry);
    byUuid.set(uuid, from);
  };
  for (let i = 0; i < msgs.length; i++) {
    const m = msgs[i];
    if (m.role === "user") {
      add({ type: "user", source: "prompt", message: { role: "user", content: userBlocks(m.content) } }, [m], keyOf(m, i));
    } else if (m.role === "assistant") {
      const content: Block[] = [];
      for (const c of (Array.isArray(m.content) ? m.content : []) as Content[]) {
        if (c.type === "text") content.push({ type: "text", text: c.text ?? "" });
        else if (c.type === "thinking") content.push({ type: "thinking", text: c.thinking ?? "" });
        else if (c.type === "toolCall") {
          const args = (c.arguments ?? {}) as Record<string, unknown>;
          const input = FILE_TOOLS.has(c.name ?? "") && typeof args.path === "string" ? { ...args, file_path: args.path } : args;
          content.push({ type: "tool_use", id: c.id, name: GNOSH_NAMES[c.name ?? ""] ?? c.name, input });
        }
      }
      const calls = content.some((b) => b.type === "tool_use");
      add({ type: "assistant", stop: calls ? "tool_use" : "end_turn", message: { role: "assistant", content } }, [m], keyOf(m, i));
    } else if (m.role === "toolResult") {
      const group: PiMessage[] = [];
      const first = i;
      while (i < msgs.length && msgs[i].role === "toolResult") group.push(msgs[i++]);
      i--;
      add(
        {
          type: "user",
          source: "tool_results",
          message: {
            role: "user",
            content: group.map((r) => ({ type: "tool_result", tool_use_id: r.toolCallId, is_error: !!r.isError, content: userBlocks(r.content) })),
          },
        },
        group,
        keyOf(group[0], first),
      );
      for (const r of group) {
        framesOf(r).forEach((f, k) => {
          if (f.session !== session) return;
          const common = { type: "frame" as const, sessionId: session, frame: f.id, label: f.purpose ?? "", toolUseId: r.toolCallId };
          add(f.kind === "push" ? { ...common, op: "push" } : { ...common, op: "pop", result: f.result ?? "" }, [], `f:${r.toolCallId}:${k}`, r);
        });
      }
    } else if ((m as { excludeFromContext?: boolean }).excludeFromContext !== true) {
      add({ type: "user", source: "notice", message: { role: "user", content: [{ type: "text", text: noticeText(m) }] } }, [m], keyOf(m, i));
    }
  }
  return { entries, byUuid };
}

/** fromView maps a view back to pi messages, reusing pi's own wherever the view kept one. */
export function fromView(view: { messages: Message[]; sources: string[][] }, byUuid: Map<string, PiMessage[]>): PiMessage[] {
  const out: PiMessage[] = [];
  const sent = new Set<PiMessage>();
  view.messages.forEach((vm, k) => {
    const from = (view.sources[k] ?? []).flatMap((u) => byUuid.get(u) ?? []);
    if (from.length === 0) {
      // A message the view made up itself.
      out.push({ role: "user", content: vm.content.filter((b) => b.type === "text").map((b) => ({ type: "text", text: b.text ?? "" })), timestamp: Date.now() });
      return;
    }
    const results = new Map(vm.content.filter((b) => b.type === "tool_result").map((b) => [b.tool_use_id, b]));
    for (const m of from) {
      if (sent.has(m)) continue;
      if (m.role !== "toolResult") {
        out.push(m);
        sent.add(m);
        continue;
      }
      const b = results.get(m.toolCallId);
      if (!b) continue; // this result left the view
      sent.add(m);
      const had = textOf(m.content);
      const now = (b.content ?? []).filter((c) => c.type === "text").map((c) => c.text ?? "").join("");
      const added = now.startsWith(had) ? now.slice(had.length) : "";
      if (!added) {
        out.push(m);
        continue;
      }
      const content = typeof m.content === "string" ? [{ type: "text", text: m.content }] : [...(m.content ?? [])];
      out.push({ ...m, content: [...content, { type: "text", text: added }] });
    }
  });
  return out;
}

/**
 * staleUsage takes the usage off the replies the view sends after the
 * first message it left out. pi sizes a request from the last reply's usage
 * plus an estimate of what came after it (pi-ai's estimateContextTokens),
 * and caps the answer by what it thinks is left of the window. A reply
 * from before a pop or slide reports the context as it was then, so after
 * a large cut pi would think the window nearly full and cap the answer at a
 * token or two: in the first benchmark one run ended on a reply cut off at
 * one token with 29K of a 64K window in use. The replies before the cut
 * keep their usage, which still describes the prefix in front of them;
 * after it pi estimates from the text. Only the copies sent are changed:
 * pi's session keeps the replies as they were.
 */
export function staleUsage(sent: PiMessage[], had: readonly PiMessage[]): PiMessage[] {
  const same = (a: PiMessage, b: PiMessage | undefined) =>
    !!b && (a === b || (a.role === "toolResult" && b.role === "toolResult" && a.toolCallId === b.toolCallId));
  let cut = 0;
  while (cut < sent.length && same(sent[cut], had[cut])) cut++;
  if (cut >= had.length) return sent;
  return sent.map((m, i) => (i >= cut && m.role === "assistant" && m.usage ? { ...m, usage: { ...(m.usage as object), input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0 } } : m));
}
