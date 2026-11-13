// Frames in a pi session: the stack a session's mem pushes and pops, read
// from its gnovm_run results, and the cut a pop makes in what the model is
// sent. A port of gnosh's internal/agent/frames.go, the parts of it that
// need no gnosh: frameEvents, apply, poppedRows, turnStart, workNote and
// cutPopped. Slides are left out.
//
// Nothing here edits the session: the cut is made again, the same, in each
// request's copy of the messages (pi's "context" event), so every request
// after a pop sends the same list and a provider's prompt cache of the
// prefix through the push holds.

import type { MemFrame } from "./wire.ts";

export const RUN_TOOL = "gnovm_run";

// The message shapes this needs, of pi's AgentMessage.
export interface TextPart {
  type: "text";
  text: string;
}
export interface ToolCallPart {
  type: "toolCall";
  id: string;
  name: string;
}
export interface Msg {
  role: string;
  content?: unknown;
  toolCallId?: string;
  toolName?: string;
  details?: unknown;
  isError?: boolean;
  stopReason?: string;
}

// RunDetails is what gnovm_run keeps with its result (a tool result's
// details), from which a session's frames are read again.
export interface RunDetails {
  vm: string;
  frames?: MemFrame[];
}

export interface StackFrame {
  id: string;
  purpose: string;
  callID: string;
}

export interface PoppedRange {
  frame: string;
  pushCallID: string;
  popCallID: string;
}

export interface Frames {
  stack: StackFrame[];
  pops: PoppedRange[];
}

function toolCalls(m: Msg): ToolCallPart[] {
  if (m.role !== "assistant" || !Array.isArray(m.content)) {
    return [];
  }
  return (m.content as { type?: string }[]).filter((p): p is ToolCallPart => p?.type === "toolCall");
}

function hasToolCall(m: Msg, callID: string): boolean {
  return toolCalls(m).some((c) => c.id === callID);
}

// frameEvents is the frames a gnovm_run result pushed and popped.
export function frameEvents(m: Msg): MemFrame[] {
  if (m.role !== "toolResult" || m.toolName !== RUN_TOOL || m.isError) {
    return [];
  }
  const d = m.details as RunDetails | undefined;
  return Array.isArray(d?.frames) ? d.frames : [];
}

// framesOf is the stack and the pops of a session, from its messages in
// order (its branch, the compacted ones too). Only the session's own frames
// count: mem is shared, and a result may tell of another session's.
export function framesOf(msgs: readonly Msg[], session: string): Frames {
  const stack: StackFrame[] = [];
  const pops: PoppedRange[] = [];
  for (const m of msgs) {
    for (const ev of frameEvents(m)) {
      if (ev.session && session && ev.session !== session) {
        continue;
      }
      if (ev.kind === "push") {
        stack.push({ id: ev.id, purpose: ev.purpose ?? "", callID: m.toolCallId ?? "" });
        continue;
      }
      for (let i = stack.length - 1; i >= 0; i--) {
        if (stack[i].id !== ev.id) {
          continue;
        }
        pops.push({ frame: ev.id, pushCallID: stack[i].callID, popCallID: m.toolCallId ?? "" });
        stack.splice(i, 1);
        break;
      }
    }
  }
  return { stack, pops };
}

function indexOfCall(msgs: readonly Msg[], callID: string): number {
  return callID === "" ? -1 : msgs.findIndex((m) => hasToolCall(m, callID));
}

// turnStart is the index of the first message of the turn the message at
// end is in: a turn begins with a prompt typed after the model's answer (an
// assistant message with no tool call, or one that was aborted or failed),
// or at the start; a prompt steered into a running turn belongs to it.
export function turnStart(msgs: readonly Msg[], end: number): number {
  for (let i = end - 1; i >= 0; i--) {
    if (msgs[i].role !== "user") {
      continue;
    }
    let j = i - 1;
    while (j >= 0 && msgs[j].role === "user") {
      j--;
    }
    if (j < 0) {
      return 0;
    }
    const a = msgs[j];
    if (a.role !== "assistant" && a.role !== "toolResult") {
      return j + 1;
    }
    if (a.role === "assistant" && (toolCalls(a).length === 0 || a.stopReason === "aborted" || a.stopReason === "error")) {
      return j + 1;
    }
  }
  return 0;
}

interface Span {
  from: number; // first index cut
  end: number; // the pop's assistant message
  answer: number; // the push's answer, or -1
  spareFrom: number; // user messages from here on stay
}

// span is where range r lies in msgs, or undefined when it cuts nothing.
function span(msgs: readonly Msg[], r: PoppedRange): Span | undefined {
  const end = indexOfCall(msgs, r.popCallID);
  if (end < 0) {
    return undefined;
  }
  let from = -1;
  let answer = -1;
  const push = indexOfCall(msgs, r.pushCallID);
  if (push >= 0) {
    if (push === end) {
      return undefined;
    }
    // The push's round stays: its assistant message and every tool result
    // answering it, so no call is left without its answer.
    from = push + 1;
    const ids = new Set(toolCalls(msgs[push]).map((c) => c.id));
    while (from < end && msgs[from].role === "toolResult" && ids.has(msgs[from].toolCallId ?? "")) {
      if (msgs[from].toolCallId === r.pushCallID) {
        answer = from;
      }
      from++;
    }
  } else {
    // The push is behind a compaction: the cut starts at the model's
    // first message.
    from = msgs.findIndex((m) => m.role === "assistant");
    if (from < 0 || from >= end) {
      return undefined;
    }
  }
  return { from, end, answer, spareFrom: Math.max(from, turnStart(msgs, end)) };
}

// workNote is what a push's answer adds once the messages after it are
// cut: that work came there, and how much, by tool, so a pop never looks as
// if it came straight after its push. Without it a long session's context
// is a row of pushes each answered at once by its pop, and a model copies
// the row, popping todos it never did.
export function workNote(rows: readonly Msg[]): string {
  const why = "removed from your context when you popped it";
  const byTool = new Map<string, number>();
  let calls = 0;
  for (const m of rows) {
    for (const c of toolCalls(m)) {
      byTool.set(c.name, (byTool.get(c.name) ?? 0) + 1);
      calls++;
    }
  }
  if (calls === 0) {
    return `[Then came ${rows.length} messages of work in it, ${why}.]`;
  }
  const counts = [...byTool.keys()].sort().map((n) => `${n} ${byTool.get(n)}`);
  const what = calls === 1 ? "1 tool call" : `${calls} tool calls`;
  return `[Then came the work in it, ${what} (${counts.join(", ")}), ${why}.]`;
}

function withNote(m: Msg, text: string): Msg {
  const content = Array.isArray(m.content) ? [...(m.content as unknown[])] : [];
  for (let i = content.length - 1; i >= 0; i--) {
    const p = content[i] as TextPart;
    if (p?.type === "text") {
      content[i] = { ...p, text: p.text + "\n" + text };
      return { ...m, content };
    }
  }
  content.push({ type: "text", text });
  return { ...m, content };
}

// cutPopped is msgs, the messages a request sends, without those a pop
// removed: for each pop, everything after the round of its push and before
// its own call, but for the user messages of the turn the pop was made in.
// Nested pops remove the union. The push's answer that stays gets a note of
// what came after it (workNote), the one of the range reaching furthest.
// Earlier messages are never edited, so the prefix through the push stays
// the same from one request to the next. It returns msgs itself when it
// cuts nothing.
export function cutPopped<M extends Msg>(msgs: M[], pops: readonly PoppedRange[]): M[] {
  if (pops.length === 0) {
    return msgs;
  }
  const skip = new Array<boolean>(msgs.length).fill(false);
  const notes = new Map<number, { text: string; end: number }>();
  let changed = false;
  for (const r of pops) {
    const s = span(msgs, r);
    if (!s) {
      continue;
    }
    const rows: Msg[] = [];
    for (let i = s.from; i < s.end; i++) {
      if (i >= s.spareFrom && msgs[i].role === "user") {
        continue;
      }
      skip[i] = true;
      rows.push(msgs[i]);
      changed = true;
    }
    if (s.answer >= 0 && rows.length > 0) {
      const old = notes.get(s.answer);
      if (!old || s.end > old.end) {
        notes.set(s.answer, { text: workNote(rows), end: s.end });
      }
    }
  }
  if (!changed) {
    return msgs;
  }
  const kept: M[] = [];
  msgs.forEach((m, i) => {
    if (skip[i]) {
      return;
    }
    const n = notes.get(i);
    kept.push(n ? (withNote(m, n.text) as M) : m);
  });
  return kept;
}

// frameLine is what a gnovm_run result tells the model of its stack while
// that is not empty: the frame it is in, the stack, and what a pop does.
// It goes at the end of the newest result, never into the cached prefix.
export function frameLine(stack: readonly StackFrame[]): string {
  if (stack.length === 0) {
    return "";
  }
  const top = stack[stack.length - 1];
  let s = `You are in \`${top.id}\` ${JSON.stringify(top.purpose)}`;
  if (stack.length > 1) {
    s += ` (your stack: ${stack.map((f) => "`" + f.id + "`").join(", then ")})`;
  }
  return (
    s +
    ': pop it with its result when it is done; everything until then is removed from your context when you pop it, what the user says included, so keep what matters as a thought. Other work the user asks for meanwhile goes in a frame of its own: mem.Todo("rename the port flag").Push(), popped when done.'
  );
}
