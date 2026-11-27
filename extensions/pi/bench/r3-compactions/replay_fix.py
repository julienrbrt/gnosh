"""Offline replay of the r3-gnosh compactions through the NEW bridge (no model calls, no spend).
For each compaction, rebuild the entries the extension would have sent at that moment
(pi context = last summary + kept messages, plus the slides gnosh had kept), then ask the
real bridge `room` with covered+autokeep and need = tokensBefore - point + window/10.
 - uncovered/capped notices: replayed as they were (the bridge is fresh, so the slide cap
   (a per-session count) is not reproduced; capped cases are reported separately, not counted).
 - top-level compactions (no todo open): HYPOTHETICAL. The model is assumed to have opened a todo
   one round after the todo line (the stretch's context first reached 40% of the point); the push
   is injected at that round. Only the FIRST compaction of each stretch is a clean replay: later
   ones depend on what the slide would have done before, so they are bounded, not replayed.
Usage: replay_fix.py <gnosh-binary>
"""
import json,glob,os,subprocess,sys,re,tempfile
BIN=sys.argv[1]
ROOT='/workspace/fhe-runs/jobs-piext/r3-gnosh'
POINT=98304; WINDOW=131072
causes=json.load(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'causes.json')))
bytrial={}
for r in causes: bytrial.setdefault(r['trial'],[]).append(r)

def blocks_user(c):
    if isinstance(c,str): return [{'type':'text','text':c}]
    return [{'type':'text','text':x.get('text','')} for x in c]
GN={'bash':'Bash','read':'Read','edit':'Edit','write':'Write'}
def to_entries(msgs,session='S'):
    ents=[];seen=set()
    def add(e,key):
        u=key;n=2
        while u in seen: u=f'{key}#{n}';n+=1
        seen.add(u); e['uuid']=u; e['sessionId']=session; ents.append(e)
    def keyof(m,i):
        if m['role']=='toolResult' and m.get('toolCallId'): return 'r:'+m['toolCallId']
        if m['role']=='assistant' and isinstance(m.get('content'),list):
            for c in m['content']:
                if c.get('type')=='toolCall' and c.get('id'): return 'a:'+c['id']
        ts=m.get('timestamp')
        return f"{m['role'][0]}:{ts}" if ts is not None else f'i:{i}'
    i=0
    while i<len(msgs):
        m=msgs[i];r=m['role']
        if r=='user': add({'type':'user','source':'prompt','message':{'role':'user','content':blocks_user(m['content'])}},keyof(m,i))
        elif r=='assistant':
            cs=[]
            for c in m.get('content') or []:
                if c['type']=='text': cs.append({'type':'text','text':c.get('text','')})
                elif c['type']=='thinking': cs.append({'type':'thinking','text':c.get('thinking','')})
                elif c['type']=='toolCall':
                    a=dict(c.get('arguments') or {})
                    if c['name'] in('read','edit','write') and isinstance(a.get('path'),str): a['file_path']=a['path']
                    cs.append({'type':'tool_use','id':c['id'],'name':GN.get(c['name'],c['name']),'input':a})
            add({'type':'assistant','stop':'tool_use' if any(c['type']=='tool_use' for c in cs) else 'end_turn','message':{'role':'assistant','content':cs}},keyof(m,i))
        elif r=='toolResult':
            grp=[];first=i
            while i<len(msgs) and msgs[i]['role']=='toolResult': grp.append(msgs[i]); i+=1
            i-=1
            add({'type':'user','source':'tool_results','message':{'role':'user','content':[{'type':'tool_result','tool_use_id':g['toolCallId'],'is_error':bool(g.get('isError')),'content':blocks_user(g['content'])} for g in grp]}},keyof(grp[0],first))
            for g in grp:
                for k,f in enumerate(((g.get('details') or {}).get('gnoshFrames') or [])):
                    e={'type':'frame','frame':f['id'],'label':f.get('purpose') or '','toolUseId':g['toolCallId'],'op':f['kind']}
                    if f['kind']=='pop': e['result']=f.get('result') or ''
                    add(e,f"f:{g['toolCallId']}:{k}")
        else:
            t=m.get('content'); t=t if isinstance(t,str) else ''.join(x.get('text','') for x in (t or []))
            if r in('compactionSummary','branchSummary'): t=m.get('summary','')
            add({'type':'user','source':'notice','message':{'role':'user','content':[{'type':'text','text':t or '('+r+')'}]}},keyof(m,i))
        i+=1
    return ents

def open_frames(entries):
    st=[]
    for e in entries:
        if e['type']=='frame':
            if e['op']=='push': st.append((e['frame'],e.get('label') or e['frame']))
            elif e['op']=='pop':
                ids=[f for f,_ in st]
                if e['frame'] in ids: del st[ids.index(e['frame']):]
    return st

def room(entries,need):
    # a fresh mem knows none of the todos the trial opened: open the ones the session stands in,
    # in order, as the model did, and map the frame ids in the entries to the ones mem gives
    st=open_frames(entries)
    home=tempfile.mkdtemp()
    env=dict(os.environ,HOME=home,XDG_STATE_HOME=home+'/s',XDG_DATA_HOME=home+'/d',XDG_CONFIG_HOME=home+'/c',GNOSH_DATA_DIR=home+'/data')
    def run(reqs):
        p=subprocess.run([BIN,'bridge'],input='\n'.join(json.dumps(r) for r in reqs)+'\n',capture_output=True,text=True,cwd=home,env=env,timeout=300)
        return [json.loads(l) for l in p.stdout.strip().split('\n') if l.strip()],p.stderr
    reqs=[{'op':'hello','id':'h','protocol':1}]
    for i,(fid,label) in enumerate(st):
        reqs.append({'op':'call','id':10+i,'session':'S','call_id':f'setup{i}','name':'gnovm_run','input':{'name':'mem','code':'mem.Todo(%s).Push()'%json.dumps(label[:80])}})
    reqs.append({'op':'room','id':1,'session':'S','entries':entries,'need':need,'covered':True,'autokeep':True})
    # first pass only to learn the new ids
    out,err=run(reqs[:-1])
    m={}
    for i,(fid,label) in enumerate(st):
        fr=(out[1+i].get('frames') or [])
        if fr: m[fid]=fr[0]['id']
    def remap(o):
        if isinstance(o,dict): return {k:(m.get(v,v) if k in('frame','before') and isinstance(v,str) else remap(v)) for k,v in o.items()}
        if isinstance(o,list): return [remap(x) for x in o]
        return o
    reqs[-1]['entries']=remap(entries)
    out,err=run(reqs)
    return out[-1],err

results=[]
for d in sorted(glob.glob(ROOT+'/*__*/')):
    trial=os.path.basename(d.rstrip('/'))
    if trial not in bytrial: continue
    ss=glob.glob(d+'agent/pi/sessions/*.jsonl')[0]
    raw=[json.loads(l) for l in open(ss)]
    ctx=[];ids=[];slides=[];k=0;comp=bytrial[trial]
    last_stretch_comp=-1  # compaction index of the last one with depth 0
    for e in raw:
        t=e['type']
        if t=='message': ctx.append(e['message']); ids.append(e['id'])
        elif t=='custom_message': ctx.append({'role':'custom','content':e['content']}); ids.append(e['id'])
        elif t=='custom' and e.get('customType')=='gnosh-slides':
            for s in e['data']:
                s=dict(s); slides.append(s)
        elif t=='compaction':
            info=comp[k]; k+=1
            entries=to_entries(ctx)+slides
            need=info['tokens']-POINT+WINDOW//10
            cause=info['cause']; note=''
            if cause in('no-frame',):
                # stretch start: the first message of this context
                # hypothetical push: first assistant message whose running usage >= 40% of the point
                used=0;at=None
                for j,m in enumerate(ctx):
                    if m['role']=='assistant':
                        u=(m.get('usage') or {}).get('totalTokens') or 0
                        if u>=0.4*POINT and at is None: at=j
                # push on the toolResult of the NEXT assistant round after `at`
                tgt=None
                if at is not None:
                    seen=0
                    for j in range(at+1,len(ctx)):
                        if ctx[j]['role']=='assistant' and any(c.get('type')=='toolCall' for c in ctx[j].get('content') or []): tgt=j;break
                if tgt is None: note='no round after the nudge point'; results.append((trial,cause,None,None,note,k)); continue
                call=[c for c in ctx[tgt]['content'] if c.get('type')=='toolCall'][0]['id']
                # inject a push frame entry right after that call's result entry
                es=[];done=False
                for en in entries:
                    es.append(en)
                    if not done and en['type']=='user' and en.get('source')=='tool_results' and any(b.get('tool_use_id')==call for b in en['message']['content']):
                        es.append({'type':'frame','op':'push','frame':'hyp-todo','label':'hypothetical todo','toolUseId':call,'sessionId':'S','uuid':'hyp1'});done=True
                entries=es
            rep,err=room(entries,need)
            freed=rep.get('freed',0); n=len(rep.get('new_slides') or [])
            results.append((trial,cause,freed,need,(rep.get("notice") or "")[:140]+(" | "+" || ".join(l[:230] for l in err.strip().split("\n")) if err.strip() else ""),k))
            # compaction applied: context = summary + kept messages
            if e.get('firstKeptEntryId') in ids:
                cut=ids.index(e['firstKeptEntryId']); ctx=[{'role':'compactionSummary','summary':e.get('summary','')}]+ctx[cut:]; ids=['summary']+ids[cut:]
            else: ctx=[{'role':'compactionSummary','summary':e.get('summary','')}]; ids=['summary']
json.dump(results,open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'replay_fix.json'),'w'),indent=1)
from collections import Counter,defaultdict
print('%-36s %-10s %4s %8s %8s'%('trial','cause','#','freed','need'))
for r in results: print('%-36s %-10s %4d %8s %8s  %s'%(r[0][:36],r[1],r[5],r[2],r[3],r[4]))
