// What the model reads of a worker's answers, as gnosh's gnovm tools write
// it (internal/gnovm/tools/tools.go: renderRun, statusLine, renderList).

import { errorText, type RunResult } from "./wire.ts";

const MEM_VM = "mem";
const maxText = 30_000;

function count(n: number): string {
  return n.toLocaleString("en-US");
}

function mb(n: number): string {
  return `${(n / (1 << 20)).toFixed(1)} MB`;
}

// dur is a duration of ns nanoseconds, rounded to unit (also in ns), as Go
// prints one.
export function dur(ns: number, unit: number): string {
  const r = Math.round(ns / unit) * unit;
  if (r === 0) {
    return "0s";
  }
  if (r < 1e3) return `${r}ns`;
  if (r < 1e6) return `${trim(r / 1e3)}µs`;
  if (r < 1e9) return `${trim(r / 1e6)}ms`;
  return `${trim(r / 1e9)}s`;
}

function trim(x: number): string {
  return String(Number(x.toFixed(3)));
}

export function statusLine(res: RunResult): string {
  const state = res.run_err ? "failed" : "ok";
  let kept = "nothing to keep";
  if (res.Journaled) kept = `kept as entry ${res.Seq}`;
  else if (res.run_err) kept = "nothing kept";
  else if (res.Program) kept = "thrown away after";
  if (res.VM === MEM_VM) {
    return `${state} · mem · ran ${dur(res.Wall, 1e6)} · ${kept}`;
  }
  const vm = res.Program ? "a fresh program VM" : `VM ${res.VM}`;
  const est = res.Gas * res.NsPerGas;
  return `${state} · ${vm} · ${count(res.Gas)} of ${count(res.GasLimit)} gas (≈${dur(est, 1e3)} at ${res.NsPerGas} ns/gas; ran ${dur(res.Wall, 1e3)}) · memory ${mb(res.MemUsed)} of ${mb(res.MemCap)} · ${kept}`;
}

// renderRun is a run's result: value and output, then what is said of
// frames, then the status line, then notes.
export function renderRun(res: RunResult, frames: string): string {
  const lines: string[] = [];
  if (res.run_err) {
    lines.push(`ERROR — nothing from this call was kept: ${errorText(res.run_err)}`);
    if (res.run_err.stack) lines.push(res.run_err.stack);
    if (res.Output) lines.push("output before it stopped:", res.Output.replace(/\n+$/, ""));
  } else {
    if (res.Value) lines.push(res.Value);
    if (res.Output) {
      if (res.Value) lines.push("output:");
      lines.push(res.Output.replace(/\n+$/, ""));
    }
  }
  if (frames) lines.push(frames.replace(/\n+$/, ""));
  lines.push(statusLine(res));
  for (const n of res.Notes ?? []) lines.push("note: " + n);
  return truncate(lines.join("\n") + "\n");
}

function truncate(s: string): string {
  if (s.length <= maxText) return s;
  return s.slice(0, maxText) + `\n[… ${s.length - maxText} more characters cut]\n`;
}

interface VMInfo {
  Name: string;
  Options: { memory_mb: number; gas: number; ns_per_gas: number; persist: boolean; pinned?: boolean; description?: string };
  Resident: boolean;
  MemUsed: number;
  Charge: number;
  Entries: number;
  LifetimeGas: number;
  JournalSize: number;
  LastUsed: string;
  Broken: string;
}

export interface ListResult {
  Memory: { Total: number; Available: number; Source: string };
  ProbeErr: string;
  Budget: number;
  Used: number;
  BaseBytes: number;
  VMs: VMInfo[] | null;
  Importable?: string[];
}

export function renderList(l: ListResult): string {
  const out: string[] = [];
  out.push(
    l.ProbeErr
      ? `host memory: unknown (${l.ProbeErr})`
      : `host memory: ${mb(l.Memory.Available)} available of ${mb(l.Memory.Total)} (${l.Memory.Source})`,
  );
  out.push(`VM budget: ${mb(l.Budget)}, ${mb(l.Used)} in use (standard library ${mb(l.BaseBytes)})`);
  const vms = l.VMs ?? [];
  if (vms.length === 0) out.push("no VMs yet: gnovm_run makes your scratch VM");
  for (const vm of vms) {
    let state = vm.Resident ? `in memory (${mb(vm.MemUsed)} of its cap, ${mb(vm.Charge)} counted)` : "on disk";
    if (vm.Broken) state = "BROKEN: " + vm.Broken;
    const used = vm.LastUsed && !vm.LastUsed.startsWith("0001-") ? vm.LastUsed : "never";
    const o = vm.Options;
    let line = `- ${vm.Name}: ${o.memory_mb} MB, ${count(o.gas)} gas/call, ${o.ns_per_gas} ns/gas, ${o.persist ? "persistent" : "memory-only"}${o.pinned ? ", pinned" : ""}; ${vm.Entries} entries (${vm.JournalSize >> 10} KB), ${count(vm.LifetimeGas)} gas used, last used ${used}; ${state}`;
    if (o.description) line += ` — ${o.description}`;
    out.push(line);
  }
  out.push(`importable: fmt, ${(l.Importable ?? []).join(", ")}`);
  out.push("Built on gno.land (https://gno.land) · built on gnosh (https://gnosh.ai)");
  return out.join("\n") + "\n";
}
