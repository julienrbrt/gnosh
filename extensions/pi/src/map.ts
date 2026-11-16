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

/** toEntries maps one request's pi messages to gnosh entries for session. */
export function toEntries(msgs: readonly PiMessage[], session: string): Mapped {
  const entries: Entry[] = [];
  const byUuid = new Map<string, PiMessage[]>();
  const add = (e: Omit<Entry, "uuid">, from: PiMessage[]) => {
    const uuid = `m${entries.length}`;
    entries.push({ ...e, uuid } as Entry);
    byUuid.set(uuid, from);
  };
  for (let i = 0; i < msgs.length; i++) {
    const m = msgs[i];
    if (m.role === "user") {
      add({ type: "user", source: "prompt", message: { role: "user", content: userBlocks(m.content) } }, [m]);
    } else if (m.role === "assistant") {
      const content: Block[] = [];
      for (const c of (Array.isArray(m.content) ? m.content : []) as Content[]) {
        if (c.type === "text") content.push({ type: "text", text: c.text ?? "" });
        else if (c.type === "thinking") content.push({ type: "thinking", text: c.thinking ?? "" });
        else if (c.type === "toolCall") content.push({ type: "tool_use", id: c.id, name: c.name, input: c.arguments ?? {} });
      }
      const calls = content.some((b) => b.type === "tool_use");
      add({ type: "assistant", stop: calls ? "tool_use" : "end_turn", message: { role: "assistant", content } }, [m]);
    } else if (m.role === "toolResult") {
      const group: PiMessage[] = [];
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
      );
      for (const r of group) {
        for (const f of framesOf(r)) {
          if (f.session !== session) continue;
          const common = { type: "frame" as const, sessionId: session, frame: f.id, label: f.purpose ?? "", toolUseId: r.toolCallId };
          add(f.kind === "push" ? { ...common, op: "push" } : { ...common, op: "pop", result: f.result ?? "" }, []);
        }
      }
    } else if ((m as { excludeFromContext?: boolean }).excludeFromContext !== true) {
      add({ type: "user", source: "notice", message: { role: "user", content: [{ type: "text", text: noticeText(m) }] } }, [m]);
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
