// pi-gnovm: gnosh's Gno VMs and mem in pi, through gnosh's GnoVM worker
// (gnosh-gnovm-worker). The extension links no interpreter: it starts the
// worker, speaks its protocol, registers gnovm_run and gnovm_list, and cuts
// what a mem pop removed from each request's copy of the messages.

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { cutPopped, frameLine, framesOf, type Msg, RUN_TOOL, type RunDetails } from "./frames.ts";
import { type ListResult, renderList, renderRun } from "./render.ts";
import type { RunRequest, RunResult } from "./wire.ts";
import { dataDir, findWorker, Worker } from "./worker.ts";

const LIST_TOOL = "gnovm_list";
const MEM_VM = "mem";

const runDescription = `Run Go-shaped code in a Gno virtual machine (the GnoVM, gno.land's interpreter for its Go dialect) and see its value and output.

- Input can be an expression ("len(s) * 2"), statements ("x := 3; x++"), declarations (func, type, var, const, with methods; in any order), imports, or a mix; a trailing expression's value is shown, and println and fmt.Println output is returned.
- Packages you use (fmt, strings, strconv, sort, ...) are imported for you, and x = v or x := v on a new name declares it.
- State stays between calls in the same VM, like a REPL. With no name you get your session's scratch VM; code with a package clause (package main + func main) runs as a whole program in a fresh VM, like go run.
- Redeclaring a name in a VM is refused: assign with =, use a new name, or pass reset: true (memory-only VMs).
- Gas: 1 gas is about 1 ns of work; a call that runs out of gas, memory or time is stopped and keeps nothing (the VM is as it was before the call).
- Gno is Go without goroutines, reflection, os, net, sync, encoding/json or unsafe; there is the Gno standard library (strings, strconv, sort, math, time, regexp, bytes, unicode, encoding/hex|base64|csv, crypto/sha256, ...) and fmt, which formats as Go's does.
- name "mem" is the project's memory, shared with every session on it: todos, frames and thoughts.`;

const listDescription = `List the Gno VMs of this project, with the host's memory, the VMs' memory budget and use, and each VM's settings, memory, gas used, journal size and state; and the packages a VM can import.`;

function messagesOf(ctx: ExtensionContext): Msg[] {
  const out: Msg[] = [];
  for (const e of ctx.sessionManager.getBranch()) {
    if (e.type === "message") {
      out.push(e.message as Msg);
    }
  }
  return out;
}

// withNewer is branch with the tool results of msgs (a request's messages)
// that it does not hold yet, so a pop counts from the first request after it.
function withNewer(branch: Msg[], msgs: readonly Msg[]): Msg[] {
  const seen = new Set(branch.filter((m) => m.role === "toolResult").map((m) => m.toolCallId));
  const newer = msgs.filter((m) => m.role === "toolResult" && !seen.has(m.toolCallId));
  return newer.length === 0 ? branch : [...branch, ...newer];
}

export default function (pi: ExtensionAPI) {
  pi.registerFlag("gnovm-worker", {
    description: "Path to gnosh-gnovm-worker (default: the first on PATH)",
    type: "string",
  });

  let worker: Worker | undefined;
  let starting: Promise<Worker> | undefined;

  // ensure is the worker, started on first use and again after it died.
  async function ensure(): Promise<Worker> {
    if (worker?.alive) {
      return worker;
    }
    if (!starting) {
      const program = findWorker((pi.getFlag("gnovm-worker") as string | undefined) || undefined);
      starting = Worker.start(program, dataDir()).finally(() => (starting = undefined));
    }
    worker = await starting;
    return worker;
  }

  function shutdown() {
    worker?.close();
    worker = undefined;
  }

  pi.on("session_start", async () => {
    // Start it now rather than at the first call: the handshake fails
    // early (no worker, or one that refuses this client) and says why.
    try {
      await ensure();
    } catch {
      // gnovm_run says why when it is called.
    }
  });
  pi.on("session_shutdown", async () => shutdown());

  // The cut. A session's pops are read from its whole branch, so a push
  // that a compaction summarised still has its pop cut; the cut is made in
  // this request's copy only, the same in every request after the pop.
  pi.on("context", async (event, ctx) => {
    const msgs = event.messages as unknown as Msg[];
    const { pops } = framesOf(withNewer(messagesOf(ctx), msgs), ctx.sessionManager.getSessionId());
    const cut = cutPopped(msgs, pops);
    if (cut === msgs) {
      return undefined;
    }
    return { messages: cut as unknown as typeof event.messages };
  });

  pi.registerTool({
    name: RUN_TOOL,
    label: "Gno VM",
    description: runDescription,
    promptSnippet: "Run Gno (Go-shaped) code in a persistent VM, and use mem, the project's memory of todos, frames and thoughts",
    promptGuidelines: [
      'Before a task of two or more steps, plan it in mem in one call: `mem.Todo("fix the flaky test", "reproduce it", "find why it fails").Push()` (gnovm_run with name "mem") makes the task with its todos and enters it; `mem.Next()` steps into the next todo.',
      'When a todo is done, pop it with its result, from what your tools showed: `mem.Pop("TestFlaky races on the shared port; fixed in listen_test.go:41")`. A pop removes everything since the push from your context (your file edits stay), so put in the result what the next todo needs.',
      'What should outlive a pop is a thought: `mem.Think("the user says the port is 8080")`; find it again with `mem.Recall("port")`.',
    ],
    parameters: Type.Object({
      code: Type.String({ description: "The code to run." }),
      name: Type.Optional(
        Type.String({
          description:
            'The VM to run it in: "mem" for mem. Leave it out for your scratch VM, or, for code with a package clause, a fresh VM.',
        }),
      ),
      gas: Type.Optional(Type.Number({ description: "Gas limit for this call (default the VM's, 1e9 = about 1 s of work)." })),
      reset: Type.Optional(Type.Boolean({ description: "Start a memory-only VM over before running." })),
    }),
    async execute(toolCallId, params, signal, _onUpdate, ctx) {
      const session = ctx.sessionManager.getSessionId();
      const name = params.name ?? "";
      const req: RunRequest = {
        Project: ctx.cwd,
        Session: session,
        Name: name,
        Code: params.code,
        Gas: Math.max(0, Math.trunc(params.gas ?? 0)),
        Reset: params.reset ?? false,
        Req: toolCallId,
      };
      const before = framesOf(messagesOf(ctx), session);
      if (name === MEM_VM && before.stack.length > 0) {
        req.Frames = before.stack.map((f) => f.id);
      }
      const w = await ensure();
      const res = (await w.request("run", req, signal)) as RunResult;
      const frames = (res.run_err ? [] : (res.Frames ?? [])).filter((f) => !f.session || f.session === session);
      // The stack after this call: the one before, with this call's frames.
      const after = framesOf(
        [...messagesOf(ctx), { role: "toolResult", toolName: RUN_TOOL, toolCallId, details: { vm: res.VM, frames } }],
        session,
      );
      const said: string[] = [];
      for (const f of frames) {
        if (f.kind === "pop") {
          said.push(`popped \`${f.id}\`: what came after its push is out of your context from here on; its result is kept in mem.`);
        }
      }
      // mem says where a push or pop leaves the model; another mem call is
      // told its stack, at the end of its own result (never earlier in the
      // context, which a provider caches).
      if (name === MEM_VM && frames.length === 0) {
        const line = frameLine(after.stack);
        if (line) {
          said.push(line);
        }
      }
      const details: RunDetails = { vm: res.VM, frames };
      const text = renderRun(res, said.join("\n"));
      if (res.run_err) {
        // A failed run kept nothing; the model reads why.
        throw new Error(text);
      }
      return { content: [{ type: "text", text }], details };
    },
  });

  pi.registerTool({
    name: LIST_TOOL,
    label: "Gno VMs",
    description: listDescription,
    parameters: Type.Object({}),
    async execute(_toolCallId, _params, signal, _onUpdate, ctx) {
      const w = await ensure();
      const l = (await w.request("list", { project: ctx.cwd }, signal)) as ListResult;
      return { content: [{ type: "text", text: renderList(l) }], details: {} };
    },
  });
}
