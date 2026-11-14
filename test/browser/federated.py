# A federating instance: run with the server started with FLOWA_MODE=federated, FLOWA_ORIGIN=https://flowa.test
# and a test-only FLOWA_KEY_SECRET (run.sh passes the settings on, and the workflow gives them to this suite).
# This version federates as far as discovery (docs/federation.md 5.3 and 5.4), so the suite asks the three
# addresses over HTTP as another server would, and works the member's switch in Settings with the keyboard:
#   - NodeInfo: the discovery document and the 2.1 document, their content types, the counts and the mode;
#   - WebFinger: 404 for a member who has not opted in, 200 with the self link once they have, 404 again after,
#     the CORS header on every answer, HEAD with no body, 405 with Allow for any other method;
#   - Settings: "Beyond this server" with the switch, reached with Tab and turned with Space, saved (read back
#     through the API), the focus kept on it; the warning for a name that ends with a dot;
#   - the admin dashboard's mode line, and the start log, say that this version answers discovery only, and
#     the dashboard that the origin is not announced until an answer first names a member, then since when;
#   - the card holds to the rules the keys, a11y and contrast suites hold every page to (those suites run in
#     local mode, where the card does not exist): the switch takes the focus and shows a ring of 2 px at 3:1 in
#     both themes (keys.py's STOP), every control and field has a name and no id repeats (a11y.py's NAMES,
#     FIELDS and IDS), text and the edges of controls stand out in both themes (contrast.py's CHECK), and at
#     420 px wide nothing reaches past the edge. The walks are taken from those suites' source, as single.py
#     takes them, so they cannot drift apart.
# Not covered here: the card's "unavailable" state (federation refused under a moved origin). It needs a second
# start of the server with another FLOWA_ORIGIN, and run.sh starts one; TestDBOriginFrozen (db_test.go) checks
# what /api/me tells a member and an admin then, and fedCard in web/app.js shows that text with no switch.
# federated.py [<out dir>]: with an out dir, the Settings card at desktop and at 420 px and the dashboard's mode
# line are also written there (federated-*.png) and added to the index.json the screenshots job publishes.
import sys,os,io,ast,json,base64,urllib.request,urllib.error,urllib.parse,http.cookiejar
os.environ['FLOWA_POINTER']='coarse'  # phone() zooms from the browser's side, which a touch run is allowed to do
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
import h as H
out=sys.argv[1] if len(sys.argv)>1 else None
HERE=os.path.dirname(os.path.abspath(__file__))
def strings_of(file,names):
    # The named constants of another suite, without running it, as single.py reads them; keys.py builds its
    # library with json.dumps and str, so those two names are given to the expressions.
    ns={}
    for n in ast.parse(open(os.path.join(HERE,file)).read()).body:
        if isinstance(n,ast.Assign) and len(n.targets)==1 and isinstance(n.targets[0],ast.Name) and n.targets[0].id in names:
            ns[n.targets[0].id]=eval(compile(ast.Expression(n.value),file,'eval'),{'__builtins__':{},'json':json,'str':str},dict(ns))
    missing=set(names)-set(ns)
    if missing: raise SystemExit(f'{file} no longer has {sorted(missing)}')
    return ns
A=strings_of('a11y.py',['LIB','NAMES','FIELDS','IDS'])
C=strings_of('contrast.py',['LIB','CHECK','KNOWN'])
K=strings_of('keys.py',['RING','NEED','SEL','LIB','STOP'])
# Everything in the page's main part that reaches past the window's edges, or a page that scrolls sideways. The
# menu is left out: at phone width it waits off the left edge on purpose until it is opened.
WIDE="""const W=document.documentElement.clientWidth,o=[];if(document.documentElement.scrollWidth>W)o.push('the page scrolls sideways: '+document.documentElement.scrollWidth+' px in '+W);
for(const e of document.querySelectorAll('#main *')){const r=e.getBoundingClientRect();if(r.width&&(r.right>W+0.5||r.left<-0.5))o.push(e.tagName.toLowerCase()+'.'+e.className+': '+Math.round(r.left)+' to '+Math.round(r.right))}return o"""
made=[]
def picture(name,title):
    if not out: return
    from PIL import Image
    img=Image.open(io.BytesIO(base64.b64decode(cmd('WebDriver:TakeScreenshot',full=True)['value']))).convert('RGB')
    img.save(f'{out}/{name}.png',optimize=True);made.append({'file':name+'.png','title':title});check(f'{name} ({img.width}x{img.height})',True)
def ring_ok():
    # keys.py's rule for the stop the focus is on: a ring of RING px or more at NEED:1, not cut, in both themes.
    s=js(K['STOP']);bad=[]
    for t in ('dark','light'):
        r=(s.get('ring') or {}).get(t)
        if not r: bad.append(f'{t}: no ring')
        elif r['w']<K['RING']: bad.append(f'{t}: a ring of {r["w"]} px')
        elif r.get('why'): bad.append(f'{t}: not judged ({r["why"]})')
        elif r['out']<K['NEED'] or r.get('inn',K['NEED'])<K['NEED']: bad.append(f'{t}: {r}')
        elif r['cut']: bad.append(f'{t}: cut at the {r["cut"]}')
    if s.get('hidden') or s.get('under'): bad.append(f'hidden {s.get("hidden")!r}, under {s.get("under")!r}')
    return bad
def themed(t): js("if(arguments[0]==='light')document.documentElement.dataset.theme='light';else delete document.documentElement.dataset.theme",t)
def card_rules(who):
    # a11y.py's and contrast.py's walks over the card alone, in both themes.
    for t in ('dark','light'):
        themed(t)
        n,f,d=js(A['NAMES'],'.fed-card'),js(A['FIELDS'],'.fed-card'),js(A['IDS'])
        check(f'{who}, {t}: every control and field of the card has a name, and no id repeats (a11y.py)',n['n']>0 and f['n']>0 and not n['bad'] and not f['bad'] and not d['bad'],lambda: (n,f,d))
        r=js(C['CHECK'],'.fed-card',False,C['KNOWN'],'')
        check(f'{who}, {t}: text and the edges of controls in the card stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
    themed('dark')
ORIGIN='https://flowa.test';HOST='flowa.test'
def ask(path,method='GET',data=None,headers=None):
    # One request as another server makes it: no cookie. Returns the status, the headers and the body.
    r=urllib.request.Request(BASE+path.lstrip('/'),data=data,method=method,headers=headers or {})
    try:
        with urllib.request.urlopen(r,timeout=20) as a: return a.status,a.headers,a.read()
    except urllib.error.HTTPError as e: return e.code,e.headers,e.read()
def wf(resource,extra=''): return ask('.well-known/webfinger?resource='+urllib.parse.quote(resource,safe='')+extra)
def location_of(): return js('return location.hash')
def opted(op): return get_json(op,'me').get('federation',{}).get('optedIn')
try:
    op=admin();api(op,'me',{'props':['nunchaku']},'PUT')  # past the first-run choice of props, which the admin page waits behind
    s,h,b=ask('nodeinfo/2.1')
    doc=json.loads(b) if s==200 else {}
    check('the server federates: /nodeinfo/2.1 answers, in federated mode',s==200 and doc.get('metadata',{}).get('mode')=='federated',lambda: (s,b[:200],'start the server with FLOWA_MODE=federated, FLOWA_ORIGIN and FLOWA_KEY_SECRET'))
    check('NodeInfo 2.1: its content type names the schema, and the CORS header is there',h.get('Content-Type')=='application/json; profile="http://nodeinfo.diaspora.software/ns/schema/2.1#"' and h.get('Access-Control-Allow-Origin')=='*',lambda: dict(h))
    check('NodeInfo 2.1: version, software, protocols, services, openRegistrations, usage and metadata, and nothing else',sorted(doc)==['metadata','openRegistrations','protocols','services','software','usage','version'] and doc['version']=='2.1' and doc['software']['name']=='flowa' and doc['protocols']==['activitypub'] and doc['services']=={'inbound':[],'outbound':[]},lambda: doc)
    check('NodeInfo 2.1: one account (the admin), registration open as the signups flag says',doc.get('usage',{}).get('users',{}).get('total')==1 and doc.get('openRegistrations') is True,lambda: (doc.get('usage'),doc.get('openRegistrations')))
    s,h,b=ask('.well-known/nodeinfo')
    check('the NodeInfo discovery document links the 2.1 document under the origin, as application/json',s==200 and h.get('Content-Type')=='application/json' and json.loads(b)=={'links':[{'rel':'http://nodeinfo.diaspora.software/ns/schema/2.1','href':ORIGIN+'/nodeinfo/2.1'}]},lambda: (s,dict(h),b))

    check('nothing is announced yet: NodeInfo names the origin but no member, and the dashboard says so',get_json(op,'admin/dashboard').get('federation',{}).get('announced')=='',lambda: get_json(op,'admin/dashboard').get('federation'))
    me=get_json(op,'me')
    check('the admin is told the handle, and has not opted in',{k:(me.get('federation') or {}).get(k) for k in ('handle','optedIn','label')}=={'handle':'@admin@'+HOST,'optedIn':False,'label':'Let other servers find me by my handle'} and 'Nothing you post is sent' in (me.get('federation') or {}).get('built',''),lambda: me.get('federation'))
    s,h,b=wf('acct:admin@'+HOST)
    check('WebFinger: 404 for a member who has not opted in, with the CORS header',s==404 and h.get('Access-Control-Allow-Origin')=='*',lambda: (s,dict(h)))
    nobody=wf('acct:nobody@'+HOST)
    check('and the same answer as for a name nobody has',(s,b)==(nobody[0],nobody[2]),lambda: (b,nobody[2]))
    s,h,b=ask('.well-known/webfinger')
    check('WebFinger without a resource: 400, with the CORS header',s==400 and h.get('Access-Control-Allow-Origin')=='*',lambda: (s,dict(h)))
    s,h,b=ask('.well-known/webfinger?resource=acct:admin@'+HOST,'POST',b'{}',{'Content-Type':'application/json'})
    check('WebFinger by POST: 405, Allow GET and HEAD, the CORS header',s==405 and h.get('Allow')=='GET, HEAD' and h.get('Access-Control-Allow-Origin')=='*',lambda: (s,dict(h)))

    # The switch in Settings, with the keyboard.
    start();signin('admin');nav('#/settings')
    check('Settings has "Beyond this server", with the switch off and the handle',lambda: js("const c=[...document.querySelectorAll('.card h2')].find(h=>h.textContent==='Beyond this server');const b=document.querySelector('[data-change=federates]');return !!c&&!!b&&!b.checked&&c.closest('.card').innerText.includes('@admin@flowa.test')"),
          lambda: js("return document.querySelector('main').innerText.slice(0,400)"))
    check('the switch has a name a screen reader reads',lambda: js("const b=document.querySelector('[data-change=federates]');return b&&b.closest('label').innerText.trim()")=='Let other servers find me by my handle')
    check('the card says what this version does, in the server\'s words, and promises no posts',lambda: (lambda t: 'Nothing you post is sent to them' in t and 'public posts' not in t)(js("return document.querySelector('.fed-card').innerText")),lambda: js("return document.querySelector('.fed-card').innerText"))
    if tab_to('[data-change=federates]','the switch'):
        check('the switch shows a ring of 2 px or more at 3:1 in both themes, and nothing covers it (keys.py)',not ring_ok(),ring_ok)
        key(SPACE)
        check('Space turns it on, and the server has it',lambda: opted(op) is True,lambda: get_json(op,'me').get('federation'))
        check('the focus stays on the switch, now on',lambda: on('[data-change=federates]') and js("return document.querySelector('[data-change=federates]').checked"),active)
    s,h,b=wf('acct:admin@'+HOST)
    j=json.loads(b) if s==200 else {}
    check('WebFinger: 200 once opted in, application/jrd+json, the self link to the actor id',s==200 and h.get('Content-Type')=='application/jrd+json' and h.get('Access-Control-Allow-Origin')=='*' and j=={'subject':'acct:admin@'+HOST,'aliases':[ORIGIN+'/users/admin'],'links':[{'rel':'self','type':'application/activity+json','href':ORIGIN+'/users/admin'}]},lambda: (s,dict(h),b))
    check('a short time to keep it, so that opting out reaches other servers soon',h.get('Cache-Control')=='public, max-age=300',lambda: h.get('Cache-Control'))
    s2,h2,b2=wf('ACCT:Admin@FLOWA.test.')
    check('the handle in other cases, with a trailing dot, finds the same member',s2==200 and b2==b,lambda: (s2,b2))
    s2,h2,b2=wf(ORIGIN+'/users/admin')
    check('so does the actor id',s2==200 and b2==b,lambda: (s2,b2))
    s2,h2,b2=wf('acct:admin@'+HOST,'&rel='+urllib.parse.quote('http://webfinger.net/rel/profile-page',safe=''))
    check('rel for another relation: no link, the rest as it was',s2==200 and json.loads(b2)['links']==[] and json.loads(b2)['subject']=='acct:admin@'+HOST,lambda: (s2,b2))
    s2,h2,b2=ask('.well-known/webfinger?resource=acct:admin@'+HOST,'HEAD')
    check('HEAD: 200, the headers of GET and no body',s2==200 and b2==b'' and h2.get('Content-Length')==str(len(b)) and h2.get('Access-Control-Allow-Origin')=='*',lambda: (s2,dict(h2),b2[:40]))
    key(SPACE)
    check('Space again turns it off, and WebFinger answers 404 again',lambda: opted(op) is False and wf('acct:admin@'+HOST)[0]==404)

    # The dashboard's mode line says what this version of federation does.
    nav('#/admin')
    check('the dashboard: federated mode, the origin, announced since the first answer, and discovery only',lambda: (lambda t: bool(t) and t.startswith('Instance mode federated') and 'Federating as https://flowa.test, first announced' in t and 'discovery only' in t and 'nothing is delivered' in t)(txt('.dash-mode')),lambda: (location_of(),txt('#main')[:300]))
    js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top, or the sticky bar lands in its middle
    picture('federated-dashboard','Federated mode: the dashboard says the origin and that this version answers discovery only')

    # A member whose name ends with a dot is warned when they opt in.
    jar=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(jar,'signup',{'username':'spin.','password':'flowa-test-only'})
    check('a name that ends with a dot is told Mastodon may not read it',lambda: 'Mastodon' in (get_json(jar,'me').get('federation',{}).get('warning') or ''),lambda: get_json(jar,'me').get('federation'))
    nav('#/profile');click('[data-act=signout]');idle()
    signin('spin.');nav('#/settings')
    check('no warning while the switch is off',lambda: js("return !!document.querySelector('[data-change=federates]')&&!document.querySelector('.notice.warn')"))
    if tab_to('[data-change=federates]','the switch'):
        key(SPACE)
        check('opting in shows the warning beside the switch',lambda: 'Mastodon allows those only inside a name' in (txt('.notice.warn') or ''),lambda: txt('main'))
        check('and the server has the opt-in',lambda: opted(jar) is True)
    card_rules('the card, on, with the warning')
    js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top, or the sticky bar lands in its middle
    picture('federated-settings','Federated mode: Settings, the switch beyond this server, on, with the warning for a name that ends with a dot')
    # 420 px wide (a phone; the window is zoomed from the browser's side, as single.py does), in both themes.
    got=phone(420)
    if got!=420: got=phone(420)
    for t in ('dark','light'):
        themed(t)
        bad=js(WIDE)
        check(f'Settings at {got} px wide, {t}: nothing is off the screen',got<=420 and not bad,lambda: (got,bad[:5]))
    themed('dark')
    js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top, or the sticky bar lands in its middle
    picture('federated-settings-phone','Federated mode: Settings, the switch beyond this server, phone')
    if out:
        idx=f'{out}/index.json'
        kept=[m for m in json.load(open(idx)) if not m['file'].startswith('federated-')] if os.path.exists(idx) else []
        json.dump(kept+made,open(idx,'w'));check(f'the pictures are in {idx}',len(made)==3)

    # The start said once what this version does.
    try: log=open('/tmp/flowa-test/dev.log').read()
    except OSError: log=None
    if log is None: print('SKIP the start log: /tmp/flowa-test/dev.log cannot be read here',flush=True)
    else: check('the start log says once that this version answers discovery only',log.count('this version answers discovery only (WebFinger and NodeInfo')==1,lambda: [l for l in log.splitlines() if 'federation' in l][:5])
except Exception as x: check('the federated suite ran',False,repr(x)[:300])
finally: done()
raise SystemExit(1 if errs else 0)
