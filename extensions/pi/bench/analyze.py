#!/usr/bin/env python3
"""Per-trial and per-arm table for the pi vanilla vs pi + pi-gnosh runs.

usage: analyze_piext.py JOBDIR [JOBDIR...]   (each job is one arm)
Cost is from the session's own usage at BaseTen-via-OpenRouter list prices
(input 0.30, cached 0.007, output 1.20 USD/M), the prices FrontierHarness used.
"""
import glob, json, os, sys, datetime

P_IN, P_CACHED, P_OUT = 0.30e-6, 0.007e-6, 1.20e-6

def ts(s):
    try: return datetime.datetime.fromisoformat(s.replace('Z', '+00:00'))
    except Exception: return None

def trial(d):
    r = json.load(open(os.path.join(d, 'result.json'))) if os.path.exists(os.path.join(d, 'result.json')) else {}
    rew = ((r.get('verifier_result') or {}).get('rewards') or {}).get('reward')
    exc = (r.get('exception_info') or {}).get('exception_type')
    ae = r.get('agent_execution') or {}
    st, fi = ts(ae.get('started_at') or r.get('started_at') or ''), ts(ae.get('finished_at') or r.get('finished_at') or '')
    wall = (fi - st).total_seconds() if st and fi else None
    o = dict(task=os.path.basename(d).split('__')[0], trial=os.path.basename(d), reward=rew, exc=exc, wall=wall,
             inp=0, cached=0, out=0, reqs=0, maxctx=0, compactions=0, sumreqs=0, slides=0, pushes=0, pops=0, notes=0, gnovm=0)
    for s in glob.glob(os.path.join(d, 'agent/pi/sessions/*.jsonl')):
        for line in open(s):
            try: e = json.loads(line)
            except Exception: continue
            t = e.get('type')
            if t == 'compaction':
                o['compactions'] += 1
                cu = e.get('usage') or {}
                o['inp'] += cu.get('input', 0) or 0; o['cached'] += cu.get('cacheRead', 0) or 0; o['out'] += cu.get('output', 0) or 0
                o['sumreqs'] += 1
            if t == 'custom' and e.get('customType') == 'gnosh-slides': o['slides'] += 1
            if t == 'custom_message' and e.get('customType') == 'gnosh': o['notes'] += 1
            m = e.get('message') or {}
            u = m.get('usage') or {}
            if m.get('role') == 'assistant' and u:
                o['reqs'] += 1
                o['inp'] += u.get('input', 0) or 0
                o['cached'] += u.get('cacheRead', 0) or 0
                o['out'] += u.get('output', 0) or 0
                o['maxctx'] = max(o['maxctx'], (u.get('input', 0) or 0) + (u.get('cacheRead', 0) or 0))
                for c in m.get('content') or []:
                    if c.get('type') == 'toolCall' and c.get('name', '').startswith('gnovm_'): o['gnovm'] += 1
            if m.get('role') == 'toolResult':
                for f in ((m.get('details') or {}).get('gnoshFrames') or []):
                    o['pushes' if f.get('kind') == 'push' else 'pops'] += 1
    # tokens include pi's summary requests (usage on each compaction entry)
    o['cost'] = o['inp'] * P_IN + o['cached'] * P_CACHED + o['out'] * P_OUT
    return o

def main():
    rows = []
    for job in sys.argv[1:]:
        arm = os.path.basename(job.rstrip('/'))
        for d in sorted(glob.glob(os.path.join(job, '*__*'))):
            if os.path.isdir(d):
                o = trial(d); o['arm'] = arm; rows.append(o)
    hdr = ['arm', 'task', 'reward', 'exc', 'wall', 'reqs', 'inp', 'cached', 'out', 'cost', 'maxctx', 'compactions', 'slides', 'pushes', 'pops', 'notes', 'gnovm']
    print('| ' + ' | '.join(hdr) + ' |'); print('|' + '---|' * len(hdr))
    for o in rows:
        v = [o['arm'], o['task'], o['reward'], o['exc'] or '', f"{o['wall']/60:.1f}m" if o['wall'] else '', o['reqs'], o['inp'], o['cached'], o['out'], f"${o['cost']:.3f}", o['maxctx'], o['compactions'], o['slides'], o['pushes'], o['pops'], o['notes'], o['gnovm']]
        print('| ' + ' | '.join(str(x) for x in v) + ' |')
    print()
    for arm in sorted(set(o['arm'] for o in rows)):
        rs = [o for o in rows if o['arm'] == arm]
        n = len(rs); passed = sum(1 for o in rs if o['reward'] == 1.0)
        print(f"{arm}: {passed}/{n} passed; cost ${sum(o['cost'] for o in rs):.2f}; in {sum(o['inp'] for o in rs)} cached {sum(o['cached'] for o in rs)} out {sum(o['out'] for o in rs)}; "
              f"compactions {sum(o['compactions'] for o in rs)} slides {sum(o['slides'] for o in rs)} pops {sum(o['pops'] for o in rs)}; wall {sum((o['wall'] or 0) for o in rs)/60:.0f}m; timeouts {sum(1 for o in rs if o['exc']=='AgentTimeoutError')}")
    if '--json' in os.environ.get('PIEXT_FLAGS', ''):
        print(json.dumps(rows))

if __name__ == '__main__':
    main()
