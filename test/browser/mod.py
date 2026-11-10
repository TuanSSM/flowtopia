# Moderation and admin: menus by role, reporting, the queue, hiding, muting, the audit log, roles and the dashboard.
import sys,os,re
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
PW='flowa-test-only'
def opener(): return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def account(name):
    op=opener();api(op,'signup',{'username':name,'password':PW});api(op,'me',{'props':['nunchaku']},'PUT');return op
def get(op,path): return json.loads(op.open(BASE+'api/'+path,timeout=20).read())
def code_of(op,path):
    try: op.open(BASE+'api/'+path,timeout=20);return 200
    except urllib.error.HTTPError as e: return e.code
def as_user(name,pw=PW):
    js("return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
    cmd('WebDriver:Navigate',url=BASE+f'?u={name}#/signin')
    for _ in range(40):
        if js("return !!document.querySelector('#auth-form')"): break
        time.sleep(.5)
    js("const f=document.querySelector('#auth-form');f.elements.username.value=arguments[0];f.elements.password.value=arguments[1];f.requestSubmit()",name,pw)
    for _ in range(40):
        if js("return location.hash!=='#/signin'&&!document.querySelector('#auth-form')&&(document.querySelector('.nav-profile .tiny')||{}).textContent===arguments[0]",'@'+name): break
        time.sleep(.5)
    time.sleep(1)
menu=lambda: js("return [...document.querySelectorAll('#side .side-foot .nav-a')].map(a=>a.textContent.trim())")
def wait(fn,n=20,dt=.5):
    for _ in range(n):
        v=fn()
        if v: return v
        time.sleep(dt)
    return fn()
try:
    op=admin();api(op,'flags',{'flags':{'moderation':True}},'PUT');api(op,'me',{'props':['nunchaku']},'PUT')
    sam=account('sam');rae=account('rae');mo=account('moe')
    me_id=lambda o: get(o,'me')['user']['id']
    api(op,f'admin/users/{me_id(mo)}/role',{'role':'moderator'},'PUT')
    post=api(sam,'posts',{'body':'You all spin like beginners'})['posts'][0]['id']
    start()
    # A regular user: no moderation or admin entries; reports a post.
    as_user('rae')
    check('a regular user has no Moderation or Admin entry',not any(x.startswith(('Moderation','Admin')) for x in menu()),menu())
    nav('#/timeline',2.5)
    js("document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=report]').click()",str(post));time.sleep(.8)
    check('Report opens a dialog with the reasons',js("return document.querySelectorAll('#report-form input[name=reason]').length")==5)
    PICK="const f=document.querySelector('#report-form'),r=[...f.reason].find(r=>r.value===arguments[0]);r.checked=true;r.dispatchEvent(new Event('change',{bubbles:true}))"
    st=js("const f=document.querySelector('#report-form');return [[...f.reason].filter(r=>r.checked).length,f.querySelector('[type=submit]').disabled,f.checkValidity()]")
    check('no reason is chosen for the reporter and Send report waits for one',st==[0,True,False],st)
    js(PICK,'harassment')
    check('choosing a reason makes Send report available',js("return document.querySelector('#report-form [type=submit]').disabled")==False)
    js("const f=document.querySelector('#report-form');f.note.value='mocking people';f.requestSubmit()");time.sleep(1.2)
    check('the report is sent and the dialog closes',js("return document.querySelector('#modal').hidden"))
    js("document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=report]').click()",str(post));time.sleep(.8)
    js(PICK,'spam');js("document.querySelector('#report-form').requestSubmit()");time.sleep(1.2)
    check('reporting the same post twice says so',('already reported' in (txt('#rep-err') or '')),txt('#rep-err'))
    js("document.querySelector('[data-act=modal-close]').click()")
    own=api(rae,'posts',{'body':'mine'})['posts'][0]['id'];nav('#/practice',.8);nav('#/timeline',2.5)
    check('nobody can report their own post',js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"]')",str(own)) and not js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=report]')",str(own)))
    # A moderator: sees the queue with the report, hides the post, resolves.
    as_user('moe')
    m=wait(lambda: [x for x in menu() if x.startswith('Moderation')])
    check('a moderator has Moderation (with the count) and Admin',bool(m) and m[0].endswith('1') and 'Admin' in menu(),menu())
    nav('#/admin',2)
    tabs=js("return [...document.querySelectorAll('.admin-tabs a')].map(a=>a.textContent)")
    # The skill map editor is on by default and moderators may draft maps, so they also see Skill maps.
    check('a moderator sees the dashboard, accounts and skill maps, not features or the audit log',tabs==['Dashboard','Accounts','Skill maps'],tabs)
    nav('#/mod',2.5)
    check('the queue shows the report, its reason and the author',js("return document.querySelectorAll('.mq').length")==1 and 'Harassment' in txt('.mq') and '@sam' in txt('.mq-author') and 'mocking people' in txt('.mq'))
    click('.mq [data-act=mod-open][data-do=hide]');time.sleep(.8)
    js("const f=document.querySelector('#action-form');f.reason.value='Mocking other members';f.requestSubmit()");time.sleep(1.5)
    check('hiding needs a reason and marks the item hidden',wait(lambda: 'Hidden: Mocking other members' in (txt('.mq') or '')))
    check('the hidden post is gone for others and shown to its author with the reason',code_of(rae,f'posts/{post}')==404 and get(sam,f'posts/{post}')['posts'][0]['hidden']=='Mocking other members')
    click('.mq [data-act=mod-resolve][data-outcome=actioned]');time.sleep(1.5)
    check('resolving empties the queue and thanks the reporter',wait(lambda: js("return document.querySelectorAll('.mq').length")==0) and 'took action' in get(rae,'notifications')['items'][0]['text'])
    nav('#/mod/history',2)
    check('the history lists the resolved report',js("return document.querySelectorAll('.mq').length")==1 and 'actioned' in txt('.mq'))
    # Mute from the accounts panel.
    nav('#/admin/accounts',2.5)
    js("document.querySelector('#user-q').value='sam';document.querySelector('#user-q').dispatchEvent(new Event('input',{bubbles:true}))");time.sleep(.4)
    check('the accounts search filters the rows in place',js("return [...document.querySelectorAll('tbody tr[data-user]')].filter(r=>!r.hidden).map(r=>r.dataset.user)")==['sam'])
    click('tr[data-user=sam] [data-act=mod-open][data-do=mute]');time.sleep(.8)
    js("const f=document.querySelector('#action-form');f.reason.value='Cool off for a day';f.requestSubmit()");time.sleep(2)
    check('the muted account shows its status',wait(lambda: 'Muted until' in (txt('tr[data-user=sam]') or '')))
    WHEN=r'(?:Muted|muted|Suspended|suspended) until (.+?\d\d:\d\d(?: [AP]M)?)'
    row_when=(re.search(WHEN,txt('tr[data-user=sam]') or '') or [None,None])[1]
    check('a moderator cannot act on an admin from the panel',not js("return !!document.querySelector('tr[data-user=admin] [data-act=mod-open]')"))
    # The muted user sees why.
    as_user('sam')
    nav('#/timeline',2.5)
    check('a muted user sees a notice instead of the composer',js("return !document.querySelector('#compose-form')") and 'muted until' in txt('.muted-note') and 'Cool off' in txt('.muted-note'))
    note_when=(re.search(WHEN,txt('.muted-note') or '') or [None,None])[1]
    check('their own hidden post shows the reason',js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .notice.warn')",str(post)))
    js("loadNotifs()");time.sleep(1);click('[data-act=bell]');time.sleep(.8)
    check('the bell tells them about the hide and the mute','hid your post' in txt('.ntf-panel') and 'muted until' in txt('.ntf-panel'))
    bell_when=(re.search(WHEN,txt('.ntf-panel') or '') or [None,None])[1]
    check('the end of a mute reads the same on the accounts page, in the notice and in the bell',bool(row_when) and row_when==note_when==bell_when and not re.search(r'\dT\d\d:\d\d:\d\dZ',txt('.ntf-panel')),(row_when,note_when,bell_when))
    # An admin: every tab, the audit log, roles, the dashboard.
    as_user('admin','flowa-test-only')
    st=js("return [location.hash, (document.querySelector('.nav-profile .tiny')||{}).textContent]")
    check('the admin signs in',st[1]=='@admin',st)
    nav('#/admin',2.5)
    tabs=js("return [...document.querySelectorAll('.admin-tabs a')].map(a=>a.textContent)")
    check('an admin sees every admin section',tabs[:4]==['Dashboard','Accounts','Features','Audit log'],(tabs,js("return location.hash+' | '+document.querySelector('#main').innerText.slice(0,160)")))
    wait(lambda: js("return document.querySelectorAll('.dash-chart svg').length")==4)
    # Four headline numbers, plus two for next-trick suggestions, which are on by default.
    check('the dashboard has four headline numbers, two for suggestions, and four weekly charts with labelled values',js("return document.querySelectorAll('.stat').length")==6 and 'Suggestions' in txt('#main') and js("return document.querySelectorAll('.dash-chart svg').length")==4 and js("return document.querySelectorAll('.dash-chart .dc-val').length")>=2)
    js("document.querySelector('.dash-table summary').click()");time.sleep(.3)
    check('the weekly numbers are also a table',js("return document.querySelectorAll('.dash-table tbody tr').length")==12)
    check('the dashboard shows the running version (dev here, where VERSION is not set)',txt('.dash-version').startswith('Flowa version dev'),txt('.dash-version'))
    nav('#/admin/audit',2.5)
    acts=js("return [...document.querySelectorAll('tbody tr')].map(r=>r.cells[2].textContent)")
    check('the audit log lists the hide, the resolve, the mute and the role change',any(a.startswith('Hid ') for a in acts) and any(a.startswith('Muted') for a in acts) and any(a.startswith('Resolved') for a in acts) and any(a.startswith('Changed role') for a in acts),acts[:6])
    js("const f=document.querySelector('#audit-form');f.action.value='mute';f.requestSubmit()");time.sleep(2)
    check('the audit log filters by action',js("return document.querySelectorAll('tbody tr').length")==1)
    nav('#/admin/features',2.5)
    names=js("return [...document.querySelectorAll('[data-change=flag]')].map(i=>i.dataset.k)")
    check('the Features page lists every switch the server has',sorted(names)==sorted(get(op,'me')['flags'].keys()) and len(names)==15 and 'moderation' in names and 'catalogEditor' in names,names)
    nav('#/admin/accounts',2.5)
    ROLE="const s=document.querySelector('[data-change=user-role][data-id=\"'+arguments[0]+'\"]');s.value=arguments[1];s.dispatchEvent(new Event('change',{bubbles:true}))"
    rid=str(me_id(rae))
    js(ROLE,rid,'moderator');time.sleep(1)
    check('choosing a role asks first and changes nothing',txt('tr[data-user=rae] .role-ask').startswith('Make @rae a moderator?') and get(rae,'me')['user']['role']=='user',txt('tr[data-user=rae] .role-ask'))
    click('tr[data-user=rae] [data-act=role-cancel]');time.sleep(1)
    check('Cancel puts the list back and the role stays',not js("return !!document.querySelector('.role-ask')") and js("return document.querySelector('tr[data-user=rae] [data-change=user-role]').value")=='user' and get(rae,'me')['user']['role']=='user')
    js(ROLE,rid,'moderator');time.sleep(1);click('tr[data-user=rae] [data-act=role-apply]')
    check('Apply makes someone a moderator',wait(lambda: get(rae,'me')['user']['role']=='moderator') and wait(lambda: not js("return !!document.querySelector('.role-ask')")))
    check("an admin's own row has no role control and says who the owner is",not js("return !!document.querySelector('tr[data-user=admin] [data-change=user-role]')") and 'Owner' in txt('tr[data-user=admin]'),txt('tr[data-user=admin]'))
    # A suspension shows in the row at once, and so does a change made somewhere else when the tab is opened again.
    click('tr[data-user=sam] [data-act=mod-open][data-do=suspend]');time.sleep(.8)
    js("const f=document.querySelector('#action-form');f.reason.value='Repeated abuse';f.requestSubmit()")
    check('a suspended account reads Suspended without a reload',wait(lambda: 'Suspended until' in (txt('tr[data-user=sam]') or '')),txt('tr[data-user=sam]'))
    sus_when=(re.search(WHEN,txt('tr[data-user=sam]') or '') or [None,None])[1]
    check('the last active column has a date for someone who has signed in',bool(re.search(r'\d',js("return document.querySelector('tr[data-user=sam]').cells[3].textContent"))))
    api(op,'mod/actions',{'action':'mute','kind':'user','id':me_id(mo),'reason':'Set from another window','until':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime(time.time()+86400))})
    nav('#/admin',1.5);nav('#/admin/accounts',2.5)
    check('opening the accounts tab again fetches the list',wait(lambda: 'Muted until' in (txt('tr[data-user=moe]') or '')),txt('tr[data-user=moe]'))
    # The suspended person is told when it ends, in the same form the accounts page uses.
    js("return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
    cmd('WebDriver:Navigate',url=BASE+'?u=sam2#/signin')
    wait(lambda: js("return !!document.querySelector('#auth-form')"),40)
    js("const f=document.querySelector('#auth-form');f.elements.username.value='sam';f.elements.password.value=arguments[0];f.requestSubmit()",PW)
    err=wait(lambda: js("return document.querySelector('#auth-err').textContent"),30) or ''
    told=(re.search(WHEN,err) or [None,None])[1]
    check('a suspended sign-in gives the end time in the same form as the accounts page',bool(sus_when) and told==sus_when and 'Repeated abuse' in err,(sus_when,err))
    as_user('admin','flowa-test-only')
    # With the flag off, reporting is gone.
    api(op,'flags',{'flags':{'moderation':False}},'PUT')
    check('with moderation off the report endpoint does not exist',code_of(rae,'mod/queue')==404)
    # Community off turns off its API, not only its pages.
    api(op,'flags',{'flags':{'community':False}},'PUT')
    check('with community off the timeline endpoint does not exist',code_of(rae,'posts')==404 and code_of(rae,'notifications')==404)
    check('with community off the rest of the API still answers',code_of(rae,'me')==200)
    api(op,'flags',{'flags':{'community':True}},'PUT')
    check('with community back on the timeline answers',code_of(rae,'posts')==200)
finally:
    done()
