# Single-profile mode: run with the server started with FLOWA_MODE=single (run.sh passes the setting on, and
# the workflow gives it to this suite). single.py [<out dir>]
# The admin run.sh makes is the one account, the owner. A visitor at / gets the owner's profile as a small page
# the server writes: the name, the bio, links out that tell the site nothing about where the visitor came from,
# no script, no post and no way to make an account. It is read at 320 px wide and in both colour schemes with
# nothing off the screen, and its names, pictures and contrast are judged with the walks of a11y.py and
# contrast.py, taken from their source (those suites start the server in local mode and sign people up, which
# single mode refuses, so the page cannot be a stop of theirs). "Sign in" leads to the app's form, the owner
# signs in and gets the app at /, and after signing out the visitor's page is back.
# With an out dir, the visitor's page is also written there at desktop and phone width (visitor.png,
# visitor-phone.png) and added to the index.json the screenshots job publishes.
import sys,os,io,ast,json,urllib.error
os.environ['FLOWA_POINTER']='coarse'  # phone() zooms from the browser's side, which a touch run is allowed to do
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
import h as H
out=sys.argv[1] if len(sys.argv)>1 else None
HERE=os.path.dirname(os.path.abspath(__file__))
def strings_of(file,names):
    # The named string constants of another suite, without running it: each is a string or a sum of strings
    # and the names before it, so the walks here are the ones that suite makes and cannot drift from them.
    ns={}
    for n in ast.parse(open(os.path.join(HERE,file)).read()).body:
        if isinstance(n,ast.Assign) and len(n.targets)==1 and isinstance(n.targets[0],ast.Name) and n.targets[0].id in names:
            ns[n.targets[0].id]=eval(compile(ast.Expression(n.value),file,'eval'),{'__builtins__':{}},dict(ns))
    missing=set(names)-set(ns)
    if missing: raise SystemExit(f'{file} no longer has {sorted(missing)}')
    return ns
A=strings_of('a11y.py',['LIB','NAMES','PICTURES','IDS'])
C=strings_of('contrast.py',['LIB','CHECK','KNOWN'])
NAME='Ana Spins';BIO='Poi and staff since 2019.\nFind me at the Thursday jam in the park.';POST='A post only the owner reads'
INST={'description':'The practice log of one spinner.','contact':'ana@example.org','rules':[{'text':'Be kind','hint':''}]}
VISITOR="return !!document.querySelector('#pub-name')"
def visitor(what='the visitor\'s page'):
    cmd('WebDriver:Navigate',url=BASE);wait_for(VISITOR,what,20)
    wait_for("return [...document.images].every(i=>i.complete)&&[...document.styleSheets].length>0",'its style sheet and pictures',20)
def themed(t):
    # The light scheme is set as contrast.py sets it, by data-theme on the root, which app.css colours with the
    # same tokens as a system that asks for light (test/contrast.js holds the two blocks equal); the dark one is
    # what this Firefox reports the system asks for.
    js("if(arguments[0]==='light')document.documentElement.dataset.theme='light';else delete document.documentElement.dataset.theme",t)
BG="return getComputedStyle(document.body).backgroundColor"
# Everything that reaches past the right edge of the window, or makes the page scroll sideways.
WIDE="""const W=document.documentElement.clientWidth,o=[];if(document.documentElement.scrollWidth>W)o.push('the page scrolls sideways: '+document.documentElement.scrollWidth+' px in '+W);
for(const e of document.body.querySelectorAll('*')){const r=e.getBoundingClientRect();if(r.width&&(r.right>W+0.5||r.left<-0.5))o.push(e.tagName.toLowerCase()+'.'+e.className+': '+Math.round(r.left)+' to '+Math.round(r.right))}return o"""
def picture(name,title,full=True):
    if not out: return
    from PIL import Image
    img=Image.open(io.BytesIO(base64.b64decode(cmd('WebDriver:TakeScreenshot',full=full)['value']))).convert('RGB')
    img.save(f'{out}/{name}.png',optimize=True);made.append({'file':name+'.png','title':title});check(f'{name} ({img.width}x{img.height})',True)
made=[]
try:
    op=admin()
    api(op,'me',{'displayName':NAME,'bio':BIO,'props':['nunchaku'],'links':{'instagram':'ana.spins','youtube':'@anaspins'},'stats':{'xp':98765,'level':43,'streak':321}},'PUT')
    api(op,'posts',{'body':POST})
    cur=get_json(op,'admin/instance');api(op,'admin/instance',dict(INST,updatedAt=cur['updatedAt']),'PUT')
    me=get_json(op,'me')
    check('the server runs in single mode, and the admin is the owner',me.get('single') is True and me['user'].get('owner') is True,lambda: (me.get('single'),me['user'].get('owner'),'start the server with FLOWA_MODE=single'))
    def closed():
        try: api(urllib.request.build_opener(),'signup',{'username':'someone','password':'a fine long password'});return 'it made an account'
        except urllib.error.HTTPError as e: return e.code
    check('sign-up answers 403 though the signups flag is on',closed()==403 and get_json(op,'me')['flags']['signups'],closed)

    # The visitor's page, at desktop width, in the dark scheme the system asks for.
    cmd('WebDriver:NewSession',capabilities={});H.started=True;cmd('WebDriver:SetWindowRect',width=1440,height=1000)
    visitor()
    check('the visitor sees the owner\'s name in the one h1',lambda: js("return [...document.querySelectorAll('h1')].map(h=>h.textContent)")==[NAME],lambda: js("return [...document.querySelectorAll('h1')].map(h=>h.textContent)"))
    check('and the bio, with its line break',lambda: js("const p=document.querySelector('.pub-text');return p&&p.innerText")==BIO,lambda: js("return (document.querySelector('.pub-text')||{}).innerText"))
    links=js("return [...document.querySelectorAll('a[href^=http]')].map(a=>[a.textContent,a.href,a.rel])")
    check('links out to Instagram and YouTube, each with rel nofollow noopener noreferrer',[l[:2] for l in links]==[['Instagram','https://www.instagram.com/ana.spins/'],['YouTube','https://www.youtube.com/@anaspins']] and all(l[2]=='nofollow noopener noreferrer' for l in links),lambda: links)
    check('the page holds no script at all',js("return document.scripts.length+document.querySelectorAll('[onclick],[onload],[onerror]').length")==0)
    words=js("return document.body.innerText")
    check('no post, number or mode is on it',not any(w in words.lower() for w in (POST.lower(),'98765','321','level','streak','xp','single','member')),lambda: words)
    check('no way to make an account: no form, field or link to sign up',js("return document.querySelectorAll('form,input,button,textarea,select,a[href*=signup]').length")==0)
    check('it says what this Flowa is, from the instance information',INST['description'] in words and 'ana@example.org' in words and 'Be kind' in words,lambda: words)
    check('lang, a header and a main landmark, one h1',js("return [document.documentElement.lang,document.querySelectorAll('header').length,document.querySelectorAll('main').length,document.querySelectorAll('h1').length]")==['en',1,1,1])
    check('the dark scheme the system asks for colours it',js("return matchMedia('(prefers-color-scheme: dark)').matches")==True and js(BG)=='rgb(14, 16, 21)',lambda: js(BG))
    for t in ('dark','light'):
        themed(t)
        n,p,d=js(A['NAMES']),js(A['PICTURES']),js(A['IDS'])
        check(f'{t}: every link has a name, every picture a text alternative, ids are unique',n['n']>0 and not n['bad'] and not p['bad'] and not d['bad'],lambda: (n,p,d))
        r=js(C['CHECK'],None,False,C['KNOWN'],'')
        check(f'{t}: text and the edges of controls stand out (contrast.py\'s walk)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
    check('the light scheme colours it too',js(BG)=='rgb(245, 245, 242)',lambda: js(BG))
    themed('dark')
    picture('visitor','Single-profile mode: the visitor\'s page')

    # Sign in leads to the app's form; the owner signs in and gets the app at /.
    click('a[href="index.html#/signin"]')
    wait_for("return !!document.querySelector('#auth-form')",'the sign-in form',30)
    check('Sign in opens the app\'s sign-in form, with no link to create an account',lambda: js("return [!!document.querySelector('#auth-form[data-mode=signin]'),document.querySelectorAll('a[href=\"#/signup\"]').length]")==[True,0],lambda: js("return document.querySelector('main').innerText"))
    nav('#/signup')
    check('the sign-up address says sign-ups are off, with no form',lambda: js("return [!!document.querySelector('#auth-form'),document.querySelector('main').innerText.includes('Sign-ups are off')]")==[False,True],lambda: js("return document.querySelector('main').innerText"))
    seen=json.loads(urllib.request.build_opener().open(BASE+'api/me',timeout=20).read())
    check('a visitor\'s /api/me does not say the mode, and gives the signups flag off','single' not in seen and seen['flags']['signups'] is False,lambda: (seen.get('single'),seen['flags'].get('signups')))
    signin('admin')
    load(BASE)
    check('the owner signed in gets the app at /, not the visitor\'s page',lambda: js("return [!!document.querySelector('#main .page-h, #main h1'),!!document.querySelector('#pub-name'),(document.querySelector('.nav-profile .tiny')||{}).textContent]")==[True,False,'@admin'],
          lambda: js("return location.href+' '+document.title"))
    nav('#/timeline')
    check('the timeline holds the owner\'s post, with no People list',lambda: POST in js("return document.querySelector('#main').innerText") and not js("return !!document.querySelector('.people')"))
    nav('#/profile');click('[data-act=signout]')
    wait_for(VISITOR,'the visitor\'s page after signing out',20)
    check('signing out goes back to the visitor\'s page at /',js("return [location.pathname,document.querySelector('h1').textContent]")==['/',NAME])

    # A phone, 320 px wide, in both schemes: nothing reaches past the edge. Then the picture at 390 px.
    visitor()
    # The zoom can land a few px off at first; asked again from where it is, it settles (at most 320 px is the test).
    got=phone(320)
    if got!=320: got=phone(320)
    for t in ('dark','light'):
        themed(t)
        bad=js(WIDE)
        check(f'at {got} px wide, {t}: nothing is off the screen',got<=320 and not bad,lambda: (got,bad[:5]))
    themed('dark')
    phone(390);picture('visitor-phone','Single-profile mode: the visitor\'s page, phone')
    if out:
        idx=f'{out}/index.json'
        kept=[m for m in json.load(open(idx)) if not m['file'].startswith('visitor')] if os.path.exists(idx) else []
        json.dump(kept+made,open(idx,'w'));check(f'the pictures are in {idx}',len(made)==2)
except Exception as x: check('the single-profile suite ran',False,repr(x)[:300])
finally: done()
raise SystemExit(1 if errs else 0)
