// The check before stopping: pi's model is sent back, a bounded number of
// times, when it would end its run with mem showing work closed and not yet
// checked against the task, or with work still open.
//
// A gnosh session has this in its harness: as a turn would end, every
// top-level todo the session closed as done is judged against the
// conversation, and one found not met is opened again and the model sent
// back to it (gnosh's internal/memjoin/goal.go, spec/gnosh/close-check.md).
// `gnosh bridge` does not run that loop, so without this a pi session ends
// as soon as the model pops its top todo: mem's results are the model's own
// words, and a model that has popped its plan reads that as the task done.
// Here the same model is asked to check, against the files and commands
// themselves, before the run may end; no other model is called.

import type { Frame } from "./protocol.ts";

/** MAX_CHECKS is how many times one prompt's run is sent back. */
export const MAX_CHECKS = 2;

/** The frames of one session as its tool results report them: what is open, and the top-level closes. */
export class Stack {
  open: Frame[] = [];
  closed: Frame[] = [];

  add(frames: Frame[], session: string) {
    for (const f of frames) {
      if (f.session && f.session !== session) continue;
      if (f.kind === "push") {
        this.open = this.open.filter((o) => o.id !== f.id);
        this.open.push(f);
        continue;
      }
      const i = this.open.findIndex((o) => o.id === f.id);
      if (i >= 0) this.open.splice(i, 1);
      if (!f.id.includes("/")) this.closed.push(f);
    }
  }
}

/** Check decides, as a run would end, whether to send the model back and what to tell it. */
export class Check {
  private seen = new Set<string>();
  private sent = 0;
  private readonly max: number;

  constructor(max = MAX_CHECKS) {
    this.max = max;
  }

  /** reset is a new prompt from the person: its run may be sent back again. */
  reset() {
    this.sent = 0;
  }

  /** decide returns the note to send the model back with, or undefined to let the run end. */
  decide(stack: Stack, stopReason: string | undefined): string | undefined {
    if (stopReason && stopReason !== "stop") return undefined; // aborted, cut off or failed: not a claim of done
    if (this.sent >= this.max) return undefined;
    const fresh = stack.closed.filter((f) => !this.seen.has(key(f)));
    const top = stack.open[0];
    if (!fresh.length && !top) return undefined;
    // An open frame is asked about once for each time it is still open as the run would end.
    const openKey = top ? `open:${top.id}:${stack.closed.length}` : "";
    if (!fresh.length && this.seen.has(openKey)) return undefined;
    for (const f of fresh) this.seen.add(key(f));
    if (openKey) this.seen.add(openKey);
    this.sent++;
    return note(fresh, top);
  }
}

const key = (f: Frame) => `closed:${f.id}:${f.result ?? ""}`;

const VERIFY =
  "Read the task as it was given again, and check each thing it asks, every requirement, constraint and number, against the files and commands themselves, the way whoever checks the result would (with the tools and settings the task names). mem's results are your own words, not evidence. Fix what does not hold, with a todo for it in mem.";

export function note(closed: Frame[], open: Frame | undefined): string {
  const named = (f: Frame) => `\`${f.id}\`${f.purpose ? ` "${f.purpose}"` : ""}`;
  const parts: string[] = [];
  if (open) {
    parts.push(`Before this run ends: ${named(open)} is still open in mem, so nothing shows it done.`);
    parts.push(VERIFY);
    parts.push("Once what you checked shows it done, pop it with that; if it is not done, go on with it.");
  } else {
    parts.push(`Before this run ends: you closed ${closed.map(named).join(", ")} as done, and nothing has checked ${closed.length > 1 ? "them" : "it"} yet.`);
    parts.push(VERIFY);
    parts.push("If everything holds, end with your answer as before.");
  }
  return `<gnosh>${parts.join(" ")}</gnosh>`;
}
