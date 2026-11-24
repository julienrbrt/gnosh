// The shapes of `gnosh bridge`'s protocol, version 1 (gnosh's
// spec/gnosh/bridge.md). Only what this extension reads is typed.

export const PROTOCOL = 1;

export interface Block {
  type: "text" | "thinking" | "tool_use" | "tool_result" | "image";
  text?: string;
  signature?: string;
  id?: string;
  name?: string;
  input?: unknown;
  tool_use_id?: string;
  content?: Block[];
  is_error?: boolean;
  media_type?: string;
  data?: string;
}

export interface Message {
  role: "user" | "assistant";
  content: Block[];
}

/** One line of a gnosh session, as the bridge's view reads it. */
export interface Entry {
  type: "user" | "assistant" | "summary" | "frame";
  uuid: string;
  timestamp?: string;
  sessionId?: string;
  message?: Message;
  source?: "prompt" | "tool_results" | "notice";
  stop?: string;
  summary?: string;
  op?: "push" | "pop" | "slide";
  frame?: string;
  label?: string;
  result?: string;
  toolUseId?: string;
  [extra: string]: unknown;
}

export interface Frame {
  kind: "push" | "pop";
  id: string;
  purpose?: string;
  result?: string;
  thoughts?: number;
  session: string;
}

export interface ToolInfo {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
  confirm?: boolean;
}

export interface BridgeError {
  code: string;
  message: string;
  summary?: string;
  protocols?: number[];
}

export interface Reply {
  ok: boolean;
  id?: number | string;
  error?: BridgeError;
  protocol?: number;
  build?: string;
  version?: string;
  project?: string;
  ops?: string[];
  tools?: ToolInfo[];
  content?: Block[];
  is_error?: boolean;
  frames?: Frame[];
  messages?: Message[];
  sources?: string[][];
  stack?: { id: string; label?: string; call_id?: string; slid?: boolean }[];
  tokens?: number;
  over_budget?: boolean;
  new_slides?: Entry[];
  // prompt, room and step (bridge.md §10)
  system?: string;
  parts?: string[];
  freed?: number;
  notice?: string;
  // end (bridge.md §11)
  action?: "continue" | "end";
  send?: string;
  driver?: string;
  objection?: string;
  stopped?: string;
  notices?: string[];
}

/** A request for the host's model the bridge writes while it answers a request (bridge.md §11.1). */
export interface SideRequest {
  id: number;
  purpose: string;
  system?: string;
  prompt: string;
  max_tokens?: number;
  temperature?: number;
  timeout_ms?: number;
}

/** The host's answer to a side request: the model's text and its stop reason, or why there is none. */
export type SideAnswer = { answer: string; stop: "end_turn" | "max_tokens" } | { failed: string };
