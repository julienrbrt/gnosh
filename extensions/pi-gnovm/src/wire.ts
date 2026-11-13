// The worker protocol as gnosh's client speaks it (internal/gnovm/PROTOCOL.md
// and internal/gnovm/api/wire.go): each frame is a 4-byte big-endian length
// and that many bytes of JSON, requests on the worker's fd 3 and replies on
// its fd 4. Nothing here is new; it is the same protocol, from Node.

export const MAGIC = "gnosh-gnovm";
export const PROTOCOL = 1;
export const MAX_FRAME = 16 << 20;

export interface WireError {
  kind: string;
  msg: string;
  again?: boolean;
  limit?: string;
  stack?: string;
  inner?: WireError;
}

export interface Frame {
  id: number;
  type: string;
  body?: unknown;
  err?: WireError;
  progress?: number;
  waiting?: boolean;
}

export interface Hello {
  magic: string;
  protocol: number;
  build: string;
  pinned?: boolean;
  gno: string;
  dir?: string;
  client?: string;
  budget_bytes?: number;
  default_memory_mb?: number;
  default_gas?: number;
  ns_per_gas?: number;
  embeddings?: string;
}

// RunRequest, RunResult and MemFrame keep gnosh's Go field names, as the wire
// does (encoding/json without tags).
export interface RunRequest {
  Project: string;
  Session: string;
  Name: string;
  Code: string;
  Gas: number;
  Reset: boolean;
  Req: string;
  Frames?: string[];
}

export interface MemFrame {
  kind: "push" | "pop";
  id: string;
  purpose?: string;
  result?: string;
  thoughts?: number;
  session: string;
}

export interface RunResult {
  VM: string;
  Value: string;
  Output: string;
  Gas: number;
  GasLimit: number;
  NsPerGas: number;
  Wall: number; // nanoseconds
  MemUsed: number;
  MemCap: number;
  Journaled: boolean;
  Seq: number;
  Program: boolean;
  Notes: string[] | null;
  Frames?: MemFrame[];
  run_err?: WireError;
}

export function encodeFrame(f: Frame): Buffer {
  const json = Buffer.from(JSON.stringify(f), "utf8");
  if (json.length > MAX_FRAME) {
    throw new Error(`gnovm: frame of ${json.length} bytes is over the ${MAX_FRAME} limit`);
  }
  const head = Buffer.alloc(4);
  head.writeUInt32BE(json.length, 0);
  return Buffer.concat([head, json]);
}

// FrameReader takes the bytes of a reply stream as they come and hands back
// each whole frame.
export class FrameReader {
  private buf: Buffer = Buffer.alloc(0);

  push(chunk: Buffer): Frame[] {
    this.buf = this.buf.length === 0 ? chunk : Buffer.concat([this.buf, chunk]);
    const out: Frame[] = [];
    while (this.buf.length >= 4) {
      const n = this.buf.readUInt32BE(0);
      if (n > MAX_FRAME) {
        throw new Error(`gnovm: frame of ${n} bytes is over the ${MAX_FRAME} limit`);
      }
      if (this.buf.length < 4 + n) {
        break;
      }
      out.push(JSON.parse(this.buf.subarray(4, 4 + n).toString("utf8")) as Frame);
      this.buf = this.buf.subarray(4 + n);
    }
    return out;
  }
}

// errorText is a wire error's text: its msg already holds its inner
// errors', as the worker's error printed itself.
export function errorText(e: WireError): string {
  return e.msg;
}
