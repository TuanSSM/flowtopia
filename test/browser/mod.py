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
    # The instance information, with nothing saved: a visitor sees nothing new anywhere, and its about page
    # is not there.
    ABOUT_LINKS="return document.querySelectorAll('a[href=\"#/about\"]').length"
    nav('#/');landing=js(ABOUT_LINKS);nav('#/signin');signin_links=js(ABOUT_LINKS)
    nav('#/signup');wait_for("return !!document.querySelector('#auth-form[data-mode=signup]')",'the form for a new account')
    form=js("const f=document.querySelector('#auth-form');return [[...f.elements].map(e=>e.name||e.type),!!f.querySelector('.signup-rules'),document.querySelectorAll('a[href=\"#/about\"]').length]")
    check('with nothing saved, no page a visitor sees links to an about page',landing==0 and signin_links==0 and form[2]==0,(landing,signin_links,form[2]))
    check('with nothing saved, the sign-up form has its four fields and its button, and no rules',form[:2]==[['username','displayName','password','again','submit'],False],form)
    nav('#/about')
    check('with nothing saved, the about page is not found for a visitor, and its address answers as an unknown one',txt('#main .empty').startswith('Page not found') and code_of(opener(),'about')==404,lambda: txt('#main'))
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
    check('an admin sees every admin section',tabs==['Overview','Accounts','Features','Instance','Audit log','Catalogs'],(tabs,js("return location.hash+' | '+document.querySelector('#main').innerText.slice(0,160)")))
    wait_for("return document.querySelectorAll('.dash-chart svg').length===4",'the four weekly charts')
    found=[r[0] for r in pages_for('admin')]
    check('search offers an admin every section',found==['Admin','Admin: Overview','Admin: Accounts','Admin: Features','Admin: Instance','Admin: Audit log','Admin: Catalogs'],found)
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
    check('the Features page lists every switch the server has',sorted(names)==sorted(get(op,'me')['flags'].keys()) and len(names)==16 and 'moderation' in names and 'catalogEditor' in names and 'follows' in names,names)
    # The instance information: an admin saves a description and two rules in Admin, putting them in order
    # with the keys; the rules then show at sign-up, the about page opens, and it is all cleared again.
    nav('#/admin/instance')
    wait_for("return !!document.querySelector('#inst-form')",'the instance editor')
    st=js("const f=document.querySelector('#inst-form');return [f.description.value,f.contact.value,f.querySelectorAll('[data-ed=inst-rule]').length,document.querySelector('#inst-save').disabled]")
    check('the instance editor starts empty and Save waits for a change',st==['','',0,True] and 'Nothing has been saved yet' in txt('#inst-form'),st)
    check('with nothing saved, an admin has no About entry in the menu','About this Flowa' not in menu(),menu())
    TYPE="const f=document.querySelector(arguments[0]);f.value=arguments[1];f.dispatchEvent(new Event('input',{bubbles:true}))"
    RULE=lambda i,f='text': f'#inst-form .inst-rules > li:nth-child({i}) [data-f={f}]'
    js(TYPE,'#inst-form [name=description]','A place to log practice for the spinners of the Thursday jam.')
    check('a change makes Save available',lambda: js("return document.querySelector('#inst-save').disabled")==False)
    for i,t in enumerate(['Spin away from people','Be kind','Remove me']):
        click('[data-act=inst-add]');wait_for(f"return !!document.querySelector('{RULE(i+1)}')",f'rule {i+1}')
        check(f'Add a rule puts the cursor in rule {i+1}',on(RULE(i+1)),active)
        js(TYPE,RULE(i+1),t)
    js(TYPE,RULE(2,'hint'),'No comments on how anyone looks.')
    texts=lambda: js("return [...document.querySelectorAll('#inst-form [data-f=text]')].map(i=>i.value)")
    # Up by the keys, twice, on the third rule: the focus stays on its Up, then goes to its Down at the top.
    js("document.querySelector(arguments[0]).focus()",'#inst-form .inst-rules > li:nth-child(3) [data-act=inst-up]');key(ENTER)
    check('Up moves a rule one place and the focus stays on its Up',lambda: texts()==['Spin away from people','Remove me','Be kind'] and on('.inst-rules > li:nth-child(2) [data-act=inst-up]'),lambda: (texts(),active()))
    key(ENTER)
    check('at the top its Up is off and the focus is on its Down',lambda: texts()==['Remove me','Spin away from people','Be kind'] and on('.inst-rules > li:nth-child(1) [data-act=inst-down]'),lambda: (texts(),active()))
    check('the move is said',lambda: 'is now rule 1' in (txt('#say') or ''),lambda: txt('#say'))
    names=js("return [...document.querySelectorAll('.inst-rules > li:nth-child(1) button')].map(b=>b.getAttribute('aria-label'))")
    check('each button of a rule says which rule it acts on',names==['Move rule 1 up','Move rule 1 down','Remove rule 1'],names)
    # Remove by the keys: the next row has a Remove at the same place, which the focus must not land on.
    js("document.querySelector(arguments[0]).focus()",'.inst-rules > li:nth-child(1) [data-act=inst-rule-rm]');key(ENTER)
    check('Remove takes the rule away and the focus is not on another Remove',lambda: texts()==['Spin away from people','Be kind'] and js("const e=document.activeElement;return !!e&&e!==document.body&&document.querySelector('#main').contains(e)&&e.dataset.act!=='inst-rule-rm'"),lambda: (texts(),active()))
    js("document.querySelector('#inst-form').requestSubmit()")
    saved=lambda: get(op,'admin/instance')
    check('Save stores the description and the two rules in order, and Save waits again',lambda: [r['text'] for r in saved()['rules']]==['Spin away from people','Be kind'] and saved()['rules'][1]['hint']=='No comments on how anyone looks.' and saved()['description'].startswith('A place to log practice') and js("return document.querySelector('#inst-save').disabled"),lambda: saved())
    check('the admin now has About this Flowa in the menu, above Settings',lambda: menu()[-2:]==['About this Flowa','Settings'],menu)
    nav('#/about')
    check('the about page shows the description and the rules',lambda: 'Thursday jam' in (txt('#main') or '') and js("return [...document.querySelectorAll('#main .rules li b')].map(b=>b.textContent)")==['Spin away from people','Be kind'],lambda: txt('#main'))
    nav('#/admin/audit')
    js("const f=document.querySelector('#audit-form');f.action.value='instance';f.requestSubmit()")
    acts=wait_for(lambda: (lambda a: a if a and a[0][0].startswith('Changed the instance') else None)(js("return [...document.querySelectorAll('tbody tr')].map(r=>[r.cells[2].textContent.trim(),r.cells[3].textContent.trim()])")),'the audit log filtered to the instance information') or []
    js("const f=document.querySelector('#audit-form');f.action.value='';f.requestSubmit()")
    check('the save is in the audit log with the parts it changed, and the filter finds it',len(acts)==1 and acts[0]==['Changed the instance information','description, rules; 2 rules'],acts[:2])
    # A visitor: the rules above the button of the sign-up form, in order, and the about page linked.
    js("window.__old=1;return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
    load(BASE+'?rules#/signup');wait_for("return !window.__old&&!!document.querySelector('#auth-form[data-mode=signup]')",'the sign-up form, signed out',20)
    got=js("const s=document.querySelector('#auth-form .signup-rules');if(!s)return null;const b=document.querySelector('#auth-form [type=submit]');return [[...s.querySelectorAll('li b')].map(x=>x.textContent),[...s.querySelectorAll('li p')].map(x=>x.textContent),s.textContent.includes('By creating an account you agree to follow these rules.'),!!(s.compareDocumentPosition(b)&Node.DOCUMENT_POSITION_FOLLOWING)]")
    check('sign-up shows the two rules in order, the explanation, and that creating an account is agreeing, above the button',got==[['Spin away from people','Be kind'],['No comments on how anyone looks.'],True,True],got)
    nav('#/');landing=js(ABOUT_LINKS);nav('#/signin');signin_links=js(ABOUT_LINKS)
    # The menu has the link too, so each page has two: the menu's and its own.
    check('the landing and sign-in pages link to the about page once a description is saved, and so does the menu',landing==2 and signin_links==2 and 'About this Flowa' in menu(),(landing,signin_links,menu()))
    nav('#/about')
    check('a visitor can read the about page',lambda: 'Thursday jam' in (txt('#main') or ''),lambda: txt('#main'))
    # Put back: everything cleared, and the visitor sees nothing new again.
    api(op,'admin/instance',{'description':'','contact':'','rules':[],'updatedAt':saved()['updatedAt']},'PUT')
    load(BASE+'?cleared#/signup');wait_for("return !!document.querySelector('#auth-form[data-mode=signup]')",'the sign-up form again',20)
    check('cleared again: no rules at sign-up, no link, and the visitor\'s about answers as an unknown address',not js("return !!document.querySelector('.signup-rules')") and js(ABOUT_LINKS)==0 and code_of(opener(),'about')==404 and 'instance' not in get(opener(),'me'))
    as_user('admin','flowa-test-only')
    # Saving by the keys: Enter in the contact box saves, and the cursor is back in that box afterwards.
    ver=lambda: get(op,'admin/instance')['updatedAt']
    nav('#/admin/instance');wait_for("return !!document.querySelector('#inst-form [name=contact]')",'the instance editor')
    js("document.querySelector('#inst-form [name=contact]').focus()");type_text('jam@example.org');key(ENTER)
    check('Enter in a box saves, and the cursor is back in that box',lambda: get(op,'admin/instance')['contact']=='jam@example.org' and on('#inst-form [name=contact]'),lambda: (get(op,'admin/instance')['contact'],active()))
    # An address is put into its mail link as a code, so nothing in it can add a header to the message.
    href=js("return contactHTML('a.b+c-d_e@mail.example.co','email')")
    check('the mail link of an address is the address written as a code',href=='<a href="mailto:a.b%2Bc-d_e@mail.example.co">a.b+c-d_e@mail.example.co</a>',href)
    # A save on its way: typing goes on, Save stays off and a second submit sends nothing; what was typed
    # meanwhile stays in the form, unsaved, over the new version, and the next save is no clash.
    HOLD="window.__puts=0;window.__hold=new Promise(r=>window.__go=r);const real=window.__real0=window.fetch;window.fetch=function(u,o){if(String(u)==='api/admin/instance'&&o&&o.method==='PUT'){window.__puts++;return window.__hold.then(()=>real.call(window,u,o))}return real.call(window,u,o)}"
    js(HOLD)
    js(TYPE,'#inst-form [name=description]','First words.')
    js("document.querySelector('#inst-form').requestSubmit()")
    wait_for("return window.__puts===1",'the save to be on its way')
    js(TYPE,'#inst-form [name=description]','First words. More words.')
    st=js("const b=document.querySelector('#inst-save');document.querySelector('#inst-form').requestSubmit();return b.disabled")
    check('while a save is on its way, typing does not turn Save on and a second submit sends nothing',st is True and js("return window.__puts")==1,(st,js("return window.__puts")))
    js("window.__go()")
    check('after the save, what was typed meanwhile is in the form, unsaved, and the saved text is the first',lambda: get(op,'admin/instance')['description']=='First words.' and js("return document.querySelector('#inst-form [name=description]').value")=='First words. More words.' and js("return document.querySelector('#inst-save').disabled")==False,lambda: (get(op,'admin/instance')['description'],js("return document.querySelector('#inst-form [name=description]').value")))
    js("window.fetch=window.__real0");js("document.querySelector('#inst-form').requestSubmit()")
    check('the next save goes against the new version: saved, with no clash',lambda: get(op,'admin/instance')['description']=='First words. More words.' and (txt('#inst-err') or '')=='',lambda: (get(op,'admin/instance')['description'],txt('#inst-err')))
    # Unsaved text is kept when the admin goes to another page and back, and leaving the page asks first.
    js(TYPE,'#inst-form [name=description]','Kept while I look elsewhere.')
    nav('#/admin/audit');nav('#/admin/instance')
    check('unsaved text is still in the form after another page',lambda: js("return (document.querySelector('#inst-form [name=description]')||{}).value")=='Kept while I look elsewhere.')
    check('leaving the page with unsaved text asks first',js("const e=new Event('beforeunload',{cancelable:true});dispatchEvent(e);return e.defaultPrevented"))
    # Another admin saves meanwhile: the save is refused, the text stays, and the two ways on are offered.
    api(op,'admin/instance',{'description':'Theirs.','contact':'jam@example.org','rules':[],'updatedAt':ver()},'PUT')
    js("document.querySelector('#inst-form').requestSubmit()")
    check('a clash keeps the text in the form and says that someone else saved',lambda: 'Someone else saved' in (txt('#inst-err') or '') and js("return document.querySelector('#inst-form [name=description]').value")=='Kept while I look elsewhere.' and js("return document.querySelector('#inst-save').disabled"),lambda: (txt('#inst-err'),js("return document.querySelector('#inst-form [name=description]').value")))
    check('the clash offers Load their version and Save mine over it',js("return [...document.querySelectorAll('.inst-clash button')].map(b=>b.textContent)")==['Load their version','Save mine over it'])
    click('[data-act=inst-mine]')
    check('Save mine over it saves the text over theirs',lambda: get(op,'admin/instance')['description']=='Kept while I look elsewhere.' and not js("return !!document.querySelector('.inst-clash')"),lambda: get(op,'admin/instance')['description'])
    js(TYPE,'#inst-form [name=description]','Mine again.')
    api(op,'admin/instance',{'description':'Theirs again.','contact':'jam@example.org','rules':[],'updatedAt':ver()},'PUT')
    js("document.querySelector('#inst-form').requestSubmit()")
    wait_for("return !!document.querySelector('[data-act=inst-theirs]')",'the clash to be offered')
    click('[data-act=inst-theirs]')
    check('Load their version puts theirs in the form, and Save waits for a change',lambda: js("return document.querySelector('#inst-form [name=description]').value")=='Theirs again.' and js("return document.querySelector('#inst-save').disabled") and (txt('#inst-err') or '')=='' and get(op,'admin/instance')['description']=='Theirs again.')
    api(op,'admin/instance',{'description':'','contact':'','rules':[],'updatedAt':ver()},'PUT')
    check('cleared again after the clash checks',lambda: get(op,'admin/instance')['description']=='' and get(op,'admin/instance')['contact']=='')
    # Sign-up by approval: an admin turns it on in the Instance tab; a person asks to join with a reason, is
    # told it waits, and cannot sign in; the admin approves by the keys and the person signs in; another is
    # declined and the name is free again. Everything is put back whatever happens, and that is checked.
    def put_back_registration():
        for q in get(op,'admin/requests')['requests']: api(op,f"admin/requests/{q['id']}",{},'DELETE')
        cur=get(op,'admin/instance')
        if cur['registration']!='open': api(op,'admin/instance',{'description':cur['description'],'contact':cur['contact'],'rules':cur['rules'],'registration':'open','updatedAt':cur['updatedAt']},'PUT')
    try:
        nav('#/admin/audit');nav('#/admin/instance');wait_for("return !!document.querySelector('#inst-form [name=registration]')",'the registration choice')  # read afresh, after the saves made through the API
        check('the registration choice starts on Open, and Save waits for a change',js("return [document.querySelector('#inst-form [name=registration]:checked').value,document.querySelector('#inst-save').disabled]")==['open',True])
        js("document.querySelector('#inst-form [name=registration][value=approval]').click()")
        check('choosing By approval makes Save available',lambda: js("return document.querySelector('#inst-save').disabled")==False)
        js("document.querySelector('#inst-form').requestSubmit()")
        check('Save stores By approval',lambda: get(op,'admin/instance')['registration']=='approval',lambda: get(op,'admin/instance'))
        js("window.__old=1;return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
        load(BASE+'?join#/signup');wait_for("return !window.__old&&!!document.querySelector('#auth-form[data-mode=signup] [name=reason]')",'the sign-up form with the reason box',20)
        form=js("const f=document.querySelector('#auth-form'),n=document.querySelector('#join-note');return [[...f.elements].map(e=>e.name||e.type),n&&n.textContent,!!(n&&n.compareDocumentPosition(f.username)&Node.DOCUMENT_POSITION_FOLLOWING),f.querySelector('[type=submit]').textContent,f.reason.getAttribute('aria-describedby'),f.reason.maxLength]")
        check('with approval on, the sign-up form says so before the first field, asks why, and its button asks to join',form==[['username','displayName','password','again','reason','submit'],'An admin approves new accounts here. You can sign in once your request is approved.',True,'Ask to join','join-count',500],form)
        js("const f=document.querySelector('#auth-form');f.username.value='joy';f.password.value=arguments[0];f.again.value=arguments[0];f.reason.value='I spin <b>staff</b> on Thursdays.';f.requestSubmit()",PW)
        check('a request shows that it waits, names the account, and puts the focus on the heading',lambda: '@joy' in (txt('#join-sent') or '') and on('#join-sent h1'),lambda: (txt('#main'),active()))
        check('the request made no session: the page is still signed out',js("return fetch('api/me').then(r=>r.json()).then(d=>d.user)") is None)
        fill_signin('jo1','joy',PW)
        err=wait_for("return document.querySelector('#auth-err').textContent",'the sign-in to be refused',30) or ''
        check('signing in while the request waits says so, and marks no field',err.startswith('Your request to join is waiting for an admin') and not js("return !!document.querySelector('#auth-form [aria-invalid]')"),err)
        fill_signin('jo2','joy','not the right one')
        err=wait_for("return document.querySelector('#auth-err').textContent",'the sign-in to be refused',30) or ''
        check('a wrong password for a waiting name gets the ordinary answer',err=='Wrong username or password.',err)
        api(opener(),'signup',{'username':'kim','password':PW,'reason':'Please'})
        as_user('admin','flowa-test-only')
        nav('#/admin/accounts');wait_for("return document.querySelectorAll('.req-card .req').length===2",'the two requests in Accounts')
        rows=js("return [...document.querySelectorAll('.req')].map(r=>[r.querySelector('.req-who b').textContent,(r.querySelector('.req-reason')||{}).textContent,[...r.querySelectorAll('button')].map(b=>b.getAttribute('aria-label'))])")
        check('Accounts lists the requests, oldest first, with the reason as typed and buttons that name the person',rows==[['joy','I spin <b>staff</b> on Thursdays.',['Approve the request of @joy','Decline the request of @joy']],['kim','Please',['Approve the request of @kim','Decline the request of @kim']]],rows)
        check('the Accounts tab counts the requests',lambda: 'Accounts (2)' in (txt('.admin-tabs') or ''),lambda: txt('.admin-tabs'))
        check('the waiting people are not in the accounts table',not js("return !!document.querySelector('tr[data-user=joy],tr[data-user=kim]')"))
        js("document.querySelector('.req:nth-child(1) [data-act=req-approve-rm]').focus()");key(ENTER)
        check('Approve by the keys makes joy a member, and the focus is on no other request\'s button',lambda: js("return !!document.querySelector('tr[data-user=joy]')&&document.querySelectorAll('.req').length===1") and js("const e=document.activeElement;return !!e&&e!==document.body&&!/^req-/.test(e.dataset.act||'')"),lambda: (txt('.req-card'),active()))
        js("window.__confirm=window.confirm;window.confirm=()=>true;document.querySelector('.req [data-act=req-decline-rm]').focus()");key(ENTER)
        check('Decline by the keys takes the last request away, and the section with it',lambda: not js("return !!document.querySelector('.req-card')") and get(op,'admin/requests')['requests']==[] and 'Accounts (' not in (txt('.admin-tabs') or ''),lambda: txt('#main'))
        js("window.confirm=window.__confirm")
        nav('#/admin/dashboard')
        check('the dashboard counts requests to join while approval is on',lambda: js("return [...document.querySelectorAll('.stat .lbl')].map(l=>l.textContent).includes('Requests to join')"),lambda: txt('#main'))
        api(opener(),'signup',{'username':'kim','password':PW})
        check('the declined name is free again: kim can ask once more',lambda: [q['username'] for q in get(op,'admin/requests')['requests']]==['kim'])
        nav('#/admin/audit')
        acts=wait_for(lambda: (lambda a: a if len(a)>=2 else None)(js("return [...document.querySelectorAll('tbody tr')].map(r=>(r.cells[2].textContent+' '+r.cells[3].textContent).trim()).filter(t=>/request to join/.test(t))")),'the approval and the decline in the audit log') or []
        check('the approval and the decline are in the audit log',len(acts)==2 and acts[0].startswith('Declined a request to join') and '@kim' in acts[0] and acts[1].startswith('Approved the request to join of @joy'),acts)
        as_user('joy')
        check('once approved, joy signs in with the password chosen when asking',lambda: js("return fetch('api/me').then(r=>r.json()).then(d=>d.user&&d.user.username)")=='joy')
        as_user('admin','flowa-test-only')
    finally:
        put_back_registration()
    check('put back: open sign-up and no request waiting',lambda: get(op,'admin/instance')['registration']=='open' and get(op,'admin/requests')['requests']==[])
    nav('#/admin/audit')
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
