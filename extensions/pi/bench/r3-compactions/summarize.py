"""Combine causes.json and replay_fix.json: per task, how many of the compactions the replay turns into slides,
with and without the per-todo slide limit (maxFrameSlides = 6) applied to each hypothetical todo."""
import json,collections
rep=json.load(open('replay_fix.json')); CAP=6
by=collections.defaultdict(lambda: collections.Counter())
# group the no-frame compactions of one trial that follow each other (one hypothetical todo per stretch)
prev={}
stretch=collections.Counter(); n=0
for trial,cause,freed,need,note,k in rep:
    task=trial.split('__')[0]
    ok=(freed or 0)>0
    by[task]['total']+=1
    if cause=='no-frame':
        key=(trial, 'run' if prev.get(trial)=='no-frame' else 'new')
        if key[1]=='new': stretch[trial]+=1
        sid=(trial,stretch[trial]); 
        if ok:
            by[task]['top-level: slide (no limit)']+=1
            if stretch.get(('c',sid),0)<CAP:
                stretch[('c',sid)]=stretch.get(('c',sid),0)+1; by[task]['top-level: slide (limit 6)']+=1
        else: by[task]['top-level: still summary']+=1
    elif cause=='uncovered':
        by[task]['uncovered: slide']+=int(ok); by[task]['uncovered: still summary']+=int(not ok)
    elif cause=='capped': by[task]['capped: still summary (limit holds)']+=1
    elif cause=='in-frame': by[task]['in-frame (push behind summary): still summary']+=1
    prev[trial]=cause
cols=['total','top-level: slide (no limit)','top-level: slide (limit 6)','top-level: still summary','uncovered: slide','capped: still summary (limit holds)','in-frame (push behind summary): still summary']
print('| task | summaries | top-level, replay slides | …with the limit of 6 per todo | top-level, still summary | uncovered → slide | capped | in-frame after a summary |')
print('|---|---|---|---|---|---|---|---|')
tot=collections.Counter()
for t in sorted(by):
    print('| '+t+' | '+' | '.join(str(by[t][c]) for c in cols)+' |')
    tot.update(by[t])
print('| **all** | '+' | '.join('**%d**'%tot[c] for c in cols)+' |')
print()
nolimit=tot['top-level: slide (no limit)']+tot['uncovered: slide']; lim=tot['top-level: slide (limit 6)']+tot['uncovered: slide']
print('summaries that become slides: %d of %d without the limit, %d of %d with it; at the measured 78%% follow-through of a nudge: about %d to %d'%(nolimit,tot['total'],lim,tot['total'],round(tot['uncovered: slide']+0.78*tot['top-level: slide (limit 6)']),round(tot['uncovered: slide']+0.78*tot['top-level: slide (no limit)'])))
