# The skill map editor: drafts, checks as you type, the review with its impact, publishing with an
# alias that carries progress, export, restore refusals, and who may publish.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
PW='flowa-test-only'
def get(op,path): return json.loads(op.open(BASE+path,timeout=20).read())
def wait(fn,n=30,dt=.5):
    for _ in range(n):
        v=fn()
        if v: return v
        time.sleep(dt)
    return fn()
def field(sel,val):
    js("const e=document.querySelector(arguments[0]);e.value=arguments[1];e.dispatchEvent(new Event('input',{bubbles:true}))",sel,val)
def sel_skill(sid):
    js("[...document.querySelectorAll('.ed-skill')].find(b=>b.querySelector('.tag').textContent===arguments[0]).click()",sid);time.sleep(.6)
def as_user(name,pw=PW):
    js("return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
    cmd('WebDriver:Navigate',url=BASE+f'?u={name}#/signin')
    wait(lambda: js("return !!document.querySelector('#auth-form')"))
    js("const f=document.querySelector('#auth-form');f.elements.username.value=arguments[0];f.elements.password.value=arguments[1];f.requestSubmit()",name,pw)
    wait(lambda: (js("return (document.querySelector('.nav-profile .tiny')||{}).textContent")=='@'+name))
try:
    op=admin();api(op,'flags',{'flags':{'catalogEditor':True}},'PUT');api(op,'me',{'props':['nunchaku','poi']},'PUT')
    pub=get(op,'catalog/poi.json')
    # A skill another skill needs, so a cycle can be made, and a progress record on it.
    child=next(s for s in pub['skills'] if s['needs']);parent=child['needs'][0].rstrip('!')
    api(op,'progress/poi',{'rev':0,'state':{'skills':{parent:{'O':3}}}},'PUT')
    start();signin();js("window.confirm=()=>true")
    nav('#/admin/maps/poi',2)
    wait(lambda: js("return document.querySelectorAll('.ed-skill').length")>0)
    check('the editor lists every poi skill',js("return document.querySelectorAll('.ed-skill').length")==len(pub['skills']))
    sel_skill(child['id'])
    field('#ed-skill [data-ed=name]','Edited name for the test')
    check('an edit marks the draft unsaved',js("return !document.querySelector('[data-act=ed-save]').disabled") and 'Unsaved' in txt('#ed-sum'))
    click('[data-act=ed-save]');time.sleep(1.5)
    ed=get(op,'api/admin/catalog/poi')
    check('saving keeps a draft and leaves the live map alone',[s for s in ed['draft']['skills'] if s['id']==child['id']][0]['name']=='Edited name for the test' and [s for s in get(op,'catalog/poi.json')['skills'] if s['id']==child['id']][0]['name']==child['name'])
    # A cycle: the parent now needs the child.
    sel_skill(parent)
    old_needs=' '.join([s for s in pub['skills'] if s['id']==parent][0]['needs'])
    field('#ed-skill [data-ed=needs]',(old_needs+' '+child['id']).strip())
    check('a prerequisite cycle is reported while typing',wait(lambda: 'cycle' in (txt('#ed-sum')+' '+js("return [...document.querySelectorAll('.ed-msg')].map(e=>e.textContent).join(' ')")).lower() or 'problem' in txt('#ed-sum')),txt('#ed-sum'))
    click('[data-act=ed-save]');time.sleep(1.2)
    click('[data-act=ed-view][data-v=review]');wait(lambda: js("return !!document.querySelector('[data-act=ed-publish]')"))
    check('publishing is disabled while there are problems',js("return document.querySelector('[data-act=ed-publish]').disabled") and 'cycle' in txt('.notice.warn').lower())
    click('[data-act=ed-view][data-v=skills]');time.sleep(.6);sel_skill(parent)
    field('#ed-skill [data-ed=needs]',old_needs)
    # Rename the parent; the old id stays as an alias.
    newid=parent[0]+'98'
    js("const e=document.querySelector('#ed-skill [data-ed-id]');e.value=arguments[0];e.dispatchEvent(new Event('change',{bubbles:true}))",newid);time.sleep(.8)
    click('[data-act=ed-save]');time.sleep(1.2)
    click('[data-act=ed-view][data-v=review]');wait(lambda: js("return !!document.querySelector('[data-act=ed-publish]')"))
    rows=js("return [...document.querySelectorAll('tbody tr')].map(r=>r.textContent)")
    check('the review lists the rename and the people it affects',any(f'Renamed to {newid}' in r and '1 person' in r for r in rows) and any('Changed' in r for r in rows),rows[:4])
    check('with the problems fixed, Publish is enabled',not js("return document.querySelector('[data-act=ed-publish]').disabled"))
    js("document.querySelector('#ed-note').value='Clearer names'");click('[data-act=ed-publish]');time.sleep(2.5)
    live=get(op,'catalog/poi.json')
    check('publishing changes the live map, with the alias',live['aliases'].get(parent)==newid and any(s['id']==newid for s in live['skills']) and not any(s['id']==parent for s in live['skills']))
    check('the history shows revision 2 as live',wait(lambda: 'Live' in (txt('tbody tr') or '')) and txt('tbody tr td').strip()=='2')
    # Progress follows the rename the next time it loads.
    cmd('WebDriver:Navigate',url=BASE+'?r=1#/prop/poi');time.sleep(4)
    prog=wait(lambda: (lambda s: s if newid in s and parent not in s else None)(get(op,'api/progress/poi')['state']['skills']))
    check('progress recorded under the old id moves to the new one',bool(prog) and prog[newid]['O']==3,get(op,'api/progress/poi')['state']['skills'])
    # Export, and a restore that would orphan that progress is refused.
    exp=get(op,'api/admin/catalog/poi/revisions/2')
    check('a revision exports as a catalog file',exp['id']=='poi' and exp['aliases'][parent]==newid)
    nav('#/admin/maps/poi',2);wait(lambda: js("return document.querySelectorAll('.ed-skill').length")>0)
    click('[data-act=ed-view][data-v=history]');time.sleep(.6)
    js("window.confirm=()=>true");click('[data-act=ed-restore][data-rev="1"]');time.sleep(2)
    check('restoring a revision that would orphan progress is refused',get(op,'api/admin/catalog/poi')['rev']==2 and 'progress' in (txt('#toast') or ''),txt('#toast'))
    # A moderator drafts but does not publish.
    mo=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(mo,'signup',{'username':'moe','password':PW});api(mo,'me',{'props':['poi']},'PUT')
    api(op,f"admin/users/{get(mo,'api/me')['user']['id']}/role",{'role':'moderator'},'PUT')
    as_user('moe');nav('#/admin/maps/poi',2);wait(lambda: js("return document.querySelectorAll('.ed-skill').length")>0)
    sel_skill(child['id']);field('#ed-skill [data-ed=drill]','Ten slow repetitions on each side, then ten faster.');click('[data-act=ed-save]');time.sleep(1.2)
    click('[data-act=ed-view][data-v=review]');time.sleep(2)
    check('a moderator can save a draft but sees no Publish button',not js("return !!document.querySelector('[data-act=ed-publish]')") and 'An admin publishes' in txt('main'))
    # Off: the tab is gone and the endpoints do not exist.
    api(op,'flags',{'flags':{'catalogEditor':False}},'PUT')
    try: get(op,'api/admin/catalog/poi');code=200
    except urllib.error.HTTPError as e: code=e.code
    check('with the editor off its endpoints do not exist',code==404)
finally:
    done()
