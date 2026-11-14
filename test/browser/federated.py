# A federating instance: run with the server started with FLOWA_MODE=federated, FLOWA_ORIGIN=https://flowa.test
# and a test-only FLOWA_KEY_SECRET (run.sh passes the settings on, and the workflow gives them to this suite).
# This version federates as far as reading (docs/federation.md 5.3 to 5.6, 6.2, 6.5): the suite asks discovery, the
# actor documents and the public pages over HTTP as another server would, works the member's switch in Settings
# and the audience in the composer with the keyboard, and opens the public pages in the browser:
#   - NodeInfo: the discovery document and the 2.1 document, their content types, the counts and the mode;
#   - WebFinger: 404 for a member who has not opted in, 200 with the self link once they have, 404 again after,
#     the CORS header on every answer, HEAD with no body, 405 with Allow for any other method;
#   - Settings: "Beyond this server" with the switch, reached with Tab and turned with Space, saved (read back
#     through the API), the focus kept on it; the warning for a name that ends with a dot;
#   - the composer: "Who can read it", Public first, reached with Tab, a Public post marked as such; the actor
#     document, the Note and the two public pages over HTTP, a This server post their 404; the outbox, its
#     first page, the post's Create at its own id, followers, following and featured over HTTP, and a browser
#     at the outbox sent to the profile page; the post's page and
#     the profile page in the browser, held to the a11y, contrast and keys walks, at desktop and 420 px;
#   - People elsewhere, the search by handle (FRD 8): over HTTP, a handle on this instance (answered from the
#     database), and queries refused or failed with their fixed reasons (not a handle, http, a private address,
#     localhost, another port, a name that does not resolve), a visitor, another page's write and a GET
#     refused; in the browser, the box in the timeline reached with Tab, a member found and an address refused,
#     each said to a screen reader, held to the a11y, contrast and keys walks and to 420 px. This server can
#     reach no other server (private addresses are refused and no setting changes that), so a remote account,
#     the reverse check and the outbox are covered in Go only (TestDBResolveHandle, TestDBLookupRoute);
#   - the inbox over HTTP (FWR 1): an unsigned POST from another site refused as unsigned (past the write
#     guard), plain JSON 415, a GET 405 with Allow: POST, and a search for a server nobody allowed refused; a
#     signed delivery needs another server for its key,
#     so verifying, queueing and handling are Go's (TestInboxRefusals, TestDBInboxQueue, TestDBTwoFlowaInbox);
#   - the Federation tab of the Admin page (FMD 1): a domain allowed with the keyboard, read back through the
#     API and the audit log, the policy chosen with Space, the inbox's numbers showing the refusals, held to
#     the a11y and contrast walks;
#   - the admin dashboard's mode line, and the start log, say what this version serves (the inbox acts only on
#     an account deleting itself or changing its profile, nothing delivered) and that taking deliveries is off
#     until the Federation switch is turned on, and
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
# federated.py [<out dir>]: with an out dir, the Settings card at desktop and at 420 px, the dashboard's mode
# line, the composer, the public post page and profile page, and People elsewhere with a member found and with a
# failure, at desktop and 420 px, are also written there
# (federated-*.png) and added to the index.json the screenshots job publishes.
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
A=strings_of('a11y.py',['LIB','NAMES','FIELDS','IDS','PICTURES'])
C=strings_of('contrast.py',['LIB','CHECK','KNOWN'])
K=strings_of('keys.py',['RING','NEED','SEL','LIB','STOP'])
# Everything in the page's main part that reaches past the window's edges, or a page that scrolls sideways. The
# menu is left out: at phone width it waits off the left edge on purpose until it is opened.
WIDE="""const W=document.documentElement.clientWidth,o=[];if(document.documentElement.scrollWidth>W)o.push('the page scrolls sideways: '+document.documentElement.scrollWidth+' px in '+W);
for(const e of document.querySelectorAll('#main *')){const r=e.getBoundingClientRect();if(r.width&&(r.right>W+0.5||r.left<-0.5))o.push(e.tagName.toLowerCase()+'.'+e.className+': '+Math.round(r.left)+' to '+Math.round(r.right))}return o"""
# The same for a public page, which the server writes without the app: its main part is <main>.
WIDE_PUB=WIDE.replace("'#main *'","'main *'")
made=[]
def picture(name,title):
    if not out:
        # The integration job runs the suite without a directory for pictures (the screenshots job passes one):
        # each picture is then a check that cannot run here, counted as skipped, so both runs count the same.
        print(f'SKIP {name}: no directory for pictures was given',flush=True);return
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
LABEL='Share my profile and Public posts beyond this server'
AS={'Accept':'application/activity+json'}
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
    check('the admin is told the handle, and has not opted in',{k:(me.get('federation') or {}).get(k) for k in ('handle','optedIn','label')}=={'handle':'@admin@'+HOST,'optedIn':False,'label':LABEL} and 'your posts are not sent to them yet' in (me.get('federation') or {}).get('built',''),lambda: me.get('federation'))
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
    check('the switch has a name a screen reader reads',lambda: js("const b=document.querySelector('[data-change=federates]');return b&&b.closest('label').innerText.trim()")==LABEL)
    check('the card says what this version does, in the server\'s words: read, not followed, nothing sent',lambda: (lambda t: 'read your profile and the posts you mark Public' in t and 'People there can follow you' in t and 'your posts are not sent to them yet' in t)(js("return document.querySelector('.fed-card').innerText")),lambda: js("return document.querySelector('.fed-card').innerText"))
    if tab_to('[data-change=federates]','the switch'):
        check('the switch shows a ring of 2 px or more at 3:1 in both themes, and nothing covers it (keys.py)',not ring_ok(),ring_ok)
        key(SPACE)
        check('Space turns it on, and the server has it',lambda: opted(op) is True,lambda: get_json(op,'me').get('federation'))
        check('the focus stays on the switch, now on',lambda: on('[data-change=federates]') and js("return document.querySelector('[data-change=federates]').checked"),active)
    s,h,b=wf('acct:admin@'+HOST)
    j=json.loads(b) if s==200 else {}
    check('WebFinger: 200 once opted in, application/jrd+json, the self link to the actor id and the profile page',s==200 and h.get('Content-Type')=='application/jrd+json' and h.get('Access-Control-Allow-Origin')=='*' and j=={'subject':'acct:admin@'+HOST,'aliases':[ORIGIN+'/users/admin'],'links':[{'rel':'self','type':'application/activity+json','href':ORIGIN+'/users/admin'},{'rel':'http://webfinger.net/rel/profile-page','type':'text/html','href':ORIGIN+'/users/admin'}]},lambda: (s,dict(h),b))
    check('a short time to keep it, so that opting out reaches other servers soon',h.get('Cache-Control')=='public, max-age=300',lambda: h.get('Cache-Control'))
    s2,h2,b2=wf('ACCT:Admin@FLOWA.test.')
    check('the handle in other cases, with a trailing dot, finds the same member',s2==200 and b2==b,lambda: (s2,b2))
    s2,h2,b2=wf(ORIGIN+'/users/admin')
    check('so does the actor id',s2==200 and b2==b,lambda: (s2,b2))
    s2,h2,b2=wf('acct:admin@'+HOST,'&rel='+urllib.parse.quote('http://webfinger.net/rel/profile-page',safe=''))
    check('rel for the profile page: that link only, the rest as it was',s2==200 and [l['rel'] for l in json.loads(b2)['links']]==['http://webfinger.net/rel/profile-page'] and json.loads(b2)['subject']=='acct:admin@'+HOST,lambda: (s2,b2))
    s2,h2,b2=ask('.well-known/webfinger?resource=acct:admin@'+HOST,'HEAD')
    check('HEAD: 200, the headers of GET and no body',s2==200 and b2==b'' and h2.get('Content-Length')==str(len(b)) and h2.get('Access-Control-Allow-Origin')=='*',lambda: (s2,dict(h2),b2[:40]))
    key(SPACE)
    check('Space again turns it off, and WebFinger answers 404 again',lambda: opted(op) is False and wf('acct:admin@'+HOST)[0]==404)

    # The dashboard's mode line says what this version of federation does.
    nav('#/admin')
    check('the dashboard: federated mode, the origin, announced since the first answer, and what is served',lambda: (lambda t: bool(t) and t.startswith('Instance mode federated') and 'Federating as https://flowa.test, first announced' in t and 'serves the profiles and Public posts' in t and 'members can follow people on other servers' in t and 'no post is sent yet' in t)(txt('.dash-mode')),lambda: (location_of(),txt('#main')[:300]))
    js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top, or the sticky bar lands in its middle
    picture('federated-dashboard','Federated mode: the dashboard says the origin and what this version serves')
    check('the mode line says deliveries are not taken while the Federation switch is off (its default)',lambda: 'Taking deliveries is off under Features' in (txt('.dash-mode') or ''),lambda: txt('.dash-mode'))
    check('the mode line says what waits to be sent to other servers: nothing',lambda: 'To send: 0; given up in 7 days: 0; servers unavailable: 0.' in (txt('.dash-mode') or ''),lambda: txt('.dash-mode'))

    # The inbox over HTTP (FWR 1): it takes a POST from another server past the write guard, and checks the
    # type and the signature itself; any other method is 405 with Allow: POST. A signed delivery cannot be
    # made here (this server reaches no other server for the key), so those cases are Go's (TestInboxRefusals,
    # TestDBTwoFlowaInbox).
    s,h,b=ask('inbox','POST',b'{}',{'Content-Type':'application/activity+json','Origin':'https://evil.example'})
    check('POST /inbox unsigned, from another site: 401 "not signed", not the write guard\'s 403',s==401 and b'not signed' in b,lambda: (s,b))
    s,h,b=ask('users/nobody/inbox','POST',b'{}',{'Content-Type':'application/json'})
    check('POST to a name nobody has, as plain JSON: 415, the same as any name',s==415,lambda: (s,b))
    s,h,b=ask('inbox')
    check('GET /inbox: 405 with Allow: POST',s==405 and h.get('Allow')=='POST',lambda: (s,dict(h)))

    # The Federation tab (FMD 1): an admin allows a domain with the keyboard; the policy is read back; the
    # numbers of the inbox show the refusals above.
    nav('#/admin/federation')
    check('the Federation tab: allowed servers only, no domain yet, and the deliveries refused counted',lambda: (lambda t: 'Allowed servers only' in t and 'No domain is allowed yet' in t and 'not signed' in t)(txt('#main') or ''),lambda: txt('#main')[:600])
    # The delivery queue (FWR 2): built and empty, since nothing in this version sends; its numbers come from the server.
    j=get_json(op,'admin/federation').get('deliveries') or {}
    check('the Federation tab says what waits to be sent: nothing queued, nothing sent yet, as the server says',lambda: (lambda t: 'Sending to other servers' in t and 'Follows and their answers in this version' in t and '0 queued, of at most 50,000' in t and '0 sent, 0 given up' in t)(txt('.fed-out') or '') and j.get('queued')==0 and j.get('queueMax')==50000,lambda: (txt('.fed-out'),j))
    if tab_to('#fed-domain-form [name=domain]','the domain field'):
        check('the field shows a ring of 2 px or more at 3:1 in both themes (keys.py)',not ring_ok(),ring_ok)
        type_text('Social.Example')
        key(TAB);key(TAB);type_text('friends of the house');key(ENTER)
        check('Enter saves it: the row is there, allowed, with the reason, and the server has it',lambda: js("const r=document.querySelector('tr[data-domain=\"social.example\"]');return !!r&&r.innerText.includes('Allowed')&&r.innerText.includes('friends of the house')") and any(d['domain']=='social.example' and d['policy']=='allow' for d in get_json(op,'admin/federation').get('domains',[])),lambda: txt('#main')[:600])
        check('the focus is back in the domain field for the next one',lambda: on('#fed-domain-form [name=domain]'),active)
    j=get_json(op,'admin/audit?action=domain_policy')
    check('the change is in the audit log',any(i.get('action')=='domain_policy' for i in j.get('items',[])),lambda: j)
    for t in ('dark','light'):
        themed(t)
        n,f,d=js(A['NAMES'],'#main'),js(A['FIELDS'],'#main'),js(A['IDS'])
        check(f'the Federation tab, {t}: every control and field has a name, and no id repeats (a11y.py)',n['n']>0 and f['n']>0 and not n['bad'] and not f['bad'] and not d['bad'],lambda: (n,f,d))
        r=js(C['CHECK'],'#main',False,C['KNOWN'],'')
        check(f'the Federation tab, {t}: text and the edges of controls stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
    themed('dark')
    js("scrollTo(0,0)");frames()
    picture('federated-admin-federation','Federated mode: the Federation tab, a domain allowed and the deliveries refused')
    # A group of radio buttons is one stop for Tab, on the chosen one; an arrow key chooses the next.
    if tab_to('[data-change=fed-policy][value=allowlist]','the chosen policy, "Allowed servers only"'):
        key(DOWN)
        check('the arrow key chooses "Every server not suspended", and the server has it',lambda: get_json(op,'admin/federation').get('policy')=='open')
        check('the focus stays on the choice made',lambda: on('[data-change=fed-policy][value=open]'),active)
    api(op,'admin/federation/policy',{'policy':'allowlist'},'PUT')
    api(op,'admin/federation/domains/social.example',None,'DELETE')

    # The composer offers the audience once the member shares; a Public post is marked, and is public.
    api(op,'me',{'federates':True},'PUT')
    load(BASE);nav('#/timeline')
    aud=lambda: js("const s=document.querySelector('[data-change=compose-aud]');return s&&[s.closest('label').firstChild.textContent.trim(),[...s.options].map(o=>o.text),s.value,s.getAttribute('aria-describedby'),(document.getElementById('aud-hint')||{}).textContent]")
    check('the composer offers "Who can read it": Public and This server, Public chosen, described by what it means',lambda: (lambda a: a and a[:4]==['Who can read it',['Public','This server'],'public','aud-hint'] and 'without signing in' in a[4])(aud()),aud)
    if tab_to('[data-change=compose-aud]','the audience'):
        check('the audience shows a ring of 2 px or more at 3:1 in both themes (keys.py)',not ring_ok(),ring_ok)
    js("const t=document.querySelector('#compose-form textarea');t.focus()");type_text('Public words from the federated suite')
    click('#compose-form button[type=submit]')
    check('the post is made, and marked Public for its author',lambda: js("const p=document.querySelector('.post');return p&&p.innerText.includes('Public words')&&(p.querySelector('.aud-mark')||{}).textContent")=='Public',lambda: js("return (document.querySelector('.post')||{}).innerText"))
    posts=get_json(op,'posts')['posts']
    pub=[p for p in posts if p['body']=='Public words from the federated suite']
    check('the server has it as public',len(pub)==1 and pub[0].get('audience')=='public',lambda: posts[:2])
    pid=pub[0]['id'] if pub else 0
    local=api(op,'posts',{'body':'Words for this server only','audience':'local'})['posts'][0]['id']
    load(BASE);nav('#/timeline')
    check('a This server post is marked as such',lambda: js("return [...document.querySelectorAll('.post')].map(p=>(p.querySelector('.aud-mark')||{}).textContent).slice(0,2)")==['This server','Public'])
    js("scrollTo(0,0)");frames()
    picture('federated-composer','Federated mode: the composer with Who can read it, and posts marked This server and Public')

    # What another server reads: the actor (once the key is made), the Note, and the 404 of a This server post.
    s,h,b=poll(lambda: (lambda r: r if r[0]==200 else None)(ask('users/admin',headers=AS)),30)[0] or ask('users/admin',headers=AS)
    actor=json.loads(b) if s==200 else {}
    check('the actor document: application/activity+json, Vary Accept, a Person with its key, indexable false',s==200 and h.get('Content-Type')=='application/activity+json' and h.get('Vary')=='Accept' and actor.get('id')==ORIGIN+'/users/admin' and actor.get('type')=='Person' and actor.get('publicKey',{}).get('id')==ORIGIN+'/users/admin#main-key' and actor.get('indexable') is False,lambda: (s,dict(h),b[:300]))
    s,h,b=ask(f'users/admin/posts/{pid}',headers=AS)
    note=json.loads(b) if s==200 else {}
    check('the Note: addressed to the public, its text in content',s==200 and note.get('type')=='Note' and note.get('to')==['https://www.w3.org/ns/activitystreams#Public'] and 'Public words from the federated suite' in note.get('content',''),lambda: (s,b[:300]))
    nf=ask('users/nobody',headers=AS)
    s,h,b=ask(f'users/admin/posts/{local}',headers=AS)
    check('a This server post is the 404 of an address nobody has',(s,b)==(nf[0],nf[2]) and s==404,lambda: (s,b))
    s,h,b=ask(f'users/admin/posts/{local}')
    nf=ask('users/nobody')
    check('and so is its page',(s,b)==(nf[0],nf[2]) and s==404,lambda: (s,b[:200]))

    # The outbox, the Create at its own id and the collections (docs/federation.md 6.2, 6.4, F10).
    check('the actor names the outbox, followers, following and featured',all(actor.get(k)==ORIGIN+'/users/admin/'+k for k in ('outbox','followers','following','featured')),lambda: actor)
    s,h,b=ask('users/admin/outbox',headers=AS)
    box=json.loads(b) if s==200 else {}
    check('the outbox: an OrderedCollection with its count and its first page',s==200 and h.get('Vary')=='Accept' and box.get('type')=='OrderedCollection' and box.get('first')==ORIGIN+'/users/admin/outbox?page=true' and box.get('totalItems',0)>=1,lambda: (s,b[:300]))
    s,h,b=ask('users/admin/outbox?page=true',headers=AS)
    page=json.loads(b) if s==200 else {}
    items=page.get('orderedItems',[])
    objs=[i.get('object',{}).get('id') for i in items]
    check('its first page: the Create of the Public post, newest first, not the This server post, as many as counted',s==200 and page.get('type')=='OrderedCollectionPage' and page.get('partOf')==ORIGIN+'/users/admin/outbox' and objs[:1]==[f'{ORIGIN}/users/admin/posts/{pid}'] and f'{ORIGIN}/users/admin/posts/{local}' not in objs and len(items)==box.get('totalItems') and 'next' not in page,lambda: (s,b[:400]))
    s,h,b=ask(f'users/admin/posts/{pid}/activity',headers=AS)
    cr=json.loads(b) if s==200 else {}
    cr.pop('@context',None)
    check('the Create at its own id is the one in the outbox, to the public and cc the followers',s==200 and items[:1]==[cr] and cr.get('to')==['https://www.w3.org/ns/activitystreams#Public'] and cr.get('cc')==[ORIGIN+'/users/admin/followers'],lambda: (s,b[:300]))
    for k in ('followers','following'):
        s,h,b=ask('users/admin/'+k,headers=AS)
        d=json.loads(b) if s==200 else {}
        check(f'{k}: a collection that names nobody, no page and no items (the follows flag is off, so no count either)',s==200 and d.get('type')=='OrderedCollection' and set(d)<={'@context','id','type','totalItems'},lambda: (s,b))
    s,h,b=ask('users/admin/featured',headers=AS)
    check('featured: empty',s==200 and json.loads(b).get('totalItems')==0 and json.loads(b).get('orderedItems')==[],lambda: (s,b))
    class Stay(urllib.request.HTTPRedirectHandler):
        def redirect_request(self,*a): return None
    try:
        urllib.request.build_opener(Stay).open(urllib.request.Request(BASE+'users/admin/outbox',headers={'Accept':'text/html,*/*;q=0.8'}),timeout=20);rs=(200,'')
    except urllib.error.HTTPError as e: rs=(e.code,e.headers.get('Location'))
    check('a browser at the outbox is sent to the profile page',rs==(303,'/users/admin'),lambda: rs)
    s,h,b=ask('users/admin',headers={'Accept':'text/html,*/*;q=0.8'})
    check('the profile page over HTTP: HTML with a policy that allows no script, the Public post and not the other',s==200 and h.get('Content-Type','').startswith('text/html') and 'script-src' not in h.get('Content-Security-Policy','') and h.get('Content-Security-Policy','').startswith("default-src 'none'") and b'Public words from the federated suite' in b and b'Words for this server only' not in b,lambda: (s,dict(h)))

    # The two pages in the browser, held to the rules every page of the app is held to.
    def public_page(path,what):
        cmd('WebDriver:Navigate',url=BASE+path);wait_for("return !!document.querySelector('#pub-name')",what,20)
        check(f'{what}: no script at all, one h1, a main landmark, lang',js("return [document.scripts.length+document.querySelectorAll('[onclick],[onload],[onerror]').length,document.querySelectorAll('h1').length,document.querySelectorAll('main').length,document.documentElement.lang]")==[0,1,1,'en'])
        for t in ('dark','light'):
            themed(t)
            n,p,d=js(A['NAMES']),js(A['PICTURES']),js(A['IDS'])
            check(f'{what}, {t}: every link has a name, every picture a text alternative, ids are unique (a11y.py)',n['n']>0 and not n['bad'] and not p['bad'] and not d['bad'],lambda: (n,p,d))
            r=js(C['CHECK'],None,False,C['KNOWN'],'')
            check(f'{what}, {t}: text and the edges of controls stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
        themed('dark')
        if tab_to('a[href="/index.html#/signin"]','Sign in'):
            check(f'{what}: Sign in takes the focus with a ring of 2 px or more at 3:1 in both themes (keys.py)',not ring_ok(),ring_ok)
        js("document.activeElement&&document.activeElement.blur();scrollTo(0,0)");frames()
    public_page(f'users/admin/posts/{pid}','the public post page')
    check('the post page: the text, the author and a way into the app',lambda: (lambda t: 'Public words from the federated suite' in t and '@admin@flowa.test' in t and 'Open in Flowa' in t)(js("return document.body.innerText")))
    picture('federated-post','Federated mode: a Public post as people on other servers see it')
    public_page('users/admin','the public profile page')
    check('the profile page: Public posts, and not the This server one',lambda: (lambda t: 'Public posts' in t and 'Public words from the federated suite' in t and 'Words for this server only' not in t)(js("return document.body.innerText")))
    picture('federated-profile','Federated mode: a member\'s public profile page')
    # 420 px wide, in both themes: nothing reaches past the edge.
    for path,name,title in ((f'users/admin/posts/{pid}','federated-post-phone','Federated mode: a Public post, phone'),('users/admin','federated-profile-phone','Federated mode: a public profile page, phone')):
        cmd('WebDriver:Navigate',url=BASE+path);wait_for("return !!document.querySelector('#pub-name')",name,20)
        got=phone(420)
        if got!=420: got=phone(420)
        for t in ('dark','light'):
            themed(t)
            bad=js(WIDE_PUB)
            check(f'{name} at {got} px wide, {t}: nothing is off the screen',got<=420 and not bad,lambda: (got,bad[:5]))
        themed('dark');js("scrollTo(0,0)");frames()
        picture(name,title)
    cmd('WebDriver:SetWindowRect',width=1440,height=1100)
    cmd('Marionette:SetContext',value='chrome')
    try: js("return FullZoom.reset(gBrowser.selectedBrowser)")
    finally: cmd('Marionette:SetContext',value='content')
    H.zoom=1.0
    load(BASE)

    # People elsewhere (FRD 8): the search by handle. This suite's server refuses private addresses and has no
    # other server to ask (no setting lets a running Flowa reach a private address, by design), so it covers
    # what needs no remote server: a handle on this instance (answered from the database, no connection), and
    # queries that fail before or at their first connection, each in its fixed words. The whole remote path
    # (WebFinger, the actor, the reverse check, redirects, the outbox) is TestDBResolveHandle's, in Go.
    def lookup(opener,q,headers=None):
        r=urllib.request.Request(BASE+'api/fed/lookup',data=json.dumps({'q':q}).encode(),headers={'Content-Type':'application/json',**(headers or {})},method='POST')
        try:
            with opener.open(r,timeout=30) as a: return a.status,json.loads(a.read())
        except urllib.error.HTTPError as e: return e.code,json.loads(e.read() or b'{}')
    s,j=lookup(op,'@Admin@FLOWA.test')
    check('a handle on this instance is a member of it, answered from the database',s==200 and j.get('result',{}).get('kind')=='local' and j['result'].get('handle')=='admin',lambda: (s,j))
    # The allowlist a fresh instance has holds outbound too (F8, section 10): a server nobody allowed is not asked.
    s,j=lookup(op,'bo@nowhere.invalid')
    check('with the allowlist of a fresh instance, a server nobody allowed is not asked, in fixed words',s==403 and j.get('reason')=='notAllowed' and 'allow' in j.get('error',''),lambda: (s,j))
    api(op,'admin/federation/domains/nowhere.invalid',{'policy':'allow'},'PUT')  # allowed, the lookup goes on to its resolver
    for q,code,reason in (('@nobody@flowa.test',404,'notFound'),('https://flowa.test/actor',404,'notFound'),('bo',400,'badInput'),('http://social.example/@bo',400,'badInput'),
                          ('bo@10.0.0.1',422,'refused'),('bo@localhost',422,'refused'),('https://social.example:8443/@bo',422,'refused'),('bo@nowhere.invalid',404,'notFound')):
        s,j=lookup(op,q)
        check(f'{q!r}: {code} {reason}, in fixed words',s==code and j.get('reason')==reason and j.get('error') and '10.0.0.1' not in j.get('error',''),lambda: (s,j))
    api(op,'admin/federation/domains/nowhere.invalid',None,'DELETE')
    s,j=lookup(urllib.request.build_opener(),'@admin@flowa.test')
    check('a visitor cannot look anyone up',s==401,lambda: (s,j))
    s,j=lookup(op,'@admin@flowa.test',{'Origin':'https://evil.example'})
    check('nor can another page, by a form or a script of its own',s==403,lambda: (s,j))
    # A GET (a link or a picture could send one) is not the route: the address takes POST alone, so a GET is
    # answered as an API address nobody has, the same status, Allow and body.
    s,gh,gb=ask('api/fed/lookup?q=%40admin%40flowa.test');s0,gh0,gb0=ask('api/nobody-has-this-address')
    check('a GET looks nobody up: answered as an API address nobody has',s==s0==404 and gh.get('Allow')==gh0.get('Allow') and gb==gb0,lambda: (s,gh.get('Allow'),gb[:100],s0,gb0[:100]))
    check('/api/me offers the search in a mode that federates',get_json(op,'me').get('federation',{}).get('lookup') is True,lambda: get_json(op,'me').get('federation'))
    nav('#/timeline')
    check('the timeline has "People elsewhere", with a box named for what it takes',lambda: js("const c=document.querySelector('.look-card');const i=document.querySelector('#look-q');return !!c&&c.querySelector('h2').textContent==='People elsewhere'&&!!i&&i.labels[0].innerText.startsWith('Handle or profile address')"),lambda: txt('main')[:300])
    def look_rules(who):
        for t in ('dark','light'):
            themed(t)
            n,f,d=js(A['NAMES'],'.look-card'),js(A['FIELDS'],'.look-card'),js(A['IDS'])
            check(f'{who}, {t}: every control and field has a name, and no id repeats (a11y.py)',n['n']>0 and f['n']>0 and not n['bad'] and not f['bad'] and not d['bad'],lambda: (n,f,d))
            r=js(C['CHECK'],'.look-card',False,C['KNOWN'],'')
            check(f'{who}, {t}: text and the edges of controls stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
        themed('dark')
    if tab_to('#look-q','the box of People elsewhere'):
        check('the box shows a ring of 2 px or more at 3:1 in both themes (keys.py)',not ring_ok(),ring_ok)
        type_text('@admin@flowa.test');key(ENTER)
        check('Enter looks the handle up: a member of this Flowa, linked to their profile',lambda: js("const a=document.querySelector('.look-out a.person');return !!a&&a.getAttribute('href')==='#/u/admin'&&a.innerText.includes('a member of this Flowa')"),lambda: txt('#look-out'))
        check('a screen reader is told what was found',lambda: 'a member of this Flowa' in (txt('#say') or ''),lambda: txt('#say'))
        check('the focus stays in the search',lambda: on('#look-q') or on('.look-form button'),active)
        look_rules('People elsewhere, a local member')
        js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top
        picture('federated-lookup','Federated mode: People elsewhere, a handle on this instance found')
        tab_to('#look-q','the box again',back=True)
        key('a',hold=(CTRL,));type_text('bo@10.0.0.1');key(ENTER)
        check('an address Flowa does not connect to is told so, in plain words',lambda: txt('.look-err')=='Flowa does not connect to that address.',lambda: txt('#look-out'))
        check('and a screen reader hears the same',lambda: txt('#say')=='Flowa does not connect to that address.',lambda: txt('#say'))
        look_rules('People elsewhere, a failure')
        js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top
        picture('federated-lookup-failure','Federated mode: People elsewhere, an address Flowa does not connect to')
        got=phone(420)
        if got!=420: got=phone(420)
        for t in ('dark','light'):
            themed(t)
            bad=js(WIDE)
            check(f'People elsewhere at {got} px wide, {t}: nothing is off the screen',got<=420 and not bad,lambda: (got,bad[:5]))
        themed('dark');js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top
        picture('federated-lookup-failure-phone','Federated mode: People elsewhere, a failure, phone')
        # The page's drawing of a remote profile, given an answer as hostile as another server's words can make
        # one (the server sends them as plain text; the page must keep them so): no element made of them, the
        # text shown as written, no link that is not https. No other server is asked: the page's own fetch of
        # this one address is answered by the suite for one lookup, and given back after.
        js("""const real=window.fetch;window.fetch=(u,o)=>{if(!String(u).endsWith('api/fed/lookup'))return real(u,o);window.fetch=real;
            return Promise.resolve(new Response(JSON.stringify({result:{kind:'remote',handle:'x\\u202eelpmaxe.doog@other.example',name:'<img src=x id=pwn1>\\u202e',bio:'<b id=pwn2>bio</b>',link:'https://other.example/@x',notice:'<i id=pwn3>n</i>',
            posts:[{text:'<script id=pwn4>x</script> '+'averyveryverylongwordwithnospace'.repeat(8),summary:'<u id=pwn5>cw</u>',link:'javascript:alert(2)',more:true,published:'2026-11-12T18:04:05Z',pictures:2}]}}),{status:200,headers:{'Content-Type':'application/json'}}))}""")
        tab_to('#look-q','the box again',back=True)
        key('a',hold=(CTRL,));type_text('x@remote.example');key(ENTER)
        made_of=lambda: js("""const o=document.querySelector('#look-out');return [!!o.querySelector('[id^=pwn]'),[...o.querySelectorAll('a')].map(a=>a.getAttribute('href')),o.innerText.includes('<img src=x id=pwn1>')&&o.innerText.includes('<b id=pwn2>bio</b>'),o.innerText.includes('2 pictures, not shown here')]""")
        check('a remote profile is drawn as text: no element of its words, no link that is not https, its pictures counted, not shown',lambda: made_of()==[False,['https://other.example/@x'],True,True],made_of)
        # A handle whose name turns the text's direction (the server refuses one; the page must not trust that):
        # the host stands in a place of its own, exactly the host, after an isolated name, and every link shows
        # the host it leads to.
        parts=lambda: js("""const o=document.querySelector('#look-out'),u=o.querySelector('.look-user'),h=o.querySelector('.look-host'),n=o.querySelector('.look-name b bdi');
            return [!!u&&u.tagName==='BDI',h&&h.textContent,!!n,getComputedStyle(o.querySelector('.look-handle')).unicodeBidi,o.innerText.includes('(other.example)')]""")
        check('the handle\'s host is shown apart, exactly as proven, after an isolated name; each link shows its host',lambda: parts()==[True,'other.example',True,'isolate',True],parts)
        bad=js(WIDE)
        check('a remote profile with a long word at 420 px: nothing is off the screen',not bad,lambda: bad[:5])
        js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top
        picture('federated-lookup-hostile-phone','Federated mode: People elsewhere drawing an answer the suite supplies with a name that turns the text\'s direction (no server asked), phone')
        tab_to('#look-q','the box again',back=True)
        key('a',hold=(CTRL,));type_text('@admin@flowa.test');key(ENTER)
        check('at 420 px the member found is shown too',lambda: js("return !!document.querySelector('.look-out a.person')"),lambda: txt('#look-out'))
        js("scrollTo(0,0)");frames()  # a picture of the whole page starts at its top
        picture('federated-lookup-phone','Federated mode: People elsewhere, a handle on this instance found, phone')
        cmd('WebDriver:SetWindowRect',width=1440,height=1100)
        cmd('Marionette:SetContext',value='chrome')
        try: js("return FullZoom.reset(gBrowser.selectedBrowser)")
        finally: cmd('Marionette:SetContext',value='content')
        H.zoom=1.0
        load(BASE)

    # Follows across servers (FWR 3). The switch for approving followers by hand is real: reached with Tab, turned
    # with Space, read back through the API. A remote account needs another server, which this one cannot reach, so
    # the server's side (the Follow out and in, the Accept, the requests) is Go's (TestDBFollowsAcross,
    # TestDBTwoFlowaFollow); here the page's own fetch of the few addresses of one remote account is answered by
    # the suite, as the hostile lookup above is, to check how the page draws the profile, the Follow button's three
    # states and a follow request with Approve and Decline. Over HTTP, an account nobody has is the one 404.
    api(op,'me',{'federates':True},'PUT')
    load(BASE);nav('#/settings')
    check('with Follows off (the default), Settings offers no switch for approving followers by hand',lambda: js("return !!document.querySelector('[data-change=federates]')&&!document.querySelector('[data-change=locked]')"),lambda: txt('.fed-card'))
    api(op,'flags',{'flags':{'follows':True}},'PUT')
    s,_,b=ask('api/fed/accounts/999999')
    check('a remote account nobody has: the API wants a session first',s==401,lambda: (s,b[:100]))
    try: op.open(BASE+'api/fed/accounts/999999',timeout=20);fa=200
    except urllib.error.HTTPError as x: fa=x.code
    check('and for a member, an account nobody has is the 404',fa==404,lambda: fa)
    check('the requests list is there, and empty',get_json(op,'fed/requests')=={'requests':[],'total':0},lambda: get_json(op,'fed/requests'))
    load(BASE);nav('#/settings')
    check('Settings offers approving followers from other servers by hand, off, once the member shares their profile',lambda: js("const b=document.querySelector('[data-change=locked]');return !!b&&!b.checked&&b.closest('label').innerText.trim()"),lambda: txt('.fed-card'))
    check('the card says what this version does: people elsewhere can follow, posts are not sent',lambda: (lambda t: 'People there can follow you, and you can follow them' in t and 'your posts are not sent to them yet' in t)(txt('.fed-card') or ''),lambda: txt('.fed-card'))
    if tab_to('[data-change=locked]','the switch for approving by hand'):
        check('the switch shows a ring of 2 px or more at 3:1 in both themes (keys.py)',not ring_ok(),ring_ok)
        key(SPACE)
        check('Space turns it on, and the server has it',lambda: get_json(op,'me').get('federation',{}).get('locked') is True,lambda: get_json(op,'me').get('federation'))
        check('the focus stays on it, now on',lambda: on('[data-change=locked]') and js("return document.querySelector('[data-change=locked]').checked"),active)
        s,_,b=ask('users/admin',headers=AS)
        check('the actor now says manuallyApprovesFollowers',s==200 and json.loads(b).get('manuallyApprovesFollowers') is True,lambda: (s,b[:200]))
        key(SPACE)
        check('Space again turns it off',lambda: get_json(op,'me').get('federation',{}).get('locked') is False)
    card_rules('the card, with the switch for approving by hand')
    STUB="""window.__calls=[];window.__fed={follow:arguments[0],items:[{id:901,kind:'follow_request',actorId:7,postId:0,commentId:0,rootId:0,text:'',read:false,created:new Date().toISOString()}]};
      const bo={id:7,username:'bo@social.example',displayName:'Bo \u202eexe',role:'user',avatar:'',remote:{id:7,handle:'bo@social.example',host:'social.example'}};
      const json=(d,st=200)=>Promise.resolve(new Response(JSON.stringify(d),{status:st,headers:{'Content-Type':'application/json'}}));
      window.__realFetch=window.__realFetch||window.fetch;const real=window.__realFetch;
      window.fetch=(u,o)=>{const m=(o&&o.method)||'GET',p=String(u).replace(/^.*?api[/]/,'');
        if(p==='fed/accounts/7')return json({account:{id:7,name:'Bo \u202eexe',handle:'bo@social.example',host:'social.example',bio:'Poi in the park. <b id=pwn6>x</b>',link:'https://social.example/@bo',locked:true,state:'ok'},view:{follow:window.__fed.follow,asks:!!window.__fed.items.length},notice:'Their posts do not show in Flowa yet, in Home or here: only their profile does. Open the profile on their server to read their posts. Pictures from other servers are not shown yet.'});
        if(p==='fed/accounts/7/follow'){window.__calls.push(m+' '+p);window.__fed.follow=m==='PUT'?'requested':'none';return json({view:{follow:window.__fed.follow},changed:true,sent:true})}
        if(p==='fed/requests/7'){window.__calls.push(m+' '+p);window.__fed.items=[];return json({approved:m==='PUT',sent:true})}
        if(p==='fed/requests')return json({requests:window.__fed.items.length?[{user:bo,since:new Date().toISOString()}]:[],total:window.__fed.items.length});
        if(p==='notifications')return json({items:window.__fed.items,users:{7:bo},unread:window.__fed.items.length});
        return real(u,o)};"""
    js(STUB,'none')
    nav('#/fed/7')
    prof=lambda: js("""const m=document.querySelector('#main');return [m.querySelector('h1 bdi')&&m.querySelector('h1 bdi').textContent,(m.querySelector('.look-host')||{}).textContent,!!m.querySelector('[id^=pwn]'),
      (m.querySelector('[data-act=fed-follow]')||{}).innerText,m.innerText.includes('Their posts do not show in Flowa yet'),m.innerText.includes('approves its followers by hand')]""")
    check('a remote profile: its name isolated, its host apart, its words as text, Follow, and where its posts would be, why they are not',lambda: prof()==['Bo \u202eexe','social.example',False,'Follow',True,True],prof)
    check('the request it made waits on its profile with Approve and Decline, each named for whom',lambda: js("return [...document.querySelectorAll('.fed-asks button')].map(b=>b.getAttribute('aria-label'))")==['Approve bo@social.example','Decline bo@social.example'])
    for t in ('dark','light'):
        themed(t)
        n,f,d=js(A['NAMES'],'#main'),js(A['FIELDS'],'#main'),js(A['IDS'])
        check(f'the remote profile, {t}: every control has a name, and no id repeats (a11y.py)',n['n']>0 and not n['bad'] and not d['bad'],lambda: (n,d))
        r=js(C['CHECK'],'#main',False,C['KNOWN'],'')
        check(f'the remote profile, {t}: text and the edges of controls stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
    themed('dark')
    if tab_to('[data-act=fed-follow]','Follow'):
        check('Follow shows a ring of 2 px or more at 3:1 in both themes (keys.py)',not ring_ok(),ring_ok)
        key(ENTER)
        check('Enter sends the follow; the button says Requested until their server accepts, and takes the focus',lambda: js("const b=document.querySelector('[data-act=fed-unfollow]');return !!b&&b.innerText.trim()==='Requested'&&b.getAttribute('aria-label').includes('Take the follow request back')&&document.activeElement===b") and 'PUT fed/accounts/7/follow' in js('return window.__calls'),lambda: (js('return window.__calls'),txt('#main')[:300]))
    load(BASE);js(STUB,'following');nav('#/fed/7')  # their server accepted: the profile read again
    check('once their server accepted: Unfollow',lambda: js("const b=document.querySelector('[data-act=fed-unfollow]');return b&&b.innerText.trim()")=='Unfollow')
    js("scrollTo(0,0)");frames()
    picture('federated-remote-profile','Federated mode: a remote account\'s profile drawn from an answer the suite supplies (no server asked): followed, its request waiting')
    click('[data-act=bell]')
    who=lambda: js("const r=document.querySelector('.ntf-panel .ntf');return r&&[(r.querySelector('bdi')||{}).textContent,(r.querySelector('.look-host')||{}).textContent,r.innerText.includes('@bo@social.example')]")
    check('the bell names an account of another server by its handle with its host, its name isolated (never a bare name beside members\' names)',lambda: who()==['Bo \u202eexe','social.example',True],who)
    check('the bell lists the follow request with Approve and Decline beside it, not inside a link',lambda: js("const r=document.querySelector('.ntf-panel .ntf');return !!r&&r.tagName==='DIV'&&r.innerText.includes('asked to follow you')&&r.querySelectorAll('button[data-act=req-approve],button[data-act=req-decline]').length===2"),lambda: txt('.ntf-panel'))
    js("scrollTo(0,0)");frames()
    picture('federated-follow-request','Federated mode: a follow request from another server in the notifications, with Approve and Decline (the answer supplied by the suite)')
    click('.ntf-panel [data-act=req-approve]')
    check('Approve answers the request, and it leaves the list',lambda: 'PUT fed/requests/7' in js('return window.__calls') and not js("return document.querySelector('[data-act=req-approve]')"),lambda: js('return window.__calls'))
    load(BASE)
    api(op,'me',{'federates':False},'PUT');api(op,'flags',{'flags':{'follows':False}},'PUT')
    load(BASE)

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
        json.dump(kept+made,open(idx,'w'));check(f'the pictures are in {idx}',len(made)==16)
    else: print('SKIP the pictures in index.json: no directory for pictures was given',flush=True)

    # The start said once what this version does.
    try: log=open('/tmp/flowa-test/dev.log').read()
    except OSError: log=None
    if log is None: print('SKIP the start log: /tmp/flowa-test/dev.log cannot be read here',flush=True)
    else: check('the start log says once what this version serves',log.count('this version answers discovery (WebFinger and NodeInfo) and serves the profiles and Public posts')==1,lambda: [l for l in log.splitlines() if 'federation' in l][:5])
except Exception as x: check('the federated suite ran',False,repr(x)[:300])
finally: done()
raise SystemExit(1 if errs else 0)
