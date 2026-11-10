# A small driver for headless Firefox over Marionette (the Pi has no Playwright or Chromium).
import json,socket,subprocess,tempfile,time,base64,urllib.request,http.cookiejar,os,shutil,atexit
BASE='http://127.0.0.1:3170/'
prof=tempfile.mkdtemp(dir=os.environ.get('RUNNER_TEMP'))  # a snap Firefox (CI) cannot read /tmp
open(prof+'/user.js','w').write('user_pref("marionette.port",2935);user_pref("ui.systemUsesDarkTheme",1);user_pref("signon.rememberSignons",false);user_pref("webgl.force-enabled",true);user_pref("webgl.disable-fail-if-major-performance-caveat",true);\n')
FIREFOX=os.environ.get('FIREFOX') or next((b for b in ('firefox-esr','firefox') if shutil.which(b)),'firefox-esr')
ff=subprocess.Popen([FIREFOX,'--headless','--marionette','-no-remote','-profile',prof],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
# The profile grows to about 100 MB per run; it is removed when the suite ends, however it ends.
def _cleanup():
    try: ff.kill(); ff.wait(5)
    except Exception: pass
    shutil.rmtree(prof,ignore_errors=True)
atexit.register(_cleanup)
for _ in range(60):
    try: s=socket.create_connection(('127.0.0.1',2935));break
    except OSError: time.sleep(1)
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
def nav(h,t=.6): js("location.hash=arguments[0]",h);time.sleep(t)
click=lambda sel: js("document.querySelector(arguments[0]).click()",sel)
txt=lambda sel: js("const e=document.querySelector(arguments[0]);return e?e.textContent.trim():null",sel)
errs=[]
def check(n,ok,x=''):
    print(('PASS ' if ok else 'FAIL ')+n,x if not ok or x=='' else str(x)[:110],flush=True)
    if not ok: errs.append(n)
def api(op,path,body,method='POST'):
    return json.loads(op.open(urllib.request.Request(BASE+'api/'+path,data=json.dumps(body).encode(),headers={'Content-Type':'application/json'},method=method),timeout=20).read() or b'null')
def admin():
    op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(op,'signin',{'username':'admin','password':'flowa-test-only'});api(op,'flags',{'flags':{'details':True,'references':True,'demos':True,'tour':False}},'PUT')  # every flag is on by default; the guided tour would cover the pages under test
    # FLOWA_AVATARS=1 runs a suite with avatars on and the admin wearing a tall, broad avatar.
    if os.environ.get('FLOWA_AVATARS'):
        api(op,'flags',{'flags':{'avatars':True}},'PUT')
        api(op,'me',{'avatarCfg':{'v':1,'body':'tall','build':'broad','skin':'#72422a','hair':'bun','hairColor':'#1c1714','eyes':'round','brows':'straight','mouth':'smile','top':'hoodie','topColor':'#5b3f8c','bottoms':'shorts','bottomsColor':'#1f2a44','shoes':'hightops','shoesColor':'#f2f0e9','hat':'cap','hatColor':'#c4572e','glasses':'none','wristbands':'yes','wristColor':'#d9a441','props':{}}},'PUT')
    return op
started=False
def start(w=1440,h=1100):
    global started;started=True
    cmd('WebDriver:NewSession',capabilities={});cmd('WebDriver:SetWindowRect',width=w,height=h)
    cmd('WebDriver:Navigate',url=BASE);time.sleep(2.5)
    js("window.__e=[];window.addEventListener('error',e=>__e.push(e.message+' @'+(e.filename||'').split('/').pop()+':'+e.lineno));window.addEventListener('unhandledrejection',e=>__e.push('rej '+String(e.reason)));document.addEventListener('securitypolicyviolation',e=>__e.push('CSP '+e.violatedDirective))")
def signin(name='admin'):
    # Checking a password takes a second or two on a small machine, so wait for the page to show who signed in.
    nav('#/signin',.8);js("const f=document.querySelector('#auth-form');f.elements.username.value=arguments[0];f.elements.password.value='flowa-test-only';f.requestSubmit()",name)
    for _ in range(60):
        if js("return (document.querySelector('.nav-profile .tiny')||{}).textContent")=='@'+name: break
        time.sleep(.5)
    time.sleep(1.5)
def done():
    # A suite that skipped never opened a page; Firefox is closed either way, so none is left running.
    try:
        if started: e=js("return window.__e");check('no page errors',not e,e)
    except Exception as x: check('no page errors',False,x)
    try: cmd('Marionette:Quit')
    except Exception: pass
    try: ff.wait(10)
    except Exception: ff.kill()
    print('FAILURES:',errs or 'none')
