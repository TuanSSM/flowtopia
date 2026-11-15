# A federating instance: run with the server started with FLOWA_MODE=federated, FLOWA_ORIGIN=https://flowa.test
# and a test-only FLOWA_KEY_SECRET (run.sh passes the settings on, and the workflow gives them to this suite).
# This version federates as far as reading (docs/federation.md 5.3 to 5.6, 6.2, 6.5): the suite asks discovery, the
# actor documents and the public pages over HTTP as another server would, works the member's switch in Settings
# and the audience in the composer with the keyboard, and opens the public pages in the browser:
#   - NodeInfo: the discovery document and the 2.1 document, their content types, the counts and the mode;
#   - WebFinger: 404 for a member who has not opted in, 200 with the self link once they have, 404 again after,
#     the CORS header on every answer, HEAD with no body, 405 with Allow for any other method;
#   - Settings: "Beyond this server" with the switch, reached with Tab and turned with Space, saved (read back
#     through the API), the focus kept on it; the warning for a name that ends with a dot; "Remove me from other
#     servers" (FWR 7) with its warning in view, asked again before anything is sent, declined and then confirmed,
#     the card saying so after, and the actor's 410;
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
#     API and the audit log; one domain of each kind (limit with a public comment and reject reports, suspend,
#     allow with reject media) shown with its switches, comment and note; a row changed by keyboard; the search;
#     the list published on the about page without the notes; the sentence on authorized fetch; the mode switch
#     asking first (declined, then accepted) with the arrow keys; the inbox's numbers showing the refusals, held to
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
    check('the admin is told the handle, and has not opted in',{k:(me.get('federation') or {}).get(k) for k in ('handle','optedIn','label')}=={'handle':'@admin@'+HOST,'optedIn':False,'label':LABEL} and 'your Public posts are sent to them' in (me.get('federation') or {}).get('built',''),lambda: me.get('federation'))
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
    check('the card says what this version does, in the server\'s words: read, followed, Public posts sent',lambda: (lambda t: 'read your profile and the posts you mark Public' in t and 'People there can follow you' in t and 'your Public posts are sent to them' in t)(js("return document.querySelector('.fed-card').innerText")),lambda: js("return document.querySelector('.fed-card').innerText"))
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
    check('the dashboard: federated mode, the origin, announced since the first answer, and what is served',lambda: (lambda t: bool(t) and t.startswith('Instance mode federated') and 'Federating as https://flowa.test, first announced' in t and 'serves the profiles and Public posts' in t and 'members can follow people on other servers' in t and 'sends the Public posts of members who share their profile' in t)(txt('.dash-mode')),lambda: (location_of(),txt('#main')[:300]))
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
    check('the Federation tab says what waits to be sent: nothing queued, nothing sent yet, as the server says',lambda: (lambda t: 'Sending to other servers' in t and 'Follows and their answers, Public posts, comments and likes, their edits and deletions, profile changes' in t and '0 queued, of at most 50,000' in t and '0 sent, 0 given up' in t)(txt('.fed-out') or '') and j.get('queued')==0 and j.get('queueMax')==50000,lambda: (txt('.fed-out'),j))
    if tab_to('#fed-domain-form [name=domain]','the domain field'):
        check('the field shows a ring of 2 px or more at 3:1 in both themes (keys.py)',not ring_ok(),ring_ok)
        type_text('Social.Example')
        # Past the policy, the two switches and the public comment to the note for staff.
        for _ in range(5): key(TAB)
        check('five Tabs on, the note for staff has the focus',lambda: on('#fed-domain-form [name=note]'),active)
        type_text('friends of the house');key(ENTER)
        check('Enter saves it: the row is there, allowed, with the reason, and the server has it',lambda: js("const r=document.querySelector('tr[data-domain=\"social.example\"]');return !!r&&r.innerText.includes('Allowed')&&r.innerText.includes('friends of the house')") and any(d['domain']=='social.example' and d['policy']=='allow' for d in get_json(op,'admin/federation').get('domains',[])),lambda: txt('#main')[:600])
        check('the focus is back in the domain field for the next one',lambda: on('#fed-domain-form [name=domain]'),active)
    j=get_json(op,'admin/audit?action=domain_policy')
    check('the change is in the audit log',any(i.get('action')=='domain_policy' for i in j.get('items',[])),lambda: j)
    # One domain of each kind (FMD 1): a limit with a public comment, a suspension, an allow that rejects media.
    api(op,'admin/federation/domains/loud.example',{'policy':'limit','comment':'Too many unsolicited replies.','note':'reports from three members','rejectReports':True},'PUT')
    api(op,'admin/federation/domains/spam.example',{'policy':'suspend','comment':'Spam.','note':'spam waves'},'PUT')
    api(op,'admin/federation/domains/pics.example',{'policy':'allow','rejectMedia':True,'note':'pictures we cannot moderate'},'PUT')
    load(BASE);nav('#/admin/federation')
    check('the list shows each kind with its switches, its public comment and its note',lambda: js("""const t=s=>document.querySelector(`tr[data-domain="${s}"]`)?.innerText||'';
      return t('loud.example').includes('Limited')&&t('loud.example').includes('Reports rejected')&&t('loud.example').includes('Too many unsolicited replies.')&&t('loud.example').includes('reports from three members')
        &&t('spam.example').includes('Suspended')&&t('pics.example').includes('Media rejected')&&t('social.example').includes('Allowed')"""),lambda: txt('#main')[:900])
    check('the page says the allowlist alone does not stop public reading, and that authorized fetch is not built yet',lambda: (lambda t: 'The allowlist alone does not stop a server that is not listed from reading' in t and 'Authorized fetch' in t and 'is not built yet' in t)(txt('.fed-fetch') or ''),lambda: txt('.fed-fetch'))
    check('reject reports says it does nothing yet',lambda: 'it does nothing yet' in (txt('#fed-domain-form') or ''),lambda: txt('#fed-domain-form'))
    # Change, by keyboard: the form takes the row's values, with the domain fixed, and saves the change.
    js("document.querySelector('[data-act=fed-domain-edit][data-domain=\"loud.example\"]').focus()");key(ENTER)
    check('Change fills the form with the row and the keyboard is on its policy',lambda: js("const f=document.getElementById('fed-domain-form');return f.domain.value==='loud.example'&&f.domain.readOnly&&f.policy.value==='limit'&&f.rejectReports.checked&&!f.rejectMedia.checked&&f.comment.value==='Too many unsolicited replies.'") and on('#fed-domain-form [name=policy]'),lambda: (txt('#fed-domain-form'),active()))
    js("const f=document.getElementById('fed-domain-form');f.rejectMedia.checked=true;f.comment.focus()");key(ENTER)
    check('saving the change: the server has reject media on loud.example, its limit and comment kept',lambda: any(d['domain']=='loud.example' and d['policy']=='limit' and d['rejectMedia'] and d['comment']=='Too many unsolicited replies.' for d in get_json(op,'admin/federation').get('domains',[])))
    # The search: part of a name and a policy narrow the list, and Show all gives it back.
    js("const f=document.getElementById('fed-search');f.q.value='example';f.kind.value='suspend';f.q.focus()");key(ENTER)
    check('the search narrows the list to the suspended domain',lambda: js("return [...document.querySelectorAll('tr[data-domain]')].map(r=>r.dataset.domain).join()")=='spam.example',lambda: txt('#main')[:600])
    click('[data-act=fed-search-clear]')
    check('Show all lists the four domains again',lambda: js("return document.querySelectorAll('tr[data-domain]').length")==4)
    # Publishing the list on the about page: off until an admin ticks it; then the about page lists the limited and
    # suspended domains with their public comments, never the note.
    js("document.querySelector('[data-change=fed-publish]').focus()");key(SPACE)
    check('ticking publish: the server has it',lambda: get_json(op,'admin/federation').get('publish') is True)
    nav('#/about')
    check('the about page lists the limited and suspended domains with their comments, no allowed one and no note',lambda: (lambda t: 'Moderated servers' in t and 'loud.example' in t and 'Too many unsolicited replies.' in t and 'spam.example' in t and 'pics.example' not in t and 'social.example' not in t and 'reports from three members' not in t)(txt('#main') or ''),lambda: txt('#main')[:600])
    nav('#/admin/federation')
    for t in ('dark','light'):
        themed(t)
        n,f,d=js(A['NAMES'],'#main'),js(A['FIELDS'],'#main'),js(A['IDS'])
        check(f'the Federation tab, {t}: every control and field has a name, and no id repeats (a11y.py)',n['n']>0 and f['n']>0 and not n['bad'] and not f['bad'] and not d['bad'],lambda: (n,f,d))
        r=js(C['CHECK'],'#main',False,C['KNOWN'],'')
        check(f'the Federation tab, {t}: text and the edges of controls stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
    themed('dark')
    js("scrollTo(0,0)");frames()
    picture('federated-admin-federation','Federated mode: the Federation tab, one domain of each kind, the list published and the deliveries refused')
    # A group of radio buttons is one stop for Tab, on the chosen one; an arrow key chooses the next, which asks first
    # what it means: declined, nothing changes and the choice held is checked again; accepted, the server has it.
    if tab_to('[data-change=fed-policy][value=allowlist]','the chosen policy, "Allowed servers only"'):
        js("window.__confirm=window.confirm;window.__asked=[];window.confirm=m=>{window.__asked.push(m);return false}")
        key(DOWN)
        check('the arrow key asks first, saying what "Every server not suspended" means; declined, nothing changes',lambda: js("return window.__asked.length===1&&window.__asked[0].includes('any server except the domains suspended')&&document.querySelector('[data-change=fed-policy][value=allowlist]').checked") and get_json(op,'admin/federation').get('policy')=='allowlist',lambda: js('return window.__asked'))
        js("window.confirm=m=>{window.__asked.push(m);return true}")
        key(DOWN)
        check('accepted, the server has it',lambda: get_json(op,'admin/federation').get('policy')=='open')
        check('the focus stays on the choice made',lambda: on('[data-change=fed-policy][value=open]'),active)
        js("window.confirm=window.__confirm")
    api(op,'admin/federation/policy',{'policy':'allowlist'},'PUT')
    api(op,'admin/federation/publish',{'publish':False},'PUT')
    for d in ('social.example','loud.example','spam.example','pics.example'): api(op,'admin/federation/domains/'+d,None,'DELETE')

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
    check('the card says what this version does: people elsewhere can follow, and get the Public posts',lambda: (lambda t: 'People there can follow you, and you can follow them' in t and 'your Public posts are sent to them' in t)(txt('.fed-card') or ''),lambda: txt('.fed-card'))
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
        if(p==='fed/accounts/7')return json({account:{id:7,name:'Bo \u202eexe',handle:'bo@social.example',host:'social.example',bio:'Poi in the park. <b id=pwn6>x</b>',link:'https://social.example/@bo',locked:true,state:'ok'},view:{follow:window.__fed.follow,asks:!!window.__fed.items.length},notice:'Once you follow them, their new posts show in your Home. Their older posts and their replies stay on their server: open the profile there to read them.'});
        if(p==='fed/accounts/7/follow'){window.__calls.push(m+' '+p);window.__fed.follow=m==='PUT'?'requested':'none';return json({view:{follow:window.__fed.follow},changed:true,sent:true})}
        if(p==='fed/requests/7'){window.__calls.push(m+' '+p);window.__fed.items=[];return json({approved:m==='PUT',sent:true})}
        if(p==='fed/requests')return json({requests:window.__fed.items.length?[{user:bo,since:new Date().toISOString()}]:[],total:window.__fed.items.length});
        const long={id:8,username:'+0123456789ab@long.example',displayName:'',role:'user',avatar:'',remote:{id:8,handle:'x'.repeat(200)+'@long.example',host:'long.example'}};
        if(p==='notifications')return json({items:window.__fed.items,users:{7:bo,8:long},unread:window.__fed.items.length});
        return real(u,o)};"""
    js(STUB,'none')
    nav('#/fed/7')
    prof=lambda: js("""const m=document.querySelector('#main');return [m.querySelector('h1 bdi')&&m.querySelector('h1 bdi').textContent,(m.querySelector('.look-host')||{}).textContent,!!m.querySelector('[id^=pwn]'),
      (m.querySelector('[data-act=fed-follow]')||{}).innerText,m.innerText.includes('their new posts show in your Home'),m.innerText.includes('approves its followers by hand')]""")
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
    # A handle of 200 characters with no break in it wraps inside the 380 px panel rather than scroll it sideways.
    js("window.__fed.items=[{id:902,kind:'follow',actorId:8,postId:0,commentId:0,rootId:0,text:'',read:false,created:new Date().toISOString()}]")
    if js("return !!document.querySelector('.ntf-panel')"): click('[data-act=bell]')  # closed first, so the next press opens it and reads the bell again
    click('[data-act=bell]')
    wide=lambda: js("const p=document.querySelector('.ntf-panel');return p&&p.innerText.includes('@long.example')?[p.scrollWidth,p.clientWidth]:null")
    check('a handle of 200 characters wraps in the bell: the panel does not scroll sideways',lambda: (lambda v: bool(v) and v[0]<=v[1])(wide()),wide)
    # A moderator's view of a remote account's profile (FMD 2), drawn from an answer the suite supplies (no remote server
    # can exist here): the Moderation card with each action, its sentence and its button; then the same account
    # suspended, with its end, its reason, Lift and, for an admin, Purge.
    MSTUB="""window.__mod=arguments[0];window.__realFetch=window.__realFetch||window.fetch;const real=window.__realFetch;
      const json=(d,st=200)=>Promise.resolve(new Response(JSON.stringify(d),{status:st,headers:{'Content-Type':'application/json'}}));
      window.fetch=(u,o)=>{const p=String(u).replace(/^.*?api[/]/,'');
        if(p==='fed/accounts/7')return json({account:{id:7,name:'Bo',handle:'bo@social.example',host:'social.example',link:'https://social.example/@bo',state:'ok',limited:window.__mod.suspendedUntil?'The moderators here suspended this account.':undefined},view:{follow:'none'},notice:'Once you follow them, their new posts show in your Home.',moderation:window.__mod});
        return real(u,o)};"""
    load(BASE);js(MSTUB,{});nav('#/fed/7')
    card=lambda: js("const c=document.querySelector('.fed-mod');return c&&[c.querySelector('h2').innerText,[...c.querySelectorAll('button')].map(b=>b.innerText.trim()),c.innerText.includes('its follows of members here become requests'),c.innerText.includes('The follows are not restored'),c.innerText.includes('Suspend it first')]")
    check('a moderator sees a remote profile\'s Moderation card: Limit and Suspend, each with what it does, and why Purge waits for a suspension (answer supplied by the suite)',lambda: card()==['Moderation',['Limit…','Suspend…'],True,True,True],card)
    if tab_to('.fed-mod [data-do=mute]','Limit'):
        check('Limit shows a ring of 2 px or more at 3:1 in both themes (keys.py)',not ring_ok(),ring_ok)
        key(ENTER)
        dlg=lambda: js("const f=document.querySelector('#action-form');return f&&[f.querySelector('h2').innerText,f.dataset.remote,f.innerText.includes('kept for the moderators'),[...f.querySelectorAll('[name=dur]')].length,document.activeElement===f.querySelector('textarea')]")
        check('Enter opens the dialog of a limit: its name, what it does, a reason kept for the moderators, ends up to a year, the reason box focused',lambda: dlg()==['Limit @bo@social.example','1',True,7,True],dlg)
        n,d=js(A['NAMES'],'#action-form'),js(A['IDS'])
        check('the dialog: every control has a name, and no id repeats (a11y.py)',n['n']>0 and not n['bad'] and not d['bad'],lambda: (n,d))
        click('#action-form [data-act=modal-close]')
    load(BASE);js(MSTUB,{'suspendedUntil':'2027-01-01T00:00:00Z','suspendReason':'spam waves'});nav('#/fed/7')
    held=lambda: js("const c=document.querySelector('.fed-mod');return c&&[[...c.querySelectorAll('button')].map(b=>b.innerText.trim()),c.innerText.includes('spam waves')]")
    check('suspended: its reason and end, Lift the suspension, and Purge for an admin',lambda: held()==[['Limit…','Lift the suspension','Purge…'],True],held)
    for t in ('dark','light'):
        themed(t)
        n,d=js(A['NAMES'],'.fed-mod'),js(A['IDS'])
        check(f'the Moderation card, {t}: every control has a name, and no id repeats (a11y.py)',n['n']>0 and not n['bad'] and not d['bad'],lambda: (n,d))
        r=js(C['CHECK'],'.fed-mod',False,C['KNOWN'],'')
        check(f'the Moderation card, {t}: text and the edges of controls stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
    themed('dark')
    js("document.querySelector('.fed-mod').scrollIntoView()");frames()
    picture('federated-remote-moderation','Federated mode: the Moderation card of a suspended remote account, drawn from an answer the suite supplies (no server asked)')
    load(BASE);nav('#/admin/federation')
    check('the Federation tab lists the accounts of other servers held now, with what each action does',lambda: (lambda t: 'Accounts of other servers' in t and 'No account of another server is limited or suspended.' in t)(txt('.fed-held') or ''),lambda: txt('.fed-held'))
    # A post of a followed account of another server in Home (FWR 5), drawn from an answer the suite supplies in the
    # page, since no remote server can exist here: a hostile name and text, a content warning, a picture the server
    # copied (here a picture of this origin standing for it) with its description, and a video only as a link.
    load(BASE);js("localStorage.setItem('fl-tl','home')")
    js(STUB,'following')
    js("""const bo={id:7,username:'bo@social.example',displayName:'Bo \\u202eexe <img src=x id=pwn7>',role:'user',avatar:'',remote:{id:7,handle:'bo@social.example',host:'social.example'}};
      const rp={id:4242,userId:7,kind:'post',body:'Practice day with <b id=pwn8>bold</b> and @admin, see https://social.example/@bo/1',prop:'',skill:'',image:'',data:{},created:new Date().toISOString(),
        likes:0,liked:false,comments:[],commentCount:0,remote:{link:'https://social.example/@bo/1',kind:'Note',cw:'Spoilers \\u202e of the routine',
        media:[{kind:'picture',src:'/icon.svg',link:'https://files.social.example/a.png',alt:'Two poi <script>x</script> in a fire circle'},{kind:'video',link:'https://files.social.example/c.mp4',alt:'The flow'}]}};
      const json=(d,st=200)=>Promise.resolve(new Response(JSON.stringify(d),{status:st,headers:{'Content-Type':'application/json'}}));
      const prev=window.fetch;window.fetch=(u,o)=>{const p=String(u).replace(/^.*?api[/]/,'');if(p==='home'||p.startsWith('home?'))return json({posts:[rp],users:{7:bo},more:false});return prev(u,o)};""")
    nav('#/timeline')
    card=lambda: js("""const c=document.querySelector('.fed-post');if(!c)return null;const img=c.querySelector('img.post-img');
      return [(c.querySelector('.post-name bdi')||{}).textContent,(c.querySelector('.look-host')||{}).textContent,!!document.querySelector('[id^=pwn]'),
        [...c.querySelectorAll('a[target=_blank]')].map(a=>a.getAttribute('href')).includes('https://social.example/@bo/1'),
        c.querySelectorAll('[data-act=like],[data-act=cmt-toggle],[data-act=bookmark]').length,
        !!c.querySelector('a[href="#/u/admin"]'),(c.querySelector('.fed-cw summary')||{}).innerText||'']""")
    check('a remote post in Home (answer supplied by the suite, no remote server): the name isolated with its host apart, nothing of its text as markup, the link to the original, no like, comment or bookmark, no @name linked to a member here, folded behind its warning',
      lambda: (lambda v: bool(v) and v[0].startswith('Bo ‮exe') and v[1]=='social.example' and v[2] is False and v[3] and v[4]==0 and v[5] is False and 'Spoilers' in v[6])(card()),card)
    click('.fed-cw summary')
    pic=lambda: js("""const i=document.querySelector('.fed-post img.post-img');return i&&[i.src.startsWith(location.origin+'/'),i.alt,!!document.querySelector('[id^=pwn]'),
      [...document.querySelectorAll('.fed-post .fed-media a')].map(a=>a.getAttribute('href'))]""")
    check('opened: its picture is loaded from this server only, its description is the alt text as written (escaped), and the video is a link to the original (answer supplied by the suite)',
      lambda: pic()==[True,'Two poi <script>x</script> in a fire circle',False,['https://files.social.example/c.mp4']],pic)
    check('a remote post not marked public (no talk in the answer) says it cannot be liked or answered from here',lambda: 'Followers-only posts from other servers cannot be liked or answered from here' in (txt('.fed-post') or ''),lambda: txt('.fed-post'))
    for t in ('dark','light'):
        themed(t)
        n,p,d=js(A['NAMES'],'.fed-post'),js(A['PICTURES']),js(A['IDS'])
        check(f'the remote post, {t}: every control and link has a name, its picture a text alternative, no id repeats (a11y.py)',n['n']>0 and not n['bad'] and not p['bad'] and not d['bad'],lambda: (n,p,d))
        r=js(C['CHECK'],'.fed-post',False,C['KNOWN'],'')
        check(f'the remote post, {t}: text and the edges of controls stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
    themed('dark')
    js("scrollTo(0,0)");frames()
    picture('federated-remote-post','Federated mode: a post of a followed account of another server in Home, opened past its content warning, its picture from this server (an answer supplied by the suite, no server asked)')
    # Replies and likes across servers (FWR 6), drawn from an answer the suite supplies: a public post of another server
    # offers Like and Comment; a post of this member holds a reply from another server (a hostile name and text, a
    # content warning, an attachment) beside a member's comment, and likes counted with remote ones.
    js("localStorage.removeItem('fl-tl')")
    load(BASE)
    ME=get_json(op,'me')['user']
    js(STUB,'following')
    js("""const bo={id:7,username:'bo@social.example',displayName:'Bo \\u202eexe <img src=x id=pwn9>',role:'user',avatar:'',remote:{id:7,handle:'bo@social.example',host:'social.example'}};
      const me={id:arguments[0],username:arguments[1]};const mine={id:me.id,username:me.username,displayName:me.username,role:'admin',avatar:''};
      const now=new Date().toISOString();
      const rp={id:4243,userId:7,kind:'post',body:'A public post over there',prop:'',skill:'',image:'',data:{},created:now,likes:3,liked:false,comments:[],commentCount:0,
        remote:{link:'https://social.example/@bo/2',kind:'Note',talk:true}};
      const lp={id:4244,userId:me.id,kind:'post',body:'My figure 8 at last',prop:'',skill:'',image:'',data:{},created:now,likes:2,liked:false,commentCount:2,comments:[
        {id:51,postId:4244,userId:7,body:'@admin <b id=pwn10>lovely</b> one, see https://social.example/@bo/9',created:now,likes:0,liked:false,replyCount:0,
          remote:{link:'https://social.example/@bo/3',cw:'Spoilers \\u202e ahead',media:[{kind:'picture',link:'https://files.social.example/z.png',alt:'Two staffs'}]}},
        {id:52,postId:4244,userId:me.id,body:'thanks @bo',created:now,likes:1,liked:false,replyCount:0}]};
      const json=(d,st=200)=>Promise.resolve(new Response(JSON.stringify(d),{status:st,headers:{'Content-Type':'application/json'}}));
      const prev=window.fetch;window.fetch=(u,o)=>{const p=String(u).replace(/^.*?api[/]/,'');
        if(p==='posts'||p.startsWith('posts?'))return json({posts:[lp],users:{7:bo,[me.id]:mine},more:false});
        if(p==='home'||p.startsWith('home?'))return json({posts:[rp,lp],users:{7:bo,[me.id]:mine},more:false});
        return prev(u,o)};""",ME['id'],ME['username'])
    js("localStorage.setItem('fl-tl','home')")
    nav('#/timeline')
    talk=lambda: js("""const c=document.querySelector('.fed-post');return c&&[c.querySelectorAll('[data-act=like],[data-act=cmt-toggle]').length,c.innerText.includes('cannot be liked'),(c.querySelector('[data-act=like] .num')||{}).textContent]""")
    check('a public post of another server offers Like and Comment, with its likes counted (answer supplied by the suite)',lambda: talk()==[2,False,'3'],talk)
    reply=lambda: js("""const c=document.querySelector('#c-51');if(!c)return null;return [(c.querySelector('.cmt-name bdi')||{}).textContent,(c.querySelector('.look-host')||{}).textContent,!!document.querySelector('[id^=pwn]'),
      !!c.querySelector('a[href="#/u/admin"]'),!!c.querySelector('[data-act=cmt-edit]'),[...c.querySelectorAll('a[target=_blank]')].map(a=>a.getAttribute('href')).includes('https://social.example/@bo/3'),
      (c.querySelector('.fed-cw summary')||{}).innerText||'',!!c.querySelector('[data-act=cmt-del]'),!!c.querySelector('[data-act=report]')]""")
    check('a reply from another server under a post of this member: its name isolated with its host apart, its text as text with no @name linked, folded behind its warning, a link to the original, no Edit; Delete (the post is the member\'s) and Report',
      lambda: (lambda v: bool(v) and v[0].startswith('Bo ‮exe') and v[1]=='social.example' and v[2] is False and v[3] is False and v[4] is False and v[5] and 'Spoilers' in v[6] and v[7] and v[8])(reply()),reply)
    click('#c-51 .fed-cw summary')
    opened=lambda: js("""const c=document.querySelector('#c-51');return [!!document.querySelector('[id^=pwn]'),(c.querySelector('.cmt-body')||{}).innerText,[...c.querySelectorAll('.fed-media a')].map(a=>a.getAttribute('href'))]""")
    check('opened: the reply\'s words escaped, its web address linked, its attachment only a link to the original',
      lambda: (lambda v: v[0] is False and '<b id=pwn10>lovely</b>' in v[1] and v[2]==['https://files.social.example/z.png'])(opened()),opened)
    check('the member\'s own comment beside it keeps its Edit, and the post counts its likes from both servers',
      lambda: js("return !!document.querySelector('#c-52 [data-act=cmt-edit]')&&document.querySelector('[data-post=\"4244\"] [data-act=like] .num').textContent==='2'"))
    for t in ('dark','light'):
        themed(t)
        n,d=js(A['NAMES'],'[data-post="4244"]'),js(A['IDS'])
        check(f'the reply from another server, {t}: every control and link has a name, no id repeats (a11y.py)',n['n']>0 and not n['bad'] and not d['bad'],lambda: (n,d))
        r=js(C['CHECK'],'[data-post="4244"]',False,C['KNOWN'],'')
        check(f'the reply from another server, {t}: text and the edges of controls stand out (contrast.py)',r['n']>0 and not r['bad'] and not r['unknown'],lambda: (r['n'],r['bad'][:4],r['unknown'][:4]))
    themed('dark')
    js("document.querySelector('[data-post=\"4244\"]').scrollIntoView()");frames()
    picture('federated-remote-reply','Federated mode: a reply from another server under a post of this member, opened past its content warning, beside a member\'s comment, and a public post of another server with Like and Comment (answers supplied by the suite, no server asked)')
    js("localStorage.removeItem('fl-tl')")
    load(BASE)

    # Moving to another server (PRT 2): with the moves flag on, the card offers a move to a member who shares, with the
    # server's warning before the form and the handle to name on the new account, and a place to name the accounts the
    # member moves here from. No other server answers here, so what is sent is a move to an account of this server: it
    # is refused with its own words and nothing changes. The move itself is Go's (TestDBMoveOut) and the federation
    # suite's.
    api(op,'flags',{'flags':{'moves':True}},'PUT')
    mv=lambda: get_json(op,'me').get('federation',{}).get('move') or {'cannot':'no move part'}
    check('with the moves flag on, the server offers moving to a member who shares (once their key is made)',lambda: 'cannot' not in mv(),mv)
    load(BASE);nav('#/settings')
    check('the card offers a move with the server\'s warning before it, the handle to name on the new account, and a place to name old accounts',
      lambda: js("return !!document.querySelector('#move-form [name=target]')&&!!document.querySelector('#alias-form [name=account]')") and '30 days after this one' in (txt('#move-hint') or '') and '@admin@flowa.test' in (txt('#move-first') or ''),lambda: txt('.fed-card'))
    card_rules('the card, with moving')
    js("document.querySelector('.fed-move').scrollIntoView({block:'center'})");frames()
    picture('federated-move','Federated mode: Settings, moving the profile to an account on another server, and naming the accounts moved here from')
    if tab_to('#move-form [name=target]','the new account'):
        type_text('@admin@flowa.test');key(TAB);type_text('flowa-test-only');key(TAB)
        check('the move button comes next by Tab',lambda: on('#move-form button[type=submit]'),active)
        for t in ('dark','light'):
            themed(t)
            check(f'the move button shows a ring of 2 px or more at 3:1, {t}, and nothing covers it (keys.py)',not ring_ok(),ring_ok)
        themed('dark')
        js("window.__confirm=window.confirm;window.__asked=[];window.confirm=m=>{window.__asked.push(m);return true}")
        key(ENTER)
        check('Enter asks first with the warning; the server refuses a move to an account of this server with its words, and nothing changes',
          lambda: js("return window.__asked.length===1&&window.__asked[0].includes('30 days after this one')") and 'Moving between two accounts here is not built' in (txt('#toast') or '') and not mv().get('moved'),
          lambda: (js("return window.__asked"),txt('#toast'),mv()))
        js("window.confirm=window.__confirm")
    if tab_to('#alias-form [name=account]','the old account'):
        type_text('@admin@flowa.test');key(ENTER)
        check('naming an account of this server as an old one is refused with its words',lambda: 'Only an account on another server can move here' in (txt('#toast') or ''),lambda: txt('#toast'))
    api(op,'flags',{'flags':{'moves':False}},'PUT')
    load(BASE)
    api(op,'me',{'federates':False},'PUT');api(op,'flags',{'flags':{'follows':False}},'PUT')
    load(BASE)

    # "Remove me from other servers" (FWR 7): the admin shared the profile before, so has a key, and the card offers it
    # with the server's warning in view; the question asks again before anything is sent, and declining sends nothing.
    # No other server knows this one, so nothing goes out; the Delete itself is Go's (TestDBLeaveOtherServers).
    nav('#/settings')
    leave=lambda: js("const c=document.querySelector('.fed-card');const b=c&&c.querySelector('[data-act=fed-leave]');return b&&[b.innerText.trim(),(document.getElementById(b.getAttribute('aria-describedby'))||{}).innerText||'']")
    check('the card offers "Remove me from other servers", described by the server\'s warning that servers may refuse the address later',
      lambda: (lambda v: bool(v) and v[0]=='Remove me from other servers' and 'may refuse you' in v[1] and 'Your account here stays' in v[1])(leave()),leave)
    card_rules('the card, with Remove me from other servers')
    themed('dark')
    js("document.querySelector('.fed-leave').scrollIntoView({block:'center'})");frames()
    picture('federated-leave','Federated mode: Settings, "Remove me from other servers" with the warning shown before anything is sent')
    if tab_to('[data-act=fed-leave]','Remove me from other servers'):
        for t in ('dark','light'):
            themed(t)
            check(f'the button shows a ring of 2 px or more at 3:1, {t}, and nothing covers it (keys.py)',not ring_ok(),ring_ok)
        themed('dark')
        js("window.__confirm=window.confirm;window.__asked=[];window.confirm=m=>{window.__asked.push(m);return false}")
        key(ENTER)
        check('Enter asks first, with the same warning, and declining sends nothing',
          lambda: js("return window.__asked.length===1&&window.__asked[0].includes('may refuse you')") and 'leave' in get_json(op,'me').get('federation',{}),lambda: (js("return window.__asked"),get_json(op,'me').get('federation')))
        js("window.confirm=m=>{window.__asked.push(m);return true}")
        key(ENTER)
        check('confirmed: the server has it, and the card says the member asked other servers to forget them, with no button',
          lambda: (lambda f: f.get('left') and 'leave' not in f and f.get('optedIn') is False)(get_json(op,'me').get('federation',{})) and js("return !document.querySelector('[data-act=fed-leave]')&&document.querySelector('.fed-card').innerText.includes('You asked other servers to forget you')"),
          lambda: (get_json(op,'me').get('federation'),txt('.fed-card')))
        check('the focus is on the switch, so the keyboard is not lost',lambda: on('[data-change=federates]'),active)
        js("window.confirm=window.__confirm")
    s,h,b=ask('users/admin',headers={'Accept':'application/activity+json'})
    check('the actor of a member who left answers 410 with a Tombstone, so servers that fetch it learn the account is gone',s==410 and json.loads(b).get('type')=='Tombstone',lambda: (s,b[:200]))

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
        json.dump(kept+made,open(idx,'w'));check(f'the pictures are in {idx}',len(made)==20)
    else: print('SKIP the pictures in index.json: no directory for pictures was given',flush=True)

    # The start said once what this version does.
    try: log=open('/tmp/flowa-test/dev.log').read()
    except OSError: log=None
    if log is None: print('SKIP the start log: /tmp/flowa-test/dev.log cannot be read here',flush=True)
    else: check('the start log says once what this version serves',log.count('this version answers discovery (WebFinger and NodeInfo) and serves the profiles and Public posts')==1,lambda: [l for l in log.splitlines() if 'federation' in l][:5])
except Exception as x: check('the federated suite ran',False,repr(x)[:300])
finally: done()
raise SystemExit(1 if errs else 0)
