# Next-trick suggestions in the combo builder: the list and its reasons, adding one, "Not a good fit",
# sharing marks with consent and taking them back, and the usage counts on the dashboard.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
def get(op,path): return json.loads(op.open(BASE+path,timeout=20).read())
def wait(fn,n=30,dt=.5):
    for _ in range(n):
        v=fn()
        if v: return v
        time.sleep(dt)
    return fn()
items=lambda: js("return [...document.querySelectorAll('.nt-i .tag')].map(t=>t.textContent)")
def saved_combo():
    return get(op,'api/progress/nunchaku')['state']['combos'][0]
try:
    op=admin();api(op,'me',{'props':['nunchaku']},'PUT');api(op,'flags',{'flags':{'suggest':True}},'PUT')
    # Clean skills so the ranking has something the user can do.
    skills={sid:{'L':2,'R':2} for sid in ['F1','F2','F3','F4','F5','F6','F7']}
    api(op,'progress/nunchaku',{'rev':0,'state':{'skills':skills,'combos':[{'id':'c1','name':'Test loop','role':'reliable','seq':['F5'],'reps':[1],'tr':{},'runs':0,'clean':0}]}},'PUT')
    start();signin();nav('#/combos',3)
    wait(lambda: len(items())>0)
    check('under the last trick, five suggestions appear',len(items())==5,items())
    check('each has a reason in words',js("return [...document.querySelectorAll('.nt-why')].every(e=>e.textContent.length>8)"),js("return document.querySelector('.nt-why').textContent"))
    check('the list says the weights are early, and pairs nobody has marked say so','Early suggestions' in txt('.nt') and 'untested by anyone yet' in txt('.nt-labels'))
    check('a skill already in the combo is not suggested','F5' not in items())
    first=items()[0]
    click('.nt-add');time.sleep(1.5)
    c=wait(lambda: (lambda x: x if len(x['seq'])==2 else None)(saved_combo()))
    check('adding a suggestion appends it and remembers it was suggested',c and c['seq']==['F5',first] and c.get('suggested')==[f'F5>{first}'],c and (c['seq'],c.get('suggested')))
    wait(lambda: len(items())>0)
    after=items()[0];click('.nt-no');time.sleep(1.5)
    check('"Not a good fit" hides it for this trick',after not in items())
    nav('#/practice',.6);nav('#/combos',3);wait(lambda: len(items())>0)
    check('and it stays hidden after reload',after not in items() and get(op,'api/progress/nunchaku')['state'].get('noFit',{}).get(f'{first}>{after}'))
    # Sharing marks: off by default; on sends them; off takes them back.
    js(f"document.querySelector('[data-act=combo-tr][data-k=\"F5>{first}\"]').click()");time.sleep(1.5)
    check('without consent nothing is shared',get(op,'api/suggest/counts?prop=nunchaku')['counts']==[])
    nav('#/profile',2)
    js("const f=document.querySelector('#about-form');f.shareTransitions.checked=true;f.requestSubmit()");time.sleep(3)
    counts=wait(lambda: get(op,'api/suggest/counts?prop=nunchaku')['counts'])
    marks={f"{x['a']}>{x['b']}":x for x in counts}
    check('turning sharing on sends the marks, as counts with no name',marks.get(f'F5>{first}',{}).get('works')==1 and marks.get(f'{first}>{after}',{}).get('blocked')==1,counts)
    nav('#/combos',3);wait(lambda: len(items())>0)
    js(f"document.querySelector('[data-act=combo-tr][data-k=\"F5>{first}\"]').click()");time.sleep(2.5)
    marks={f"{x['a']}>{x['b']}":x for x in get(op,'api/suggest/counts?prop=nunchaku')['counts']}
    check('changing a mark moves its count',marks.get(f'F5>{first}',{}).get('works',0)==0 and marks.get(f'F5>{first}',{}).get('bridge')==1,marks.get(f'F5>{first}'))
    nav('#/profile',2)
    js("const f=document.querySelector('#about-form');f.shareTransitions.checked=false;f.requestSubmit()");time.sleep(3)
    check('turning it off takes every mark back out',wait(lambda: get(op,'api/suggest/counts?prop=nunchaku')['counts']==[]))
    # Usage reaches the dashboard.
    js("ntFlushUsage()");time.sleep(1.5)
    nav('#/admin/dashboard',2);wait(lambda: js("return document.querySelectorAll('.stat').length")>=6)
    check('the dashboard shows how many suggestions were added and later worked','Suggestions added to a combo' in txt('main') and 'later marked Works' in txt('main'))
    # Off: no list.
    api(op,'flags',{'flags':{'suggest':False}},'PUT');cmd('WebDriver:Navigate',url=BASE+'?off=1#/combos');time.sleep(4)
    check('with the flag off there is no list',not js("return !!document.querySelector('.nt')"))
finally:
    done()
