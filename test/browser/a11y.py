# Names: on every page, menu and dialog each control has a name a screen reader can say, each picture has a
# text alternative (a demo's canvas carries the caption shown under it), and the short messages that come and
# go are put into live regions that were on the page before the message.
# A control's name is only known from the page as drawn, so this is a walk through the app, not a reading of
# its source: the stops of touch.py at desktop width, then a narrow window and a first visit with the tour.
# Not walked: link cards, whose previews need the stand-in for Instagram, TikTok and YouTube that only the
# links suite is given. A11Y_LIST=1 prints each kind of control without a name once, to work through.
import sys,os,re,struct,zlib
os.environ['FLOWA_AVATARS']='1'  # the admin wears an avatar, so the stickers and the avatar's controls are drawn
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
# The rules for a name, kept small. In order: aria-labelledby (the words of the elements it points to, or
# their aria-label, so an element can name itself and add to it), aria-label, for a form field its <label>,
# alt for a picture, the words inside the element, the value of a button input, and last the title.
# The words inside leave out what aria-hidden or CSS hides; a picture inside counts by its alt, an svg by its
# aria-label or <title>. A placeholder is not a name: it is gone as soon as something is typed.
LIB=r"""
const root=document.querySelector(arguments[0]||'body');
const sq=s=>(s||'').replace(/\s+/g,' ').trim();
const gone=e=>{const s=getComputedStyle(e);return s.display==='none'||s.visibility==='hidden'};
const shown=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden'&&!e.closest('[hidden],[inert],[aria-hidden=true]');
const words=e=>{let s='';for(const n of e.childNodes){
  if(n.nodeType===3)s+=n.nodeValue;
  else if(n.nodeType===1&&n.getAttribute('aria-hidden')!=='true'&&!n.matches('select,textarea,script,style')&&!gone(n)){
    const t=n.tagName.toLowerCase();
    s+=' '+(n.getAttribute('aria-label')||(t==='img'?n.getAttribute('alt')||'':t==='svg'?(n.querySelector('title')||{}).textContent||'':words(n)))+' ';}}
  return s};
const refs=e=>sq((e.getAttribute('aria-labelledby')||'').split(/\s+/).map(i=>{const r=i&&document.getElementById(i);return r?r.getAttribute('aria-label')||r.textContent:''}).join(' '));
const name=e=>{
  const t=e.tagName.toLowerCase(),ty=(e.getAttribute('type')||'').toLowerCase(),field=/^(input|select|textarea)$/.test(t)||e.hasAttribute('contenteditable');
  return refs(e)||sq(e.getAttribute('aria-label'))
    ||(field?sq([...(e.labels||[])].map(words).join(' ')):'')
    ||(t==='img'||(t==='input'&&ty==='image')?sq(e.getAttribute('alt')):'')
    ||(field?'':sq(words(e)))
    ||(t==='input'&&/^(button|submit|reset)$/.test(ty)?sq(e.value):'')
    ||sq(e.getAttribute('title'))};
const cls=e=>e.tagName.toLowerCase()+(e.id?'#'+e.id:'')+(typeof e.className==='string'&&e.className.trim()?'.'+e.className.trim().split(/\s+/).join('.'):'');
const what=e=>cls(e)+(e.dataset.act?'[act='+e.dataset.act+']':'')+(e.dataset.change?'[change='+e.dataset.change+']':'')+(e.getAttribute('type')?'[type='+e.getAttribute('type')+']':'')+(e.getAttribute('name')?'[name='+e.getAttribute('name')+']':'');
const where=e=>{const p=e.parentElement&&e.parentElement.closest('[id],[class]');return p?' in '+cls(p):''};
"""
# Every visible control. Left out, and why:
#   - what is not drawn (no box, visibility: hidden) or sits inside [hidden], [inert] or aria-hidden="true":
#     a screen reader is not shown it either. The dark sheet behind the open menu on a narrow window (.scrim)
#     is left out this way: it is aria-hidden, and Escape or any link of the menu closes the menu as it does.
#   - input[type=hidden], and the file input inside a label.btn, which has the hidden attribute: the label
#     is the control and is checked in its place.
#   - an element that only carries data-act, is not a button, a link or a field, has no role and no tabindex,
#     and lets no click through (pointer-events: none).
NAMES=LIB+r"""
const out=[];let n=0;
for(const e of root.querySelectorAll('a[href],button,summary,label.btn,input,select,textarea,[contenteditable]:not([contenteditable=false]),[data-act],[tabindex]:not([tabindex="-1"]),[role=button],[role=tab],[role=link],[role=menuitem],[role=switch],[role=checkbox],[role=radio],[role=option]')){
  if(e.matches('input[type=hidden]')||!shown(e))continue;
  if(!e.matches('a,button,summary,label,input,select,textarea,[contenteditable],[tabindex],[role]')&&getComputedStyle(e).pointerEvents==='none')continue;
  n++;if(!name(e))out.push(what(e)+where(e));
}
return {n,bad:out};
"""
# Every visible picture. A canvas is a picture with role="img" and a name. An img has an alt; an empty alt
# is for decoration only, so it passes only when the link or button it is in has a name, or there are words
# beside it: in the element it is in, or in the one around that (a thumbnail in its own box, next to a title).
PICTURES=LIB+r"""
const out=[];let n=0;
const beside=e=>{const b=e.closest('a,button'),p=e.parentElement;return b?name(b):name(p)||(p.parentElement?name(p.parentElement):'')};
for(const e of root.querySelectorAll('img,canvas')){
  if(!shown(e))continue;n++;
  if(e.tagName==='CANVAS'){if(e.getAttribute('role')!=='img'||!(refs(e)||sq(e.getAttribute('aria-label'))))out.push(what(e)+where(e)+': a canvas needs role="img" and a name')}
  else if(!e.hasAttribute('alt'))out.push(what(e)+where(e)+': no alt')
  else if(!sq(e.alt)&&!refs(e)&&!sq(e.getAttribute('aria-label'))&&!beside(e))out.push(what(e)+where(e)+': an empty alt and no words beside it');
}
return {n,bad:out};
"""
# A canvas and the caption drawn under it: [role, name, caption].
CAPTION=LIB+r"""
const c=root.querySelector(arguments[1]),p=root.querySelector(arguments[2]);
return c&&p?[c.getAttribute('role'),refs(c)||sq(c.getAttribute('aria-label')),sq(p.textContent)]:null;
"""
# A place for short messages, as a screen reader meets it: its role, whether it is on the page for it (drawn,
# not hidden), the words in it and whether a person can see them (a box with a size).
REGION=r"""
const e=document.querySelector(arguments[0]);if(!e)return null;
const r=e.getBoundingClientRect(),s=getComputedStyle(e);
return {role:e.getAttribute('role'),there:e.getClientRects().length>0&&s.visibility!=='hidden'&&!e.closest('[hidden],[inert],[aria-hidden=true]'),text:e.textContent.replace(/\s+/g,' ').trim(),seen:r.width>1&&r.height>1};
"""
# Controls and pictures with and without a name, to show that the rules above tell them apart.
PROBE="""<button id="n1"></button><button id="n2"><svg viewBox="0 0 4 4" aria-hidden="true"><path d="M0 0h4"/></svg></button>
<button id="n3"><span aria-hidden="true">x</span></button><input id="n4" placeholder="Search"><a id="n5" href="#/"><img id="pn0" alt="" src="icon.svg"></a>
<div id="n6" data-act="none"></div><select id="n7"><option>One</option></select><span id="n8" role="button" tabindex="0"></span><textarea id="n9"></textarea>
<button id="y1" aria-label="Close"></button><button id="y2">Save</button><input id="y3"><label for="y3">Name</label><label>Minutes <input id="y4"></label>
<a id="y5" href="#/"><img alt="Home" src="icon.svg"></a><button id="y6" title="Undo"></button><input id="y7" type="submit" value="Go"><span id="yt">Speed</span><select id="y8" aria-labelledby="yt"><option>1</option></select>
<button id="y9"><svg viewBox="0 0 4 4"><title>Play</title></svg></button><button id="s1" hidden></button><div aria-hidden="true"><button id="s2"></button></div>
<canvas id="pn1"></canvas><canvas id="pn2" aria-label="A demo"></canvas><img id="pn3" src="icon.svg"><div><p><img id="pn4" alt="" src="icon.svg"></p></div>
<canvas id="py1" role="img" aria-label="A demo"></canvas><img id="py2" alt="Flowa" src="icon.svg"><p><img id="py3" alt="" src="icon.svg"> Flowa</p><div><p><img id="py4" alt="" src="icon.svg"></p><b>Flowa</b></div>"""
stops=[];kinds=set()
def page(name,h=None,act=None):
    if h is not None: nav(h)
    if act: js(act);idle()
    c=js(NAMES);p=js(PICTURES);stops.append((name,c['n'],len(c['bad']),p['n'],len(p['bad'])))
    kinds.update(x.split(' in ')[0] for x in c['bad']);kinds.update(x.split(' in ')[0]+': '+x.rsplit(': ',1)[1] for x in p['bad'])
    few=lambda l: l[:6] if len(l)<=6 else l[:6]+[f'and {len(l)-6} more']
    # A stop where no control was found was not looked at; that is a failure, not a pass.
    check(f'{name}: every control has a name',c['n']>0 and not c['bad'],few(c['bad']) or f"{c['n']} controls")
    check(f'{name}: every picture has a text alternative',not p['bad'],few(p['bad']) or f"{p['n']} pictures")
def caption(name,cv,cap,more=''):
    # Polls: a combo's caption changes as it plays, and the canvas is named by the same element.
    got=lambda: js(CAPTION,None,cv,cap)
    check(f'{name}: the canvas is a picture named with the caption under it',lambda: (lambda g: g and g[0]=='img' and len(g[2])>10 and g[2] in g[1] and more in g[1])(got()),got)
def region(sel): return js(REGION,sel)
try:
    op=admin();api(op,'flags',{'flags':{'moderation':True,'avatars':True,'catalogEditor':True}},'PUT');api(op,'me',{'props':['nunchaku','poi']},'PUT')
    pid=api(op,'posts',{'body':'Which drill fixed your wrist?'})['posts'][0]['id']
    cid=api(op,'posts/%d/comments'%pid,{'body':'Slow figure-8s in front of a mirror.'})['comment']['id']
    api(op,'posts/%d/comments'%pid,{'body':'And a lighter pair to start.','parentId':cid})
    other=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(other,'signup',{'username':'rae','password':'flowa-test-only'});api(other,'me',{'props':['nunchaku']},'PUT')
    # A picture someone posted and a profile picture: 64 x 64 px of one colour, as a PNG.
    chunk=lambda k,d: struct.pack('>I',len(d))+k+d+struct.pack('>I',zlib.crc32(k+d))
    png=base64.b64encode(b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',64,64,8,2,0,0,0))+chunk(b'IDAT',zlib.compress((b'\0'+b'\x5b\x3f\x8c'*64)*64))+chunk(b'IEND',b'')).decode()
    api(other,'me',{'avatar':'data:image/png;base64,'+png,'avatarCfg':json.loads(op.open(BASE+'api/me',timeout=20).read())['user']['avatarCfg']},'PUT')
    api(other,'progress/nunchaku',{'rev':0,'state':{'skills':{'F5':{'O':2}}}},'PUT')  # a clean skill, which their avatar plays on their profile
    op2=api(other,'posts',{'body':'First clean upspin today','image':'data:image/png;base64,'+png})['posts'][0]['id'];api(other,'posts/%d/comments'%pid,{'body':'A mirror, yes.'})
    start(1440,1100)
    # The rules themselves, on controls made for the purpose: each one without a name is found, no other is.
    js("const d=document.createElement('div');d.id='a11y-probe';d.innerHTML=arguments[0];document.body.append(d)",PROBE)
    wait_for("return [...document.querySelectorAll('#a11y-probe img')].every(i=>i.complete)",'the probe pictures to load')
    c=js(NAMES,'#a11y-probe');p=js(PICTURES,'#a11y-probe');ids=lambda l: sorted(re.search(r'#(\w+)',x).group(1) for x in l)
    check('the rules find each control without a name and no control with one',ids(c['bad'])==['n1','n2','n3','n4','n5','n6','n7','n8','n9'] and c['n']==18,lambda: c)
    check('the rules find each picture without a text alternative and no picture with one',ids(p['bad'])==['pn0','pn1','pn2','pn3','pn4'] and p['n']==10,lambda: p)
    js("document.querySelector('#a11y-probe').remove()")
    # Signed out.
    page('landing','#/')
    wait_for("return !!document.querySelector('.land #demo-cv')",'the demo on the landing page',20)
    caption('landing','.land #demo-cv','.land .demo-cap')
    page('sign in','#/signin')
    page('create an account','#/signup')
    page('skill maps, signed out','#/maps')
    page('skill guide, signed out','#/prop/nunchaku/F/F5')
    page('settings, signed out','#/settings')
    page('a page that needs an account','#/timeline')
    # The places for short messages are on the page, empty, before there is anything to say.
    for sel,what in (('#toast','messages'),('#unlock','unlocked achievements'),('#tip','hints')):
        r=region(sel);check(f'the place for {what} is a live region that is on the page before a message',bool(r) and r['role']=='status' and r['there'] and r['text']=='' and not r['seen'],lambda: r)
    signin()
    # Stickers, the avatar in the share dialog and the avatar on a profile are drawn only once the 3D figure
    # is there. Where it cannot load, those are not looked at, and the suite says so.
    if not figure(40): print('SKIP stickers and avatars: this browser has no WebGL, so the 3D figure did not load and what it draws was not looked at',flush=True)
    page('dashboard','#/')
    page('skill maps','#/maps')
    for v in js("return [...document.querySelectorAll('[data-act=view]')].map(b=>b.dataset.v)"): page(f'skill maps, {v} view',act=f"document.querySelector('[data-act=view][data-v={v}]').click()")
    page('skill map of another prop','#/prop/poi')
    page('branch graph','#/prop/nunchaku/F')
    page('skill guide','#/prop/nunchaku/F/F5')
    # The demo player: its canvas says which clip, from where, and what the caption under it says of it.
    caption('skill guide','#panel #demo-cv','#panel .demo-cap','three-quarter view')
    click('#panel [data-act=demo][data-k=view][data-v=front]')
    caption('skill guide, front view','#panel #demo-cv','#panel .demo-cap','front view')
    page('skill guide, demo paused',act="document.querySelector('#panel [data-act=demo][data-k=play]').click()")
    js("document.querySelector('#panel [data-act=demo][data-k=view][data-v=angle]').click();document.querySelector('#panel [data-act=demo][data-k=play]').click()")
    nav('#/combos');page('combos, none yet')
    js("const f=document.querySelector('#combo-form');f.name.value='Sample combo';f.requestSubmit()");idle()
    page('combos, an empty combo')
    for sid in ['F5','F7','R1']:
        js("const s=document.querySelector('[data-pick=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()",sid);idle()
    page('combos','#/combos')
    caption('combo player','#combo-cv','#combo-now')
    # A message through a real action: a logged session says "Session saved." and unlocks a badge.
    nav('#/practice')
    js("const s=document.querySelector('[data-pick=focus-add]');s.value='F7';s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()");idle()
    page('practice, a skill chosen')
    js("const f=document.querySelector('#log-form');f.minutes.value='35';f.requestSubmit()")
    check('a saved session is said in the live region, where it can also be seen',lambda: (lambda r: r['role']=='status' and r['there'] and r['seen'] and r['text']=='Session saved.')(region('#toast')),lambda: region('#toast'))
    check('an unlocked achievement is said in its live region, where it can also be seen',lambda: (lambda r: r['role']=='status' and r['there'] and r['seen'] and 'unlocked' in r['text'])(region('#unlock')),lambda: region('#unlock'),timeout=20)
    page('practice, with the unlock card','#/practice')
    js("document.querySelector('#main [data-tip]').dispatchEvent(new PointerEvent('pointerover',{bubbles:true,pointerType:'mouse'}))")
    check('a hint under the pointer is said in its live region, where it can also be seen',lambda: (lambda r: r['role']=='status' and r['there'] and r['seen'] and len(r['text'])>3)(region('#tip')),lambda: region('#tip'))
    js("document.querySelector('#main').dispatchEvent(new PointerEvent('pointerover',{bubbles:true,pointerType:'mouse'}))")
    check('the hint is taken away again, and its place stays',lambda: (lambda r: r['there'] and r['text']=='' and not r['seen'])(region('#tip')),lambda: region('#tip'))
    page('plan','#/plan')
    page('timeline','#/timeline')
    page('post with replies',f'#/post/{pid}')
    page('post with a picture',f'#/post/{op2}')
    page('notifications',act="document.querySelector('#bell [data-act=bell]').click()")
    js("document.querySelector('#bell [data-act=bell]').click()")
    page('achievements','#/achievements')
    page('share dialog',act="document.querySelector('[data-act=share-streak]').click()")
    js("document.querySelector('[data-act=modal-close]')?.click()")
    page('bookmarks, none yet','#/bookmarks')
    nav(f'#/post/{op2}');js("document.querySelector('.post [data-act=bookmark]').click()");idle()
    nav('#/prop/nunchaku/F/F5');js("document.querySelector('#panel [data-act=bookmark]').click()");idle()
    page('skill guide, bookmarked')
    page('bookmarks','#/bookmarks')
    page('profile','#/profile')
    page('public profile','#/u/rae')
    page('settings','#/settings')
    page('avatar editor','#/avatar')
    for t in js("return [...document.querySelectorAll('.av-tabs [role=tab]')].map(b=>b.dataset.v)"): page(f'avatar editor, {t}',act=f"document.querySelector('.av-tabs [data-v={t}]').click()")
    page('search',act="openPalette()")
    js("document.querySelector('#pal-q').value='spin';document.querySelector('#pal-q').dispatchEvent(new Event('input',{bubbles:true}))");page('search results')
    js("closePalette()")
    # Forms and dialogs that open on a page.
    nav('#/timeline')
    page('skill picker',act="document.querySelector('[data-act=pick-open]').click()")
    js("document.querySelector('[data-act=pick-add]').click()");idle()
    page('composer with a skill tagged')
    # A picture is put on the hidden file input the way a person choosing a file would: the click on Picture
    # takes the cursor out of the search box first.
    js("document.activeElement.blur();const u=Uint8Array.from(atob(arguments[0]),c=>c.charCodeAt(0)),d=new DataTransfer();d.items.add(new File([u],'pair.png',{type:'image/png'}));const i=document.querySelector('[data-change=compose-img]');i.files=d.files;i.dispatchEvent(new Event('change',{bubbles:true}))",png)
    wait_for("return !!document.querySelector('.compose-img img')",'the picture to show in the composer',20);idle()
    page('composer with a picture attached')
    # The draft is emptied again, so the page does not ask before it is left at the end.
    js("document.querySelector('[data-act=compose-img-rm]').click();document.querySelector('[data-act=tag-rm]').click()")
    nav(f'#/post/{pid}')
    page('reply form',act="document.querySelector('[data-act=cmt-reply]').click()")
    js("document.querySelector('[data-act=reply-cancel]')?.click()")
    page('comment being edited',act="document.querySelector('[data-act=cmt-edit]').click()")
    js("document.querySelector('[data-act=cmt-edit-cancel]')?.click()")
    nav(f'#/post/{op2}')
    page('report dialog',act="document.querySelector('[data-act=report]').click()")
    js("document.querySelector('#report-form').reason.value='spam';document.querySelector('#report-form').requestSubmit()")
    wait_for("return document.querySelector('#modal').hidden",'the report to be sent');idle()
    # Staff pages.
    page('moderation','#/mod')
    page('moderation, action dialog',act="document.querySelector('[data-act=mod-open]').click()")
    js("document.querySelector('[data-act=modal-close]')?.click()")
    page('moderation, history','#/mod/history')
    page('admin','#/admin')
    page('admin accounts','#/admin/accounts')
    page('admin accounts, a role being changed',act="const s=document.querySelector('select[data-change=user-role]');s.value=[...s.options].find(o=>!o.selected).value;s.dispatchEvent(new Event('change',{bubbles:true}))")
    page('admin features','#/admin/features')
    page('admin audit log','#/admin/audit')
    page('skill map editor, list of maps','#/admin/maps')
    nav('#/admin/maps/poi');wait_for("return document.querySelectorAll('.ed-skill').length>0",'the editor to list the skills',20)
    for v in js("return [...document.querySelectorAll('[data-act=ed-view]')].map(b=>b.dataset.v)"):
        page(f'skill map editor, {v}',act=f"document.querySelector('[data-act=ed-view][data-v={v}]').click()")
    # A narrow window, where the menu is a drawer behind a button.
    cmd('WebDriver:SetWindowRect',width=700,height=900)
    wait_for("return innerWidth<=820&&getComputedStyle(document.querySelector('.menu-btn')).display!=='none'",'the window to be narrow enough for the menu button')
    page('dashboard, narrow window','#/')
    page('menu, narrow window',act="document.querySelector('.menu-btn').click()")
    js("document.body.classList.remove('nav-open')")
    page('skill guide, narrow window','#/prop/nunchaku/F/F5')
    # Someone new: the page that asks which props they spin, the avatar they are offered, and the guided tour,
    # which is turned on for them alone (it would cover the pages above).
    js("return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
    api(op,'flags',{'flags':{'tour':True}},'PUT')
    cmd('WebDriver:SetWindowRect',width=1440,height=1100);load(BASE+'?new#/signup')
    wait_for("return !!document.querySelector('#auth-form')",'the form for a new account',20)
    js("const f=document.querySelector('#auth-form');f.elements.username.value='newbie';f.elements.password.value=f.elements.again.value='flowa-test-only';f.requestSubmit()")
    wait_for(lambda: js("return (document.querySelector('.nav-profile .tiny')||{}).textContent")=='@newbie','@newbie to be signed in',30);idle()
    page('choosing props, first visit')
    js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()")
    wait_for("return !!document.querySelector('[data-act=av-skip]')",'the avatar a new member is offered',30);idle()
    page('avatar offer, first visit')
    click('[data-act=av-skip]')
    TOUR="[...document.querySelectorAll('[role=alertdialog] button')]"
    wait_for(f"return {TOUR}.length>0",'the guided tour to open',30);idle()
    page('guided tour')
    js(f"{TOUR}.find(b=>b.textContent.startsWith('Next')).click()")
    wait_for(f"return {TOUR}.some(b=>b.textContent.startsWith('Back'))",'the second step of the tour');idle()
    page('guided tour, second step')
    print(f'{len(stops)} stops, {sum(s[1] for s in stops)} controls and {sum(s[3] for s in stops)} pictures looked at, {sum(s[2] for s in stops)} controls and {sum(s[4] for s in stops)} pictures without a name')
    # A11Y_LIST=1 lists each kind of control or picture without a name, once, to work through.
    if os.environ.get('A11Y_LIST'): print('\n'.join(sorted(kinds)))
finally: done()
