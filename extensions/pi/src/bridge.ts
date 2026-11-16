// A client of `gnosh bridge`: the child process, one request a line, the
// replies matched to their requests by id.

import { type ChildProcessWithoutNullStreams, spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { PROTOCOL, type Reply } from "./protocol.ts";

/** A reply with ok false, as an Error the caller can read the code of. */
export class BridgeCallError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly reply: Reply,
  ) {
    super(message);
  }
}

export interface StartOptions {
  /** The gnosh program; "gnosh" finds it on PATH. */
  program: string;
  /** The folder whose project the bridge serves. */
  cwd: string;
  /** Says what the bridge writes on standard error. */
  log?: (line: string) => void;
  env?: NodeJS.ProcessEnv;
}

export class Bridge {
  private readonly proc: ChildProcessWithoutNullStreams;
  private readonly waiting = new Map<number, (r: Reply) => void>();
  private next = 1;
  private ended: Error | undefined;
  hello: Reply | undefined;

  private constructor(o: StartOptions) {
    this.proc = spawn(o.program, ["bridge", "--cwd", o.cwd], { cwd: o.cwd, env: o.env ?? process.env, stdio: ["pipe", "pipe", "pipe"] });
    createInterface({ input: this.proc.stderr }).on("line", (l) => o.log?.(l));
    createInterface({ input: this.proc.stdout }).on("line", (l) => this.onLine(l));
    const end = (err: Error) => {
      this.ended ??= err;
      for (const [, resolve] of this.waiting) resolve({ ok: false, error: { code: "ended", message: this.ended.message } });
      this.waiting.clear();
    };
    this.proc.on("error", (e) => end(new Error(`gnosh bridge could not start (${o.program}): ${e.message}`)));
    this.proc.on("exit", (code, sig) => end(new Error(`gnosh bridge ended (${sig ?? code})`)));
  }

  /** start starts the bridge and says hello; it fails if the bridge cannot speak this protocol. */
  static async start(o: StartOptions & { client: string }): Promise<Bridge> {
    const b = new Bridge(o);
    b.hello = await b.call({ op: "hello", protocol: PROTOCOL, client: o.client });
    return b;
  }

  get alive(): boolean {
    return this.ended === undefined;
  }

  private onLine(line: string) {
    let r: Reply;
    try {
      r = JSON.parse(line) as Reply;
    } catch {
      return;
    }
    // A reply without an id answers a line the bridge could not read;
    // every request this client sends has one.
    const resolve = typeof r.id === "number" ? this.waiting.get(r.id) : undefined;
    if (resolve) {
      this.waiting.delete(r.id as number);
      resolve(r);
    }
  }

  /** call sends one request and resolves with its reply, or rejects with a BridgeCallError. */
  call(req: Record<string, unknown>): Promise<Reply> {
    if (this.ended) return Promise.reject(this.ended);
    const id = this.next++;
    return new Promise<Reply>((resolve, reject) => {
      this.waiting.set(id, (r) =>
        r.ok ? resolve(r) : reject(new BridgeCallError(r.error?.code ?? "internal", r.error?.message ?? "the bridge failed", r)),
      );
      this.proc.stdin.write(JSON.stringify({ ...req, id }) + "\n");
    });
  }

  close() {
    this.proc.stdin.end();
  }
}
