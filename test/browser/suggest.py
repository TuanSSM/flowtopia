# Next-trick suggestions in the combo builder: the list and its reasons, adding one, "Not a good fit",
# sharing marks with consent and taking them back, the usage counts on the dashboard, and the "early"
# label, which is there with the starting weights and gone with fitted ones.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
def get(op,path): return json.loads(op.open(BASE+path,timeout=20).read())
def listed(): return wait_for(lambda: len(items())>0,'the suggestions to show',20)
items=lambda: js("return [...document.querySelectorAll('.nt-i .tag')].map(t=>t.textContent)")
def saved_combo():
    return get(op,'api/progress/nunchaku')['state']['combos'][0]
try:
    op=admin();api(op,'me',{'props':['nunchaku']},'PUT');api(op,'flags',{'flags':{'suggest':True}},'PUT')
    # Clean skills so the ranking has something the user can do.
    skills={sid:{'L':2,'R':2} for sid in ['F1','F2','F3','F4','F5','F6','F7']}
    api(op,'progress/nunchaku',{'rev':0,'state':{'skills':skills,'combos':[{'id':'c1','name':'Test loop','role':'reliable','seq':['F5'],'reps':[1],'tr':{},'runs':0,'clean':0}]}},'PUT')
    start();signin();nav('#/combos')
    listed()
    check('under the last trick, five suggestions appear',len(items())==5,items())
    check('each has a reason in words',js("return [...document.querySelectorAll('.nt-why')].every(e=>e.textContent.length>8)"),js("return document.querySelector('.nt-why').textContent"))
    check('the list says the weights are early, and pairs nobody has marked say so','Early suggestions' in txt('.nt') and 'untested by anyone yet' in txt('.nt-labels'))
    check('a skill already in the combo is not suggested','F5' not in items())
    first=items()[0]
    click('.nt-add')
    c=poll(lambda: (lambda x: x if len(x['seq'])==2 else None)(saved_combo()),15)[0];idle()
    check('adding a suggestion appends it and remembers it was suggested',c and c['seq']==['F5',first] and c.get('suggested')==[f'F5>{first}'],c and (c['seq'],c.get('suggested')))
    listed()
    after=items()[0];click('.nt-no')
    check('"Not a good fit" hides it for this trick',lambda: after not in items());idle()
    nav('#/practice');nav('#/combos');listed()
    check('and it stays hidden after reload',after not in items() and get(op,'api/progress/nunchaku')['state'].get('noFit',{}).get(f'{first}>{after}'))
    # Sharing marks: off by default; on sends them; off takes them back.
    js(f"document.querySelector('[data-act=combo-tr][data-k=\"F5>{first}\"]').click()");idle()
    check('without consent nothing is shared',get(op,'api/suggest/counts?prop=nunchaku')['counts']==[])
    nav('#/profile')
    js("const f=document.querySelector('#about-form');f.shareTransitions.checked=true;f.requestSubmit()");idle()
    counts=poll(lambda: get(op,'api/suggest/counts?prop=nunchaku')['counts'],15)[0]
    marks={f"{x['a']}>{x['b']}":x for x in counts}
    check('turning sharing on sends the marks, as counts with no name',marks.get(f'F5>{first}',{}).get('works')==1 and marks.get(f'{first}>{after}',{}).get('blocked')==1,counts)
    nav('#/combos');listed()
    js(f"document.querySelector('[data-act=combo-tr][data-k=\"F5>{first}\"]').click()");idle()
    marks={f"{x['a']}>{x['b']}":x for x in get(op,'api/suggest/counts?prop=nunchaku')['counts']}
    check('changing a mark moves its count',marks.get(f'F5>{first}',{}).get('works',0)==0 and marks.get(f'F5>{first}',{}).get('bridge')==1,marks.get(f'F5>{first}'))
    nav('#/profile')
    js("const f=document.querySelector('#about-form');f.shareTransitions.checked=false;f.requestSubmit()");idle()
    check('turning it off takes every mark back out',lambda: get(op,'api/suggest/counts?prop=nunchaku')['counts']==[],timeout=15)
    # Usage reaches the dashboard.
    js("ntFlushUsage()");idle()
    nav('#/admin/dashboard');wait_for("return document.querySelectorAll('.stat').length>=6",'the dashboard to show its numbers',20)
    check('the dashboard shows how many suggestions were added and later worked','Suggestions added to a combo' in txt('main') and 'later marked Works' in txt('main'))
    # The label follows the weights file. The page is given weights that were fitted and measured, as a
    # passing gate commits them (test/suggest-gate.js), and the list no longer calls itself early.
    load(BASE+'?fitted=1#/')
    js("""const f=window.fetch;window.fetch=function(u,...a){return String(u).endsWith('suggest-weights.json')
      ?f.call(window,u,...a).then(r=>r.json()).then(w=>new Response(JSON.stringify(Object.assign(w,{early:false,pairs:42,auc:0.81,baseline:0.62})),{headers:{'Content-Type':'application/json'}}))
      :f.call(window,u,...a)}""")
    nav('#/combos');listed()
    check('with weights that were fitted and measured, the list is there and no longer says it is early',len(items())>=1 and not js("return !!document.querySelector('.nt-early')") and 'Early suggestions' not in txt('.nt'),txt('.nt')[:160])
    # Off: no list.
    api(op,'flags',{'flags':{'suggest':False}},'PUT');load(BASE+'?off=1#/combos')
    check('with the flag off there is no list',not js("return !!document.querySelector('.nt')"))
finally:
    done()
