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
def fill_signin(tag,name,pw):
    # Sign out, load the page afresh, then find the form and fill it in within one script. The page before may
    # itself be showing a sign-in form, and a form that is found in one step can be gone by the next.
    js("window.__old=1;return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
    for go in range(3):
        load(BASE+f'?u={tag}{go or ""}#/signin')
        if poll(lambda: js("if(window.__old)return false;const f=document.querySelector('#auth-form');if(!f)return false;f.elements.username.value=arguments[0];f.elements.password.value=arguments[1];f.requestSubmit();return true",name,pw),20)[1]: return
    raise RuntimeError('the sign-in form never appeared: '+str(js("return document.body.innerText.slice(0,300)")))
def as_user(name,pw=PW):
    fill_signin(name,name,pw)
    wait_for(lambda: js("return location.hash!=='#/signin'&&!document.querySelector('#auth-form')&&(document.querySelector('.nav-profile .tiny')||{}).textContent===arguments[0]",'@'+name),f'@{name} to be signed in',30);idle()
menu=lambda: js("return [...document.querySelectorAll('#side .side-foot .nav-a')].map(a=>a.textContent.trim())")
# The Staff group of the menu: its heading and where its entries lead.
staff=lambda: js("const g=document.querySelector('#side .side-staff');return g?[g.querySelector('.nav-h').textContent,[...g.querySelectorAll('.nav-a')].map(a=>a.getAttribute('href'))]:null")
# What the search offers for a word: the pages only, as [name, address].
def pages_for(q):
    js("if(document.querySelector('#palette').hidden)openPalette();const i=document.querySelector('#pal-q');i.value=arguments[0];i.dispatchEvent(new Event('input',{bubbles:true}))",q);idle()
    rows=js("return [...document.querySelectorAll('.pal-i')].filter(a=>!/^#\\/prop\\//.test(a.getAttribute('href'))).map(a=>[a.querySelector('.grow').textContent,a.getAttribute('href')])")
    js("closePalette()")
    return rows
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
    found=pages_for('moderation')+pages_for('admin')
    check('a regular user has no Staff group, and search offers no staff pages',staff() is None and found==[],(staff(),found))
    nav('#/timeline')
    js("document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=report]').click()",str(post));idle()
    check('Report opens a dialog with the reasons',js("return document.querySelectorAll('#report-form input[name=reason]').length")==5)
    PICK="const f=document.querySelector('#report-form'),r=[...f.reason].find(r=>r.value===arguments[0]);r.checked=true;r.dispatchEvent(new Event('change',{bubbles:true}))"
    st=js("const f=document.querySelector('#report-form');return [[...f.reason].filter(r=>r.checked).length,f.querySelector('[type=submit]').disabled,f.checkValidity()]")
    check('no reason is chosen for the reporter and Send report waits for one',st==[0,True,False],st)
    js(PICK,'harassment')
    check('choosing a reason makes Send report available',js("return document.querySelector('#report-form [type=submit]').disabled")==False)
    js("const f=document.querySelector('#report-form');f.note.value='mocking people';f.requestSubmit()");idle()
    check('the report is sent and the dialog closes',js("return document.querySelector('#modal').hidden"))
    js("document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=report]').click()",str(post));idle()
    js(PICK,'spam');js("document.querySelector('#report-form').requestSubmit()");idle()
    check('reporting the same post twice says so',('already reported' in (txt('#rep-err') or '')),txt('#rep-err'))
    js("document.querySelector('[data-act=modal-close]').click()")
    own=api(rae,'posts',{'body':'mine'})['posts'][0]['id'];nav('#/practice');nav('#/timeline')
    check('nobody can report their own post',js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"]')",str(own)) and not js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=report]')",str(own)))
    # A moderator: sees the queue with the report, hides the post, resolves.
    as_user('moe')
    m=wait_for(lambda: [x for x in menu() if x.startswith('Moderation')],'the Moderation entry in the menu')
    check('a moderator has Moderation (with the count) and Admin',bool(m) and m[0].endswith('1') and 'Admin' in menu(),menu())
    check('they sit in one group headed Staff, above Settings',staff()==['Staff',['#/mod','#/admin']] and menu()[-1]=='Settings',(staff(),menu()))
    found=pages_for('moderation')+pages_for('admin')
    check('search offers a moderator Moderation, Admin and the sections they may open',found==[['Moderation','#/mod'],['Admin','#/admin'],['Admin: Overview','#/admin/dashboard'],['Admin: Accounts','#/admin/accounts'],['Admin: Catalogs','#/admin/maps']],found)
    js("openPalette();const i=document.querySelector('#pal-q');i.value='admin: accounts';i.dispatchEvent(new Event('input',{bubbles:true}))");idle()
    js("document.querySelector('.pal-i[href=\"#/admin/accounts\"]').click()");idle()
    check('choosing a section in search opens it',js("return location.hash")=='#/admin/accounts' and js("return document.querySelector('#palette').hidden") and js("return (document.querySelector('.admin-tabs a.on')||{}).textContent")=='Accounts',js("return [location.hash,(document.querySelector('.admin-tabs a.on')||{}).textContent]"))
    nav('#/admin')
    tabs=js("return [...document.querySelectorAll('.admin-tabs a')].map(a=>a.textContent)")
    # The skill map editor is on by default and moderators may draft maps, so they also see Catalogs.
    check('a moderator sees the overview, accounts and catalogs, not features or the audit log',tabs==['Overview','Accounts','Catalogs'],tabs)
    nav('#/mod')
    check('the queue shows the report, its reason and the author',js("return document.querySelectorAll('.mq').length")==1 and 'Harassment' in txt('.mq') and '@sam · member · ' in txt('.mq-author') and 'mocking people' in txt('.mq'),txt('.mq-author'))
    click('.mq [data-act=mod-open][data-do=hide]');idle()
    js("const f=document.querySelector('#action-form');f.reason.value='Mocking other members';f.requestSubmit()");idle()
    check('hiding needs a reason and marks the item hidden',lambda: 'Hidden: Mocking other members' in (txt('.mq') or ''))
    check('the hidden post is gone for others and shown to its author with the reason',code_of(rae,f'posts/{post}')==404 and get(sam,f'posts/{post}')['posts'][0]['hidden']=='Mocking other members')
    click('.mq [data-act=mod-resolve][data-outcome=actioned]');idle()
    check('resolving empties the queue and thanks the reporter',lambda: js("return document.querySelectorAll('.mq').length")==0 and 'took action' in get(rae,'notifications')['items'][0]['text'])
    nav('#/mod/history')
    check('the history lists the resolved report',js("return document.querySelectorAll('.mq').length")==1 and 'actioned' in txt('.mq'))
    # Mute from the accounts panel.
    nav('#/admin/accounts')
    js("document.querySelector('#user-q').value='sam';document.querySelector('#user-q').dispatchEvent(new Event('input',{bubbles:true}))");idle()
    check('the accounts search filters the rows in place',js("return [...document.querySelectorAll('tbody tr[data-user]')].filter(r=>!r.hidden).map(r=>r.dataset.user)")==['sam'])
    click('tr[data-user=sam] [data-act=mod-open][data-do=mute]');idle()
    js("const f=document.querySelector('#action-form');f.reason.value='Cool off for a day';f.requestSubmit()");idle()
    check('the muted account shows its status',lambda: 'Muted until' in (txt('tr[data-user=sam]') or ''))
    WHEN=r'(?:Muted|muted|Suspended|suspended) until (.+?\d\d:\d\d(?: [AP]M)?)'
    row_when=(re.search(WHEN,txt('tr[data-user=sam]') or '') or [None,None])[1]
    check('a moderator cannot act on an admin from the panel',not js("return !!document.querySelector('tr[data-user=admin] [data-act=mod-open]')"))
    # The muted user sees why.
    as_user('sam')
    nav('#/timeline')
    check('a muted user sees a notice instead of the composer',js("return !document.querySelector('#compose-form')") and 'muted until' in txt('.muted-note') and 'Cool off' in txt('.muted-note'))
    note_when=(re.search(WHEN,txt('.muted-note') or '') or [None,None])[1]
    check('their own hidden post shows the reason',js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .notice.warn')",str(post)))
    js("loadNotifs()");idle();click('[data-act=bell]');idle()
    check('the bell tells them about the hide and the mute','hid your post' in txt('.ntf-panel') and 'muted until' in txt('.ntf-panel'))
    bell_when=(re.search(WHEN,txt('.ntf-panel') or '') or [None,None])[1]
    check('the end of a mute reads the same on the accounts page, in the notice and in the bell',bool(row_when) and row_when==note_when==bell_when and not re.search(r'\dT\d\d:\d\d:\d\dZ',txt('.ntf-panel')),(row_when,note_when,bell_when))
    # An admin: every tab, the audit log, roles, the dashboard.
    as_user('admin','flowa-test-only')
    st=js("return [location.hash, (document.querySelector('.nav-profile .tiny')||{}).textContent]")
    check('the admin signs in',st[1]=='@admin',st)
    nav('#/admin')
    tabs=js("return [...document.querySelectorAll('.admin-tabs a')].map(a=>a.textContent)")
    check('an admin sees every admin section',tabs==['Overview','Accounts','Features','Audit log','Catalogs'],(tabs,js("return location.hash+' | '+document.querySelector('#main').innerText.slice(0,160)")))
    wait_for("return document.querySelectorAll('.dash-chart svg').length===4",'the four weekly charts')
    found=[r[0] for r in pages_for('admin')]
    check('search offers an admin every section',found==['Admin','Admin: Overview','Admin: Accounts','Admin: Features','Admin: Audit log','Admin: Catalogs'],found)
    # The link to the machine's own start page: none unless the server names one, and then for admins, in the Staff group.
    check('the menu has no link out of Flowa when the server names no start page',staff()==['Staff',['#/mod','#/admin']] and not js("return [...document.querySelectorAll('#side a')].some(a=>/^[a-z]+:/i.test(a.getAttribute('href')))"),staff())
    js("const real=window.fetch;window.__real=real;window.fetch=function(u,o){return real.call(window,u,o).then(r=>String(u)==='api/me'&&(!o||o.method==='GET')?r.json().then(d=>new Response(JSON.stringify(Object.assign(d,{homeUrl:'http://pi.example:8080/?a=1&b=\"2'})),{status:200,headers:{'Content-Type':'application/json'}})):r)}")
    js("loadSession().then(render)");idle()
    link=js("const a=document.querySelector('#side .side-staff a:not([href^=\"#\"])');return a?[a.textContent.trim(),a.getAttribute('href'),a.rel]:null")
    check('when the server names one, an admin gets Server home in the Staff group',link==['Server home','http://pi.example:8080/?a=1&b="2','noopener noreferrer'] and menu()[-1]=='Settings',(link,menu()))
    check('search stays inside Flowa',not [r for r in pages_for('server')+pages_for('home') if not r[1].startswith('#')])
    js("window.fetch=window.__real");js("loadSession().then(render)");idle()
    check('and it goes again when the server stops naming one',staff()==['Staff',['#/mod','#/admin']],staff())
    # Four headline numbers, plus three for next-trick suggestions, which are on by default.
    check('the dashboard has four headline numbers, three for suggestions, and four weekly charts with labelled values',js("return document.querySelectorAll('.stat').length")==7 and 'Suggestions' in txt('#main') and js("return document.querySelectorAll('.dash-chart svg').length")==4 and js("return document.querySelectorAll('.dash-chart .dc-val').length")>=2)
    # No transition has a shared mark in this suite, so the count toward the fit is 0 of 30, with no date to give.
    gate=txt('.dash-suggest .stat:nth-child(3)')
    check('the dashboard counts the pairs with a shared mark toward the 30 the fit needs, and gives no date without a pace',gate.startswith('Pairs of skills with a shared mark') and 'of 30 needed' in gate and 'no date yet' in gate,gate)
    js("document.querySelector('.dash-table summary').click()");idle()
    check('the weekly numbers are also a table',js("return document.querySelectorAll('.dash-table tbody tr').length")==12)
    check('the dashboard shows the running version (dev here, where VERSION is not set)',txt('.dash-version').startswith('Flowa version dev'),txt('.dash-version'))
    check('the dashboard says the instance runs in local mode, which is what no setting means',txt('.dash-mode').startswith('Instance mode local: a community that lives on this server only'),txt('.dash-mode'))
    nav('#/admin/audit')
    acts=js("return [...document.querySelectorAll('tbody tr')].map(r=>r.cells[2].textContent)")
    check('the audit log lists the hide, the resolve, the mute and the role change',any(a.startswith('Hid ') for a in acts) and any(a.startswith('Muted') for a in acts) and any(a.startswith('Resolved') for a in acts) and any(a.startswith('Changed role') for a in acts),acts[:6])
    js("const f=document.querySelector('#audit-form');f.action.value='mute';f.requestSubmit()");idle()
    check('the audit log filters by action',js("return document.querySelectorAll('tbody tr').length")==1)
    nav('#/admin/features')
    names=js("return [...document.querySelectorAll('[data-change=flag]')].map(i=>i.dataset.k)")
    check('the Features page lists every switch the server has',sorted(names)==sorted(get(op,'me')['flags'].keys()) and len(names)==15 and 'moderation' in names and 'catalogEditor' in names,names)
    nav('#/admin/accounts')
    ROLE="const s=document.querySelector('[data-change=user-role][data-id=\"'+arguments[0]+'\"]');s.value=arguments[1];s.dispatchEvent(new Event('change',{bubbles:true}))"
    rid=str(me_id(rae))
    js(ROLE,rid,'moderator');idle()
    check('choosing a role asks first and changes nothing',txt('tr[data-user=rae] .role-ask').startswith('Make @rae a moderator?') and get(rae,'me')['user']['role']=='user',txt('tr[data-user=rae] .role-ask'))
    click('tr[data-user=rae] [data-act=role-cancel]');idle()
    check('Cancel puts the list back and the role stays',not js("return !!document.querySelector('.role-ask')") and js("return document.querySelector('tr[data-user=rae] [data-change=user-role]').value")=='user' and get(rae,'me')['user']['role']=='user')
    js(ROLE,rid,'moderator');idle();click('tr[data-user=rae] [data-act=role-apply]')
    check('Apply makes someone a moderator',lambda: get(rae,'me')['user']['role']=='moderator' and not js("return !!document.querySelector('.role-ask')"))
    check("an admin's own row has no role control and says who the owner is",not js("return !!document.querySelector('tr[data-user=admin] [data-change=user-role]')") and 'Owner' in txt('tr[data-user=admin]'),txt('tr[data-user=admin]'))
    # A suspension shows in the row at once, and so does a change made somewhere else when the tab is opened again.
    click('tr[data-user=sam] [data-act=mod-open][data-do=suspend]');idle()
    js("const f=document.querySelector('#action-form');f.reason.value='Repeated abuse';f.requestSubmit()")
    check('a suspended account reads Suspended without a reload',lambda: 'Suspended until' in (txt('tr[data-user=sam]') or ''),lambda: txt('tr[data-user=sam]'))
    sus_when=(re.search(WHEN,txt('tr[data-user=sam]') or '') or [None,None])[1]
    check('the last active column has a date for someone who has signed in',bool(re.search(r'\d',js("return document.querySelector('tr[data-user=sam]').cells[3].textContent"))))
    api(op,'mod/actions',{'action':'mute','kind':'user','id':me_id(mo),'reason':'Set from another window','until':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime(time.time()+86400))})
    nav('#/admin');nav('#/admin/accounts')
    check('opening the accounts tab again fetches the list',lambda: 'Muted until' in (txt('tr[data-user=moe]') or ''),lambda: txt('tr[data-user=moe]'))
    # The suspended person is told when it ends, in the same form the accounts page uses.
    fill_signin('sam2','sam',PW)
    err=wait_for("return document.querySelector('#auth-err').textContent",'the sign-in to be refused',30) or ''
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
