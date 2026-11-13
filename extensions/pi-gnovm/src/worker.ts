// The worker: gnosh-gnovm-worker, started and spoken to as gnosh's own client
// does (internal/gnovm/client.go), with what a Node process can do of it.

import { spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { accessSync, closeSync, constants, globSync, mkdirSync, openSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { delimiter, join } from "node:path";
import type { Socket } from "node:net";
import type { Readable, Writable } from "node:stream";
import { encodeFrame, errorText, type Frame, FrameReader, type Hello, MAGIC, PROTOCOL, type WireError } from "./wire.ts";

export const WORKER_PROGRAM = "gnosh-gnovm-worker";

// silenceKill is how long a worker running a request may send nothing, not
// even a heartbeat (it sends one every 500 ms), before it is taken for hung.
const silenceKill = 10_000;

// dataDir is gnosh's GnoVM directory (app.GnoVMDir: next to the global data
// config), so a pi session and a gnosh session on one project share its mem.
export function dataDir(env: NodeJS.ProcessEnv = process.env): string {
  if (env.GNOSH_GLOBAL_DATA) {
    return join(env.GNOSH_GLOBAL_DATA, "gnovm");
  }
  if (env.XDG_DATA_HOME) {
    return join(env.XDG_DATA_HOME, "gnosh", "gnovm");
  }
  if (process.platform === "win32") {
    const local = env.LOCALAPPDATA || join(env.USERPROFILE || homedir(), "AppData", "Local");
    return join(local, "gnosh", "gnovm");
  }
  return join(homedir(), ".local", "share", "gnosh", "gnovm");
}

// findWorker is the worker program: the one named (the --gnovm-worker flag),
// else the first gnosh-gnovm-worker on PATH.
export function findWorker(named: string | undefined, env: NodeJS.ProcessEnv = process.env): string {
  if (named) {
    return named;
  }
  const exe = process.platform === "win32" ? WORKER_PROGRAM + ".exe" : WORKER_PROGRAM;
  for (const dir of (env.PATH || "").split(delimiter)) {
    if (!dir) {
      continue;
    }
    const p = join(dir, exe);
    try {
      accessSync(p, constants.X_OK);
      return p;
    } catch {
      // not here
    }
  }
  throw new Error(
    `${WORKER_PROGRAM} was not found on PATH; build it with \`task build:worker\` in gnosh, or name it with --gnovm-worker`,
  );
}

export class RequestError extends Error {
  readonly wire: WireError;
  constructor(wire: WireError) {
    super(errorText(wire));
    this.wire = wire;
  }
}

interface Pending {
  resolve: (body: unknown) => void;
  reject: (err: Error) => void;
  heard: number;
}

// Worker is one connection to one worker process.
export class Worker {
  readonly client: string;
  readonly dir: string;
  private child: ChildProcess;
  private requests: Writable;
  private nextId = 1;
  private pending = new Map<number, Pending>();
  private dead: Error | undefined;
  private watchdog: NodeJS.Timeout;
  hello: Hello | undefined;

  private constructor(child: ChildProcess, client: string, dir: string) {
    this.child = child;
    this.client = client;
    this.dir = dir;
    this.requests = child.stdio[3] as Writable;
    const replies = child.stdio[4] as Readable;
    const reader = new FrameReader();
    replies.on("data", (chunk: Buffer) => {
      let frames: Frame[];
      try {
        frames = reader.push(chunk);
      } catch (err) {
        this.fail(err as Error);
        return;
      }
      for (const f of frames) {
        this.take(f);
      }
    });
    this.requests.on("error", (err) => this.fail(err));
    replies.on("error", (err) => this.fail(err));
    child.on("exit", (code, signal) => this.fail(new Error(`gnovm worker exited (${signal ?? code})`)));
    child.on("error", (err) => this.fail(err));
    this.watchdog = setInterval(() => {
      const now = Date.now();
      for (const p of this.pending.values()) {
        if (now - p.heard > silenceKill) {
          this.fail(new Error("gnovm worker sent nothing for 10s; it was stopped"));
          child.kill("SIGKILL");
          return;
        }
      }
    }, 1000);
    this.watchdog.unref();
    this.idle();
  }

  // idle lets pi exit while no request runs: the worker then reads the end
  // of its request pipe and exits too. A request holds pi open (busy).
  private idle() {
    this.child.unref();
    (this.child.stdio[3] as Socket).unref?.();
    (this.child.stdio[4] as Socket).unref?.();
  }

  private busy() {
    this.child.ref();
    (this.child.stdio[3] as Socket).ref?.();
    (this.child.stdio[4] as Socket).ref?.();
  }

  // start spawns the worker in dir, as `gnosh` does (cmd.Dir is the data
  // dir; stdout is thrown away, stderr goes to worker.log), and shakes
  // hands. The worker gives up on a client that says nothing for 2 s.
  static async start(program: string, dir: string): Promise<Worker> {
    mkdirSync(dir, { recursive: true });
    const log = openSync(join(dir, "worker.log"), "a");
    let child: ChildProcess;
    try {
      child = spawn(program, [], { cwd: dir, stdio: ["ignore", "ignore", log, "pipe", "pipe"] });
    } finally {
      closeSync(log);
    }
    const client = `${process.pid}-${randomBytes(4).toString("hex")}`;
    const w = new Worker(child, client, dir);
    const mine: Hello = { magic: MAGIC, protocol: PROTOCOL, build: "pi-gnovm", gno: "", dir, client };
    try {
      w.hello = (await w.request("hello", mine)) as Hello;
    } catch (err) {
      w.close();
      throw err;
    }
    return w;
  }

  get alive(): boolean {
    return this.dead === undefined;
  }

  // request sends a request and resolves to its result's body. An abort
  // sends a cancel and still waits for the reply, as a mem call that was
  // already journaled answers it.
  request(type: string, body: unknown, signal?: AbortSignal): Promise<unknown> {
    if (this.dead) {
      return Promise.reject(this.dead);
    }
    // The handshake is frame 0, and so is its answer.
    const id = type === "hello" ? 0 : this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, heard: Date.now() });
      this.busy();
      const onAbort = () => this.send({ id, type: "cancel" });
      signal?.addEventListener("abort", onAbort, { once: true });
      const done = () => {
        signal?.removeEventListener("abort", onAbort);
        if (this.pending.size === 0) {
          this.idle();
        }
      };
      const p = this.pending.get(id)!;
      p.resolve = (b) => (done(), resolve(b));
      p.reject = (e) => (done(), reject(e));
      this.send({ id, type, body });
    });
  }

  private send(f: Frame) {
    try {
      this.requests.write(encodeFrame(f));
    } catch (err) {
      this.fail(err as Error);
    }
  }

  private take(f: Frame) {
    const p = this.pending.get(f.id);
    if (!p) {
      return;
    }
    switch (f.type) {
      case "heartbeat":
      case "ack":
        p.heard = Date.now();
        return;
      case "hello":
      case "result":
        this.pending.delete(f.id);
        if (f.err) {
          p.reject(new RequestError(f.err));
        } else {
          p.resolve(f.body);
        }
        return;
    }
  }

  private fail(err: Error) {
    if (this.dead) {
      return;
    }
    this.dead = err;
    clearInterval(this.watchdog);
    for (const p of this.pending.values()) {
      p.reject(err);
    }
    this.pending.clear();
    this.idle();
  }

  // close stops the worker the way gnosh does: by closing its request pipe,
  // so it never dies in the middle of a journal append.
  close() {
    this.fail(new Error("gnovm worker closed"));
    this.requests.end();
    const child = this.child;
    const t = setTimeout(() => child.kill("SIGKILL"), 5000);
    t.unref();
    const gone = () => {
      clearTimeout(t);
      this.deleteEphemeral();
    };
    if (child.exitCode !== null || child.signalCode !== null) {
      gone();
    } else {
      child.once("exit", gone);
    }
  }

  // deleteEphemeral deletes this client's memory-only VMs, in every
  // project, as gnosh's client does when it closes.
  private deleteEphemeral() {
    for (const m of globSync(join("*", ".ephemeral", this.client), { cwd: this.dir })) {
      rmSync(join(this.dir, m), { recursive: true, force: true });
    }
  }
}
