// A client of `gnosh bridge`: the child process, one request a line, the
// replies matched to their requests by id.

import { type ChildProcessWithoutNullStreams, spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { PROTOCOL, type Reply, type SideAnswer, type SideRequest } from "./protocol.ts";

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
  /**
   * side answers the bridge's side requests with the host's model: gnosh's
   * close review, goal judge and namer (bridge.md §11.1). Without it the
   * bridge is told there is no model, and those checks do not run.
   */
  side?: (req: SideRequest) => Promise<SideAnswer>;
}

export class Bridge {
  private readonly proc: ChildProcessWithoutNullStreams;
  private readonly waiting = new Map<number, (r: Reply) => void>();
  private next = 1;
  private ended: Error | undefined;
  private readonly side: StartOptions["side"];
  hello: Reply | undefined;

  private constructor(o: StartOptions) {
    this.side = o.side;
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
    b.hello = await b.call({ op: "hello", protocol: PROTOCOL, client: o.client, side: !!o.side });
    return b;
  }

  get alive(): boolean {
    return this.ended === undefined;
  }

  private onLine(line: string) {
    let r: Reply & { side?: SideRequest };
    try {
      r = JSON.parse(line) as Reply & { side?: SideRequest };
    } catch {
      return;
    }
    if (r.side) {
      void this.answer(r.side);
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

  /** answer asks the host's model a side request and writes its answer back; a failure is an answer too. */
  private async answer(req: SideRequest) {
    let a: SideAnswer;
    try {
      a = this.side ? await this.side(req) : { failed: "this host answers no side requests" };
    } catch (e) {
      a = { failed: (e as Error).message || String(e) };
    }
    if (this.ended) return;
    this.proc.stdin.write(JSON.stringify({ op: "side_answer", side_id: req.id, ...a }) + "\n");
  }

  close() {
    this.proc.stdin.end();
  }
}
