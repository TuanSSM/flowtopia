# Names: on every page, menu and dialog each control has a name a screen reader can say, each picture has a
# text alternative (a demo's canvas carries the caption shown under it), and the short messages that come and
# go are put into live regions that were on the page before the message.
# A control's name is only known from the page as drawn, so this is a walk through the app, not a reading of
# its source: the stops of touch.py at desktop width, then a narrow window and a first visit with the tour.
# Not walked: link cards, whose previews need the stand-in for Instagram, TikTok and YouTube that only the
# links suite is given. A11Y_LIST=1 prints each kind of control without a name once, to work through.
# Forms: at the same stops every field has a label bound to it, what a field or a label points at by id is on
# the page, and no id is used twice. Then three forms are sent wrong (a password that is not the account's, a
# password that is too short, a post that is too long) to hear how the error is said, and the composer is
# typed full to hear how the room left in it is said.
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
# Every visible field: what a person types in, picks from or ticks. The rule for a label is narrower than the
# rule for a name: aria-labelledby (to ids that are on the page and hold words), aria-label, or a <label> that
# wraps the field or points at it with for. A title does not count, though it names a button: it is a hint
# that shows under a resting mouse and is not said by every screen reader. A placeholder does not count
# either. A radio button also sits in a named group (a fieldset with a legend, or role="radiogroup" or
# "group" with a name), because its own label is only one of the answers and the group holds the question.
# Left out: buttons made of <input> (they are controls, not fields) and what is not drawn, as above. The
# file inputs behind "Picture" and "Import JSON" are not drawn; the label around them is the control.
FIELDS=LIB+r"""
const out=[];let n=0;
const label=e=>refs(e)||sq(e.getAttribute('aria-label'))||sq([...(e.labels||[])].map(words).join(' '));
for(const e of root.querySelectorAll('input,select,textarea,[contenteditable]:not([contenteditable=false]),[role=textbox],[role=searchbox],[role=combobox],[role=listbox],[role=slider],[role=spinbutton]')){
  const ty=(e.getAttribute('type')||'').toLowerCase();
  if((e.tagName==='INPUT'&&/^(hidden|button|submit|reset|image)$/.test(ty))||!shown(e))continue;
  n++;
  if(!label(e))out.push(what(e)+where(e)+(sq(e.getAttribute('title'))||sq(e.getAttribute('placeholder'))?': only a title or a placeholder, which is not a label':': no label'));
  else if(ty==='radio'){
    const g=e.closest('fieldset,[role=radiogroup],[role=group]'),l=g&&(g.tagName==='FIELDSET'?[...g.children].find(c=>c.tagName==='LEGEND'):null);
    if(!g||!(refs(g)||sq(g.getAttribute('aria-label'))||(l&&sq(words(l)))))out.push(what(e)+where(e)+': a radio button outside a named group');
  }
}
return {n,bad:out};
"""
# The whole page, drawn or not: what aria-labelledby, aria-describedby and a label's for point at is on the
# page, and no id is on two elements (the browser would follow such a pointer to the first one only).
IDS=LIB+r"""
const out=[],seen=new Map();let n=0;
for(const e of root.querySelectorAll('[id]'))seen.set(e.id,(seen.get(e.id)||0)+1);
for(const [id,k] of seen)if(k>1)out.push('#'+id+': on '+k+' elements');
for(const e of root.querySelectorAll('[aria-labelledby],[aria-describedby],label[for]'))for(const a of ['aria-labelledby','aria-describedby','for']){
  if(!e.hasAttribute(a))continue;
  for(const id of e.getAttribute(a).split(/\s+/).filter(Boolean)){n++;if(!document.getElementById(id))out.push(what(e)+where(e)+': '+a+' points at #'+id+', which is not on the page')}
}
return {n:n+seen.size,bad:out};
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
# Fields with and without a label, pointers to ids that are and are not there, and an id used twice. x1 has a
# name by the rules for a control (its title) and still no label: the two rules differ there.
FPROBE="""<input id="x1" title="Name"><input id="x2" placeholder="Search"><select id="x3"><option>One</option></select><textarea id="x4" aria-labelledby="gone"></textarea>
<div id="x5" contenteditable="true"></div><label for="nowhere">Minutes</label><input id="x6"><label>From <input id="k1"> to <input id="x7"></label><input id="x8" aria-label=" ">
<label><input id="x9" type="radio" name="pr1"> Spam</label><span id="x0" role="textbox" tabindex="0"></span>
<label>Name <input id="k2"></label><label for="k3">Date</label><input id="k3" type="date"><input id="k4" type="search" aria-label="Search" placeholder="Search">
<span id="kt">Speed</span><select id="k5" aria-labelledby="kt"><option>1</option></select><div id="k6" contenteditable="true" aria-label="Note"></div>
<label><input id="k7" type="checkbox"><span>Share</span></label><fieldset><legend>Why</legend><label><input id="k8" type="radio" name="pr2"> Spam</label></fieldset>
<div role="radiogroup" aria-label="How long"><label><input id="k9" type="radio" name="pr3"> 1 day</label></div><textarea id="k0" aria-label="Post" aria-describedby="kt"></textarea>
<input id="s3" type="hidden"><input id="s4" type="submit" value="Go"><input id="s5" hidden><input id="s6" aria-describedby="lost" aria-label="Tag"><b id="dup"></b><i id="dup"></i>"""
stops=[];kinds=set()
few=lambda l: l[:6] if len(l)<=6 else l[:6]+[f'and {len(l)-6} more']
def page(name,h=None,act=None):
    if h is not None: nav(h)
    if act: js(act);idle()
    c=js(NAMES);p=js(PICTURES);f=js(FIELDS);d=js(IDS);stops.append((name,c['n'],len(c['bad']),p['n'],len(p['bad']),f['n'],len(f['bad']),len(d['bad'])))
    kinds.update(x.split(' in ')[0] for x in c['bad']);kinds.update(x.split(' in ')[0]+': '+x.rsplit(': ',1)[1] for x in p['bad']+f['bad']);kinds.update(d['bad'])
    # A stop where no control was found was not looked at; that is a failure, not a pass.
    check(f'{name}: every control has a name',c['n']>0 and not c['bad'],few(c['bad']) or f"{c['n']} controls")
    check(f'{name}: every picture has a text alternative',not p['bad'],few(p['bad']) or f"{p['n']} pictures")
    # A stop may have no field at all (the achievements page); the count says how many were looked at.
    check(f'{name}: every field has a label, ids are unique and pointers lead somewhere',not f['bad'] and not d['bad'],few(f['bad']+d['bad']) or f"{f['n']} fields")
def caption(name,cv,cap,more=''):
    # Polls: a combo's caption changes as it plays, and the canvas is named by the same element.
    got=lambda: js(CAPTION,None,cv,cap)
    check(f'{name}: the canvas is a picture named with the caption under it',lambda: (lambda g: g and g[0]=='img' and len(g[2])>10 and g[2] in g[1] and more in g[1])(got()),got)
def region(sel): return js(REGION,sel)
# A field as a screen reader meets it: whether it is marked invalid, the ids it points at for more to say,
# the words those hold, and whether it says it is required.
FIELD=r"""
const f=document.querySelector(arguments[0]);if(!f)return null;
const by=(f.getAttribute('aria-describedby')||'').split(/\s+/).filter(Boolean);
return {invalid:f.getAttribute('aria-invalid'),by,said:by.map(i=>(document.getElementById(i)||{}).textContent||'').join(' ').replace(/\s+/g,' ').trim(),required:f.required||f.getAttribute('aria-required')==='true'};
"""
def field(sel): return js(FIELD,sel)
def put(sel,v):
    # Puts text into a field the way typing ends up: the value, then the event the page listens for.
    js("const f=document.querySelector(arguments[0]);f.value=arguments[1];f.dispatchEvent(new Event('input',{bubbles:true}))",sel,v)
def wrong(form,name,sel,words,fields,clear,still=None):
    # An error under a form, as it is heard: said in a live region that was there before, tied to the fields it
    # is about, and gone from the region and from each field at the next thing typed that could be sent.
    # fields are the ones the error is about; clear is (field, text) to type; still is (field, text) that is
    # not yet good enough, after which the error must still stand.
    r=region(sel);check(f'{name}: the place for the error is a live region on the page before there is one',bool(r) and r['role']=='alert' and r['there'] and r['text']=='' and not r['seen'],lambda: r)
    form()
    check(f'{name}: the error is said in the live region, where it can also be seen',lambda: (lambda r: r['role']=='alert' and r['there'] and r['seen'] and words in r['text'])(region(sel)),lambda: region(sel),timeout=30)
    got={f: field(f) for f in fields}
    check(f'{name}: each field the error is about is marked invalid and points at it',all(g and g['invalid']=='true' and sel[1:] in g['by'] and words in g['said'] for g in got.values()),lambda: got)
    if still:
        put(*still);r=region(sel);g=field(still[0])
        check(f'{name}: the error stands while what is typed still cannot be sent',words in r['text'] and g['invalid']=='true',lambda: (r,g))
    put(*clear);r=region(sel);got={f: field(f) for f in fields}
    check(f'{name}: the error and the marks are gone at the next thing typed that can be sent',r['there'] and r['text']=='' and all(g['invalid'] is None and sel[1:] not in g['by'] for g in got.values()),lambda: (r,got))
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
    js("document.querySelector('#a11y-probe').innerHTML=arguments[0]",FPROBE)
    f=js(FIELDS,'#a11y-probe');d=js(IDS,'#a11y-probe')
    check('the rules find each field without a label and no field with one',ids(f['bad'])==['x0','x1','x2','x3','x4','x5','x6','x7','x8','x9'] and f['n']==21,lambda: f)
    check('the rules find each pointer to an id that is not there, and the id used twice',sorted(re.search(r'#(\w+)[,:]',x.split(': ',1)[1] if ' points at ' in x else x).group(1) for x in d['bad'])==['dup','gone','lost','nowhere'],lambda: d)
    js("document.querySelector('#a11y-probe').remove()")
    # Signed out.
    page('landing','#/')
    wait_for("return !!document.querySelector('.land #demo-cv')",'the demo on the landing page',20)
    caption('landing','.land #demo-cv','.land .demo-cap')
    page('sign in','#/signin')
    U,PW,AG='#auth-form [name=username]','#auth-form [name=password]','#auth-form [name=again]'
    check('sign in: the username and the password say they are required',field(U)['required'] and field(PW)['required'],lambda: (field(U),field(PW)))
    wrong(lambda: js("const f=document.querySelector('#auth-form');f.elements.username.value='admin';f.elements.password.value='not-the-password';f.requestSubmit()"),
        'sign in with a wrong password','#auth-err','Wrong username or password.',[U,PW],(PW,'another-try'))
    page('create an account','#/signup')
    wrong(lambda: js("const f=document.querySelector('#auth-form');f.elements.username.value='shorty';f.elements.password.value=f.elements.again.value='short';f.requestSubmit()"),
        'create an account with a short password','#auth-err','Use a password of 8 to 200 characters.',[PW],(PW,'long-enough-now'),still=(PW,'shorter'))
    js("const f=document.querySelector('#auth-form');f.elements.password.value='long-enough-now';f.elements.again.value='long-enough-not';f.requestSubmit()")
    check('create an account: two passwords that differ are said, and the second is the one marked',lambda: region('#auth-err')['text']=='The two passwords do not match.' and field(AG)['invalid']=='true' and field(PW)['invalid'] is None,lambda: (region('#auth-err'),field(AG),field(PW)))
    page('skill maps, signed out','#/maps')
    page('skill guide, signed out','#/prop/nunchaku/F/F5')
    page('settings, signed out','#/settings')
    page('a page that needs an account','#/timeline')
    # The places for short messages are on the page, empty, before there is anything to say.
    for sel,what in (('#toast','messages'),('#unlock','unlocked achievements'),('#tip','hints'),('#say','what is said and not shown')):
        r=region(sel);check(f'the place for {what} is a live region that is on the page before a message',bool(r) and r['role']=='status' and r['there'] and r['text']=='' and not r['seen'],lambda: r)
    r=region('#sync');check('the note on whether changes are saved is not on the page for a visitor, who saves nothing',bool(r) and not r['there'],lambda: r)
    signin()
    # The note under the menu is the same element before and after a change is saved, and its words change in it.
    r=region('#sync');check('the note on whether changes are saved is a live region that is on the page',bool(r) and r['role']=='status' and r['there'] and r['seen'] and r['text']=='All changes saved',lambda: r)
    nav('#/prop/nunchaku/F/F1')
    js("const s=document.querySelector('#sync');s.dataset.probe='1';window.__sync=[];new MutationObserver(()=>__sync.push(s.textContent.trim())).observe(s,{childList:true,characterData:true,subtree:true});const t=document.querySelector('#panel [data-change=note]');t.value='Keep the elbow in.';t.dispatchEvent(new Event('change',{bubbles:true}))")
    check('a change is said as "Saving…" and then "All changes saved", in the same element',lambda: js("return __sync")==['Saving…','All changes saved'] and js("return document.querySelector('#sync').dataset.probe")=='1',lambda: js("return [__sync,document.querySelector('#sync').dataset.probe]"))
    js("const t=document.querySelector('#panel [data-change=note]');t.value='';t.dispatchEvent(new Event('change',{bubbles:true}))");idle()
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
    # The composer: the room left in the box is shown from 100 characters left and said when it passes 20
    # left, when none is left and when the text is too long, not at each letter.
    T='#compose-form textarea';said=lambda: region('#say')['text'];count=lambda: txt('#compose-count')
    heard=[]
    for n in (1890,1975,1980,1985,2000,1999):
        put(T,'a'*n);heard.append((n,count(),said()))
    check('the room left in a post is shown from 100 characters left and said at 20 left and at none, not at each letter',
        heard==[(1890,'',''),(1975,'25 characters left',''),(1980,'20 characters left','20 characters left.'),(1985,'15 characters left','20 characters left.'),(2000,'No characters left','No characters left.'),(1999,'1 character left','1 character left.')],lambda: heard)
    g=field(T);check('the box points at its counter, so the count is read with it',g['by']==['compose-count'] and g['said']=='1 character left' and g['invalid'] is None,lambda: g)
    # A person cannot type past the limit, but a draft the page puts back can be longer; then Post is refused.
    put(T,'a'*2001)
    check('a post that is too long is marked invalid and said so, before it is sent',lambda: field(T)['invalid']=='true' and count()=='1 character too many' and said()=='1 character too many.',lambda: (field(T),count(),said()))
    put(T,'a'*1900);check('the mark goes when the post fits again',field(T)['invalid'] is None and count()=='100 characters left',lambda: (field(T),count()))
    wrong(lambda: (put(T,'a'*2001),js("document.querySelector('#compose-form').requestSubmit()")),
        'a post that is too long','#compose-err','A post holds 2000 characters. This one has 2001.',[T],(T,'a'*1990),still=(T,'a'*2003))
    put(T,'')
    page('skill picker',act="document.querySelector('[data-act=pick-open]').click()")
    js("document.querySelector('[data-act=pick-add]').click()");idle()
    check('a trick tagged on the draft is said, with how many of the 10 it makes',lambda: re.fullmatch(r'\w+ tagged, 1 of 10 tricks\.',said()),said)
    page('composer with a skill tagged')
    # A picture is put on the hidden file input the way a person choosing a file would: the click on Picture
    # takes the cursor out of the search box first.
    js("document.activeElement.blur();const u=Uint8Array.from(atob(arguments[0]),c=>c.charCodeAt(0)),d=new DataTransfer();d.items.add(new File([u],'pair.png',{type:'image/png'}));const i=document.querySelector('[data-change=compose-img]');i.files=d.files;i.dispatchEvent(new Event('change',{bubbles:true}))",png)
    wait_for("return !!document.querySelector('.compose-img img')",'the picture to show in the composer',20);idle()
    check('a picture attached to the draft is said',lambda: said()=='Picture attached.',said)
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
    print(f'{sum(s[5] for s in stops)} fields looked at, {sum(s[6] for s in stops)} without a label, {sum(s[7] for s in stops)} ids used twice or pointed at and not there')
    # A11Y_LIST=1 lists each kind of control or picture without a name, once, to work through.
    if os.environ.get('A11Y_LIST'): print('\n'.join(sorted(kinds)))
finally: done()
