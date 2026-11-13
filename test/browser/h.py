# A small driver for headless Firefox over Marionette (the Pi has no Playwright or Chromium).
import json,socket,subprocess,tempfile,time,base64,urllib.request,http.cookiejar,os,shutil,atexit
BASE='http://127.0.0.1:3170/'
prof=tempfile.mkdtemp(dir=os.environ.get('RUNNER_TEMP'))  # a snap Firefox (CI) cannot read /tmp
open(prof+'/user.js','w').write('user_pref("marionette.port",2935);user_pref("ui.systemUsesDarkTheme",1);user_pref("signon.rememberSignons",false);user_pref("webgl.force-enabled",true);user_pref("webgl.disable-fail-if-major-performance-caveat",true);user_pref("privacy.reduceTimerPrecision",false);\n')  # the page's clock is not rounded to a millisecond, so a frame can be timed
# FLOWA_POINTER=coarse makes Firefox report a touch screen (pointer: coarse, no hover), as a phone does.
COARSE=os.environ.get('FLOWA_POINTER')=='coarse'
if COARSE: open(prof+'/user.js','a').write('user_pref("ui.primaryPointerCapabilities",1);user_pref("ui.allPointerCapabilities",1);')
# FLOWA_MOTION=reduce makes Firefox report that the person asked their system for less motion
# (prefers-reduced-motion: reduce), as pages.py asks for its second set of pictures.
STILL=os.environ.get('FLOWA_MOTION')=='reduce'
if STILL: open(prof+'/user.js','a').write('user_pref("ui.prefersReducedMotion",1);')
FIREFOX=os.environ.get('FIREFOX') or next((b for b in ('firefox-esr','firefox') if shutil.which(b)),'firefox-esr')
# Zooming the page for phone() is done from the browser's own context, which Firefox allows only when asked:
# by a flag in Firefox 140, by an environment variable in later ones. Both are given, and only for a touch run.
ff=subprocess.Popen([FIREFOX,'--headless','--marionette','-no-remote','-profile',prof]+(['--remote-allow-system-access'] if COARSE else []),
    env=dict(os.environ,MOZ_REMOTE_ALLOW_SYSTEM_ACCESS='1') if COARSE else None,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
# The profile grows to about 100 MB per run; it is removed when the suite ends, however it ends.
def _cleanup():
    try: ff.kill(); ff.wait(5)
    except Exception: pass
    shutil.rmtree(prof,ignore_errors=True)
atexit.register(_cleanup)
for _ in range(60):
    try: s=socket.create_connection(('127.0.0.1',2935));break
    except OSError: time.sleep(1)  # fixed: Firefox is still starting and has no page to ask
buf=b''
def recv():
    global buf
    while b':' not in buf: buf+=s.recv(65536)
    n,r=buf.split(b':',1);n=int(n)
    while len(r)<n: r+=s.recv(1<<20)
    buf=r[n:];return json.loads(r[:n])
recv();i=0
def cmd(name,**p):
    global i;i+=1;m=json.dumps([0,i,name,p]).encode();s.sendall(str(len(m)).encode()+b':'+m);r=recv()
    if r[2]: raise RuntimeError(f'{name}: {r[2]}')
    return r[3]
js=lambda c,*a: cmd('WebDriver:ExecuteScript',script=c,args=list(a),sandbox=None)['value']
def shot(path): open(path,'wb').write(base64.b64decode(cmd('WebDriver:TakeScreenshot',full=False)['value']))
# What a test waits for is named, never guessed as a number of seconds. HOOK is put into the page by a test,
# not shipped with it: it counts the requests the page has sent and not yet handled, and the timers under a
# second it has set (a save is sent 350 ms after a change), so idle() knows when an action has run its course.
# It also counts address changes for nav() and collects page errors for done().
HOOK="""if(!window.__q){const q=window.__q={n:0,t:new Set(),nav:0},st=window.setTimeout,ct=window.clearTimeout,f0=window.fetch;
window.fetch=function(...a){q.n++;let open=true;const end=()=>{if(open){open=false;st.call(window,()=>q.n--,0)}};
  return f0.apply(window,a).then(r=>{let read=false;
    for(const m of ['json','text','blob','arrayBuffer','formData']){const o=r[m];r[m]=function(){read=true;return o.call(r).finally(end)}}
    st.call(window,()=>{if(!read)end()},0);return r},e=>{end();throw e})};
window.setTimeout=function(fn,ms,...a){if(typeof fn!=='function'||ms>=1000)return st.call(window,fn,ms,...a);
  const id=st.call(window,()=>{q.t.delete(id);fn(...a)},ms);q.t.add(id);return id};
window.clearTimeout=function(id){q.t.delete(id);return ct.call(window,id)};
window.addEventListener('hashchange',()=>q.nav++);
window.__e=[];window.addEventListener('error',e=>__e.push(e.message+' @'+(e.filename||'').split('/').pop()+':'+e.lineno));window.addEventListener('unhandledrejection',e=>__e.push('rej '+String(e.reason)));document.addEventListener('securitypolicyviolation',e=>__e.push('CSP '+e.violatedDirective))}"""
def poll(cond,timeout=10):
    # Asks every 50 ms at first and less often as time passes, down to every 250 ms, so a slow machine
    # is not kept busy answering. Returns what cond gave last and whether that was in time.
    t0=time.monotonic();dt=.05
    while True:
        v=cond()
        if v or time.monotonic()-t0>=timeout: return v,bool(v)
        time.sleep(dt);dt=min(.25,dt*1.5)  # fixed: the pause between two looks
def wait_for(cond,what,timeout=10):
    # Waits until cond() is true and returns its value. cond is a function, or a line of JS for the page.
    # When the time runs out the suite fails with "waited 10 s for <what>" and goes on with the last value.
    v,ok=poll((lambda: js(cond)) if isinstance(cond,str) else cond,timeout)
    if not ok: check(f'waited {timeout:g} s for {what}',False,v)
    return v
RUNNING="document.getAnimations().filter(a=>a.playState==='running'&&a.effect&&a.effect.getComputedTiming().endTime!==Infinity)"
def idle(what='the page to finish its requests',timeout=10):
    # True once no request is unanswered, no short timer is pending and no transition or animation is still
    # running, twice in a row with the same number of files fetched (requests the page sent before HOOK was
    # there are seen only that way). An animation that never ends is not waited for. When the time runs out
    # the failure says what was still going on.
    last=[None]
    def quiet():
        # A page that is being replaced refuses to answer, or answers nothing; that is not quiet.
        try: v=js(HOOK+f";return [__q.n+__q.t.size+{RUNNING}.length,performance.getEntriesByType('resource').length]")
        except RuntimeError: v=None
        if not v: last[0]=None;return False
        ok=v[0]==0 and v==last[0];last[0]=v;return ok
    def busy():
        try: return js(HOOK+f";return {{requests:__q.n,timers:__q.t.size,animations:{RUNNING}.map(a=>(a.animationName||a.transitionProperty||'animation')+' on '+(a.effect.target?a.effect.target.tagName.toLowerCase()+'.'+a.effect.target.className:'?')),files:performance.getEntriesByType('resource').length}}")
        except RuntimeError as e: return str(e)[:200]
    ok=poll(quiet,timeout)[1]
    if not ok: check(f'waited {timeout:g} s for {what}',False,busy())
    return ok
page_errs=[]  # errors from pages that were since reloaded
def load(url=None,what='the page to load'):
    # Opens url, or reloads the page, and waits for the first drawing. Errors of the page that is left are kept.
    try: page_errs.extend(js("return window.__e||[]"))
    except Exception: pass
    cmd('WebDriver:Navigate',url=url) if url else cmd('WebDriver:Refresh')
    def drawn():
        # Between two pages the browser can answer from an empty document, or refuse to answer at all.
        try: return js(HOOK+";const m=document.querySelector('#main');return !!m&&m.childElementCount>0")
        except RuntimeError: return False
    wait_for(drawn,what,30);idle()
def nav(h):
    # Changes the address and waits for the page to have drawn it and fetched what it needs.
    n,moved=js(HOOK+";const b=location.hash,n=__q.nav;location.hash=arguments[0];return [n,location.hash!==b]",h)
    if moved: wait_for(lambda: js("return __q.nav")>n,f'the page to open {h}')
    idle()
def figure(timeout=30):
    # The 3D figure is fetched after the first drawing. True once it is ready; False when it has said it
    # cannot be drawn here (a browser with no WebGL), or did not come in time.
    return poll(lambda: js("const F=window.Figure3D;return F&&F.ready?1:F&&F.failed?2:0"),timeout)[0]==1
def frames(n=2):
    # Lets the page draw n frames, so a picture taken next shows what the last step changed.
    js("let n=arguments[0];return new Promise(r=>{const f=()=>n-->0?requestAnimationFrame(f):r(1);f()})",n)
# A canvas as a number that changes when its picture does, and how many colours it holds: [number, colours].
SNAP="const c=document.querySelector(arguments[0]||'#demo-cv');if(!c)return null;const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let h=0,set=new Set();for(let i=0;i<d.length;i+=4*41){h=(h*31+d[i]+d[i+1]*3+d[i+2]*7)>>>0;set.add(d[i]<<16|d[i+1]<<8|d[i+2])}return [h,set.size]"
def holds(sel='#demo-cv',secs=.4):
    # True when the canvas is drawn and shows the same picture after a while.
    a=js(SNAP,sel);time.sleep(secs)  # fixed: a picture counts as still only if it has not changed after a while
    return bool(a) and a[1]>=4 and js(SNAP,sel)==a
def moves(sel='#demo-cv',timeout=2):
    # True when the canvas shows another picture within the time: a clip that plays draws a new frame.
    a=js(SNAP,sel);return bool(a) and poll(lambda: (lambda s: bool(s) and s[0]!=a[0])(js(SNAP,sel)),timeout)[1]
click=lambda sel: js("document.querySelector(arguments[0]).click()",sel)
# Keys, for a suite that works the page the way a person without a mouse does. The browser is sent the key
# presses and moves the focus itself: such a suite never focuses or clicks from the page's own JS, and asks
# the page only where the focus is.
TAB,ENTER,ESC,SPACE,SHIFT,CTRL,BACKSPACE,LEFT,UP,RIGHT,DOWN='\ue004','\ue007','\ue00c','\ue00d','\ue008','\ue009','\ue003','\ue012','\ue013','\ue014','\ue015'
def key(*ks,hold=()):
    # Presses each key in turn and lets it go, with the keys in hold kept down meanwhile (Shift for Shift+Tab).
    a=[{'type':'keyDown','value':k} for k in hold]+[x for k in ks for x in ({'type':'keyDown','value':k},{'type':'keyUp','value':k})]+[{'type':'keyUp','value':k} for k in reversed(hold)]
    cmd('WebDriver:PerformActions',actions=[{'type':'key','id':'kb','actions':a}])
    # Tab past the last control of the page takes the focus out of it, to the browser's own bar. Marionette
    # can only send keys to the page, not to that bar, so later keys would reach a page that no longer has
    # the focus: Firefox then moves the page's focused element with no focus event and draws no ring. A
    # person's next Tab goes through the bar and back into the page with the focus, at its first control
    # (Shift+Tab: its last). That is done here by giving the window its focus back, and by dropping the
    # caret a text box can leave in the page, from which Firefox would start the next Tab instead of
    # from the top. The press that left still finds no element of the page focused.
    if TAB in ks and not js("if(document.hasFocus())return true;getSelection().removeAllRanges();return false"):
        cmd('WebDriver:SwitchToWindow',handle=cmd('WebDriver:GetWindowHandle')['value'],focus=True)
def type_text(s): key(*s)  # one key press for each letter
def point(x,y):
    # A click of the mouse at a place in the window (px from its top left), for a check about a person who uses
    # the pointer: the browser is sent the pointer's move, press and release, as it is sent keys.
    cmd('WebDriver:PerformActions',actions=[{'type':'pointer','id':'mouse','parameters':{'pointerType':'mouse'},'actions':[{'type':'pointerMove','x':int(x),'y':int(y),'origin':'viewport'},{'type':'pointerDown','button':0},{'type':'pointerUp','button':0}]}])
# Where the focus is: [a number that stays with the element, what it is], or None when no element of the page
# has it (the focus is on the page as a whole, or outside it).
WHO="""const e=document.activeElement;if(!e||e===document.body||e===document.documentElement)return null;
const k=window.__who||(window.__who={m:new WeakMap(),n:0});if(!k.m.has(e))k.m.set(e,++k.n);
const c=typeof e.className==='string'?e.className.trim().split(/\\s+/).filter(Boolean).join('.'):'',t=(e.getAttribute('aria-label')||e.textContent||e.getAttribute('name')||'').replace(/\\s+/g,' ').trim().slice(0,30);
return [k.m.get(e),e.tagName.toLowerCase()+(e.id?'#'+e.id:'')+(c?'.'+c:'')+(e.dataset.act?'[act='+e.dataset.act+']':'')+(t?' "'+t+'"':'')]"""
def active(): return js(WHO)
def on(sel):
    # True when the focus is on an element that matches the selector.
    return js("const e=document.activeElement;return !!e&&e!==document.body&&e.matches(arguments[0])",sel)
def tab_to(sel,what=None,limit=300,back=False):
    # Presses Tab (Shift+Tab with back) until the focus is on an element that matches the selector. True when
    # it got there. When the focus has gone all the way round, or the limit is reached, the suite fails with
    # the list of what it passed on the way.
    look=lambda: js("const e=document.activeElement;return [(function(){"+WHO+"})(),!!e&&e!==document.body&&e.matches(arguments[0])]",sel)
    seen=[];ids=set();last=None;same=gone=0;a,there=look()
    for _ in range(limit):
        if there: return True
        key(TAB,hold=(SHIFT,) if back else ());a,there=look()
        if not a:
            # Past the last control the focus may rest on the page as a whole, or on the browser's own bars, for a few presses.
            gone+=1;last=None;seen.append('(no element of the page)')
            if gone>12: break
            continue
        if a[0]==last:
            # A date field keeps the focus while Tab walks its day, month and year; more presses than that is a trap.
            same+=1
            if same>8: seen.append(a[1]+' (the focus is stuck here)');break
            continue
        if a[0] in ids and not there: seen.append(a[1]+' (again: all the way round)');break
        ids.add(a[0]);last=a[0];same=0;seen.append(a[1])
    if there: return True
    check(f'{"Shift+Tab" if back else "Tab"} reaches {what or sel}',False,f'not reached in {len(seen)} presses; passed: '+' > '.join(seen[:40])+(' ...' if len(seen)>40 else ''))
    return False
txt=lambda sel: js("const e=document.querySelector(arguments[0]);return e?e.textContent.trim():null",sel)
errs=[];times=[];clock=[time.monotonic()]
def check(n,ok,x='',timeout=10):
    # ok is a value, or a function that is asked until it is true or the time runs out. x says more when the
    # check fails; give a function to have it worked out then, not before the wait.
    late=''
    if callable(ok):
        ok,intime=poll(ok,timeout)
        if not intime: late=f'(waited {timeout:g} s)'
    if callable(x): x=x() if not ok else ''
    print(('PASS ' if ok else 'FAIL ')+n,*([late] if late else []),x if not ok or x=='' else str(x)[:110],flush=True)
    if not ok: errs.append(n)
    # What this check cost: the time since the check before it, so the steps that led to it are counted in.
    now=time.monotonic();times.append((now-clock[0],n));clock[0]=now
def api(op,path,body,method='POST'):
    return json.loads(op.open(urllib.request.Request(BASE+'api/'+path,data=json.dumps(body).encode(),headers={'Content-Type':'application/json'},method=method),timeout=20).read() or b'null')
def admin():
    op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(op,'signin',{'username':'admin','password':'flowa-test-only'});api(op,'flags',{'flags':{'details':True,'references':True,'demos':True,'tour':False}},'PUT')  # every flag but follows is on by default; the guided tour would cover the pages under test
    # FLOWA_AVATARS=1 runs a suite with avatars on and the admin wearing a tall, broad avatar.
    if os.environ.get('FLOWA_AVATARS'):
        api(op,'flags',{'flags':{'avatars':True}},'PUT')
        api(op,'me',{'avatarCfg':{'v':1,'body':'tall','build':'broad','skin':'#72422a','hair':'bun','hairColor':'#1c1714','eyes':'round','brows':'straight','mouth':'smile','top':'hoodie','topColor':'#5b3f8c','bottoms':'shorts','bottomsColor':'#1f2a44','shoes':'hightops','shoesColor':'#f2f0e9','hat':'cap','hatColor':'#c4572e','glasses':'none','wristbands':'yes','wristColor':'#d9a441','props':{}}},'PUT')
    return op
started=False
def start(w=1440,h=1100):
    global started;started=True
    cmd('WebDriver:NewSession',capabilities={});cmd('WebDriver:SetWindowRect',width=w,height=h)
    load(BASE)
zoom=1.0  # the zoom phone() has set so far
def phone(width=390):
    # A Firefox window is never narrower than 450 px, so the page is zoomed until the window holds `width` px
    # of it. Zoom is set from the browser's own side, which is why a touch run starts Firefox with system access.
    # The first try can land a few px off (392 for 390 on the runner's Firefox), so the zoom is corrected from
    # what the page then reports. The width the page ends with is returned.
    global zoom
    cmd('WebDriver:SetWindowRect',width=450,height=900);got=js("return innerWidth")
    for _ in range(4):
        if got==width: break
        zoom*=got/width
        cmd('Marionette:SetContext',value='chrome')
        try: js("return FullZoom.setZoom(arguments[0],gBrowser.selectedBrowser)",zoom)
        finally: cmd('Marionette:SetContext',value='content')
        got=poll(lambda: (lambda w: w if w!=got else None)(js("return innerWidth")),2)[0] or got  # the zoom takes a moment to reach the page
    return got
def signin(name='admin'):
    # Checking a password takes a second or two on a small machine, so wait for the page to show who signed in.
    nav('#/signin');js("const f=document.querySelector('#auth-form');f.elements.username.value=arguments[0];f.elements.password.value='flowa-test-only';f.requestSubmit()",name)
    wait_for(lambda: js("return (document.querySelector('.nav-profile .tiny')||{}).textContent")=='@'+name,f'@{name} to be signed in',30);idle()
def done():
    # A suite that skipped never opened a page; Firefox is closed either way, so none is left running.
    try:
        if started: e=page_errs+(js("return window.__e") or []);check('no page errors',not e,e)
    except Exception as x: check('no page errors',False,x)
    try: cmd('Marionette:Quit')
    except Exception: pass
    try: ff.wait(10)
    except Exception: ff.kill()
    # Seconds per check, slowest first, for run.sh and the pull request comment (test/slowest.sh).
    for secs,n in sorted(times,key=lambda t:-t[0]): print(f'TIME {secs:.1f} {n}')
    print('FAILURES:',errs or 'none')
