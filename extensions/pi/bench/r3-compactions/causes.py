"""Break down every pi compaction in the r3-gnosh sessions by cause.
Cause rules (per compaction, in order within a session):
  uncovered   : a 'nothing slid ... no record of' notice directly before it
  capped      : a 'has slid N times without being popped' notice directly before it
  slid-short  : the slides freed >0 but not enough ("the slides freed F, not enough", F>0)
  no-frame    : slides freed 0, no notice, and the session stood in NO frame (top level)
  in-frame    : slides freed 0, no notice, the session stood in a frame (nothing could be cut: too little left / start already slid)
  overflow    : pi compacted on a context overflow (reason), not a threshold
Depth = frame stack depth reconstructed from toolResult.details.gnoshFrames.
"""
import json,glob,os,re,sys
from collections import Counter,defaultdict
ROOT=sys.argv[1] if len(sys.argv)>1 else '/workspace/fhe-runs/jobs-piext/r3-gnosh'
rows=[]
for d in sorted(glob.glob(ROOT+'/*__*/')):
    name=os.path.basename(d.rstrip('/')); task=name.split('__')[0]
    ss=glob.glob(d+'agent/pi/sessions/*.jsonl')
    if not ss: continue
    ents=[json.loads(l) for l in open(ss[0])]
    # depth at each compaction + slides so far + pushes
    stack=[]; comps=[]; pushes=pops=0
    for e in ents:
        m=e.get('message') or {}
        for fr in ((m.get('details') or {}).get('gnoshFrames') or []):
            if fr['kind']=='push': stack.append(fr['id']); pushes+=1
            elif fr['kind']=='pop':
                pops+=1
                if fr['id'] in stack: del stack[stack.index(fr['id']):]
        if e.get('type')=='compaction':
            comps.append({'depth':len(stack),'frame':stack[-1] if stack else '','tokens':int(e.get('tokensBefore') or 0)})
    # log events from pi.txt
    ev=[]
    f=d+'agent/pi.txt'
    if os.path.exists(f):
        for line in open(f,errors='ignore'):
            if line.startswith('pi-gnosh: pi compacts'):
                m=re.search(r'pi compacts \((\w+)\) at ~(\d+) tokens: the slides freed (\d+)',line)
                if m: ev.append(('compact',m.group(1),int(m.group(2)),int(m.group(3))))
            elif line.startswith('pi-gnosh: nothing slid:'):
                kind='uncovered' if 'no record of' in line else ('capped' if 'without being popped' in line else 'other-refusal')
                ev.append(('refuse',kind))
    ci=[i for i,x in enumerate(ev) if x[0]=='compact']
    if len(ci)!=len(comps):
        print('WARN count mismatch',name,len(ci),len(comps),file=sys.stderr)
    for n,c in enumerate(comps):
        cause='other'; reason='threshold'; freed=0; tok=c['tokens']
        if n<len(ci):
            i=ci[n]; _,reason,tok2,freed=ev[i]
            prev=ev[i-1] if i>0 else None
            # a refuse directly before (since the previous compaction)
            between=[x for x in ev[(ci[n-1]+1 if n>0 else 0):i] if x[0]=='refuse']
            if reason!='threshold': cause='overflow'
            elif between and between[-1][1]=='uncovered': cause='uncovered'
            elif between and between[-1][1]=='capped': cause='capped'
            elif freed>0: cause='slid-short'
            elif c['depth']==0: cause='no-frame'
            else: cause='in-frame'
        rows.append({'trial':name,'task':task,'cause':cause,'depth':c['depth'],'frame':c['frame'],'tokens':tok,'freed':freed,'pushes':pushes,'pops':pops})
json.dump(rows,open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'causes.json'),'w'),indent=1)
tot=Counter(r['cause'] for r in rows)
print('TOTAL',len(rows),dict(tot))
bt=defaultdict(Counter)
for r in rows: bt[r['task']][r['cause']]+=1
causes=['uncovered','capped','slid-short','no-frame','in-frame','overflow','other']
print('%-24s'%'task',*['%10s'%c for c in causes],'%6s'%'total')
for t in sorted(bt):
    print('%-24s'%t,*['%10d'%bt[t][c] for c in causes],'%6d'%sum(bt[t].values()))
print('%-24s'%'ALL',*['%10d'%tot[c] for c in causes],'%6d'%len(rows))
print()
# per-trial: trials with no frames at all
pt=defaultdict(lambda:[0,0,0])
for r in rows:
    pt[r['trial']][0]+=1; pt[r['trial']][1]=r['pushes']; pt[r['trial']][2]=r['pops']
print('per trial (compactions, pushes, pops):')
for k,v in sorted(pt.items()): print(' ',k,tuple(v))
