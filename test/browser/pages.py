# Screenshots of the main pages for a pull request: pages.py <out dir>
# Signed in as the test admin with nunchaku chosen, a sample combo, and a few skills marked.
# Writes <name>.png per page at desktop and phone width, a demo contact sheet, and index.json
# ([{file, title}]) in the order to show them.
# With FLOWA_MOTION=reduce the browser says the person asked for less motion (prefers-reduced-motion), and
# this takes a second, shorter set instead, named still-<name>.png and added to the index.json that is
# there: the pages where something moves by itself, each with checks that it now does not.
import sys,os,io,json
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
from PIL import Image
out=sys.argv[1];os.makedirs(out,exist_ok=True);made=[]
if STILL and os.path.exists(f'{out}/index.json'): made=[m for m in json.load(open(f'{out}/index.json')) if not m['file'].startswith('still-')]
def settle():
    # A picture is taken of a page that has stopped changing: its requests answered, the 3D figure there
    # (or known to be missing), its pictures loaded and two frames drawn.
    idle();figure(20)
    wait_for("return [...document.images].every(i=>i.complete)",'the pictures on the page to load',20)
    idle();frames()
def page(name,title,h,w=1440,ht=1000,full=True):
    cmd('WebDriver:SetWindowRect',width=w,height=ht);nav(h);settle()
    img=Image.open(io.BytesIO(base64.b64decode(cmd('WebDriver:TakeScreenshot',full=full)['value']))).convert('RGB')
    if img.height>2600: img=img.crop((0,0,img.width,2600))
    img.save(f'{out}/{name}.png',optimize=True);made.append({'file':name+'.png','title':title});check(f'{name} ({img.width}x{img.height})',True)
def get_me(op): return json.loads(op.open(BASE+'api/me',timeout=20).read())
def add(sid):
    js("const s=document.querySelector('[data-pick=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()",sid);idle()
# Everything on the page, and what is drawn before and after each element, that still has an animation, a
# transition longer than nothing, or smooth scrolling; and the animations the browser is running now.
MOTION=r"""
const out=[],cls=e=>e.tagName.toLowerCase()+(e.id?'#'+e.id:'')+(typeof e.className==='string'&&e.className.trim()?'.'+e.className.trim().split(/\s+/).join('.'):'');
for(const e of document.querySelectorAll('*'))for(const p of [null,'::before','::after']){
  const s=getComputedStyle(e,p);
  if(s.animationName!=='none')out.push(cls(e)+(p||'')+': animation '+s.animationName);
  if(s.transitionDuration.split(',').some(d=>parseFloat(d)>0))out.push(cls(e)+(p||'')+': transition '+s.transitionProperty+' '+s.transitionDuration);
  if(s.scrollBehavior==='smooth')out.push(cls(e)+(p||'')+': smooth scrolling');
}
for(const a of document.getAnimations())out.push('running: '+(a.animationName||a.transitionProperty||'animation'));
return out;
"""
PLAY="return (document.querySelector(arguments[0])||{}).textContent"
def calm(name):
    bad=js(MOTION);check(f'{name}: nothing on the page animates, fades or scrolls smoothly',not bad,bad[:6])
def paused(name,cv,btn,what):
    # A demo that opened by itself: its picture holds, its button offers Play, and Play starts it.
    wait_for(f"return !!document.querySelector('{cv}')",f'{what} to be on the page',20);figure(40);idle();frames()
    check(f'{name}: {what} opens as a still, with a Play button',holds(cv) and js(PLAY,btn)=='Play',lambda: (js(SNAP,cv),js(PLAY,btn)))
def played(name,cv,btn,what):
    click(btn);check(f'{name}: Play starts {what}, and the button then offers Pause',moves(cv) and js(PLAY,btn)=='Pause',lambda: (js(SNAP,cv),js(PLAY,btn)))
def reduced(op):
    DEMO,COMBO='[data-act=demo][data-k=play]','[data-act=combo-demo][data-k=play]'
    check('the browser says less motion was asked for',js("return matchMedia('(prefers-reduced-motion: reduce)').matches"),lambda: 'the pref ui.prefersReducedMotion did not reach the page')
    # The rule above is shown to find what it looks for: a style that wins over the page's own is found, and
    # one written on an element without !important, as the guided tour writes its fade, is switched off.
    js("const d=document.createElement('div');d.id='motion-probe';d.innerHTML='<i id=\"mp1\" style=\"transition:opacity 1s !important\"></i><i id=\"mp2\" style=\"transition:opacity 1s\"></i><i id=\"mp3\" style=\"animation:slide 1s infinite !important\"></i><i id=\"mp4\" style=\"animation:slide 1s infinite;scroll-behavior:smooth\"></i>';document.body.append(d)")
    bad=js(MOTION);found=sorted({b.split(':')[0] for b in bad if b.startswith('i#mp')})
    check('the rule finds an animation and a transition that are forced, and none that the page switches off',found==['i#mp1','i#mp3'] and any(b.startswith('running') for b in bad),bad)
    js("document.querySelector('#motion-probe').remove()")
    # The landing page plays one demo for a visitor. Here it waits.
    page('still-landing','Reduced motion: landing page, the demo waits for Play','#/')
    paused('landing','.land #demo-cv',DEMO,'the demo');calm('landing');played('landing','.land #demo-cv',DEMO,'the demo')
    signin()
    if js("return !!document.querySelector('input[name=prop]')"): js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()");idle()
    # A skill guide: the demo of a move that repeats waits at its start with its whole path drawn; one that
    # does not repeat waits half way through. The panel does not slide in (calm finds its animation if it does).
    page('still-skill','Reduced motion: skill guide, the demo waits for Play (F5)','#/prop/nunchaku/F/F5')
    paused('skill guide','#panel #demo-cv',DEMO,'the demo');calm('skill guide')
    check('a move that repeats waits at its start',js("return [window.Demo.clip().loop,window.Demo.state.phase]")==[True,0],lambda: js("return [window.Demo.clip().loop,window.Demo.state.phase]"))
    played('skill guide','#panel #demo-cv',DEMO,'the demo')
    # Another skill opened while this one plays, and this one opened again, each wait: playing is asked for per demo.
    nav('#/prop/nunchaku/F/F6');paused('the next skill guide','#panel #demo-cv',DEMO,'the demo')
    check('a move that does not repeat waits half way through',js("return [!!window.Demo.clip().loop,window.Demo.state.phase]")==[False,0.5],lambda: js("return [!!window.Demo.clip().loop,window.Demo.state.phase]"))
    played('the next skill guide','#panel #demo-cv',DEMO,'the demo')
    nav('#/practice');nav('#/prop/nunchaku/F/F6');paused('a skill guide left while playing and opened again','#panel #demo-cv',DEMO,'the demo')
    # The setting is followed while the page is open: taken back, the demo plays as it does for everyone; asked
    # for again, it stops. The change is told to the page the way the browser tells it.
    js("window.Demo.calm.dispatchEvent(new MediaQueryListEvent('change',{media:window.Demo.calm.media,matches:false}))")
    check('when the setting is taken back the demo plays by itself again',moves('#panel #demo-cv') and js(PLAY,DEMO)=='Pause',lambda: js(PLAY,DEMO))
    js("window.Demo.calm.dispatchEvent(new MediaQueryListEvent('change',{media:window.Demo.calm.media,matches:true}))")
    check('when the setting is asked for the demo stops where it is',holds('#panel #demo-cv') and js(PLAY,DEMO)=='Play',lambda: js(PLAY,DEMO))
    page('still-skill-phone','Reduced motion: skill guide, phone','#/prop/nunchaku/F/F5',w=420,ht=900)
    paused('skill guide, phone','#panel #demo-cv',DEMO,'the demo');calm('skill guide, phone')
    cmd('WebDriver:SetWindowRect',width=1440,height=1000)
    # A combo: its player waits on the first skill.
    nav('#/combos');js("const f=document.querySelector('#combo-form');f.name.value='Sample combo';f.requestSubmit()");idle()
    for sid in ['F5','F7','R1']: add(sid)
    nav('#/practice')
    page('still-combos','Reduced motion: combos, the player waits for Play','#/combos')
    paused('combos','#combo-cv',COMBO,'the combo');calm('combos')
    check('the combo waits on its first skill',js("return [window.Demo.combo.i,window.Demo.combo.t,window.Demo.combo.playing]")==[0,0,False],lambda: js("return [window.Demo.combo.i,window.Demo.combo.t,window.Demo.combo.playing]"))
    played('combos','#combo-cv',COMBO,'the combo')
    nav('#/practice');nav('#/combos');paused('combos left while playing and opened again','#combo-cv',COMBO,'the combo')
    # A logged session unlocks an achievement; its card is there at once, without dropping in. Then the
    # dashboard with the heatmap, which is drawn whole, and its bars, which do not grow.
    nav('#/practice');js("const f=document.querySelector('#log-form');f.minutes.value='35';f.requestSubmit()")
    wait_for("return !!document.querySelector('#unlock .unlock-card')",'the card of an unlocked achievement',20)
    card=js("const s=getComputedStyle(document.querySelector('#unlock .unlock-card'));return [s.animationName,s.opacity,s.transform]")
    check('the achievement card is there at once: no animation, fully drawn, in its place',card==['none','1','none'],card);calm('practice, with the achievement card')
    page('still-dashboard','Reduced motion: dashboard, with the heatmap','#/')
    heat=js("const h=document.querySelector('.heat');if(!h)return null;const s=getComputedStyle(h);return [s.animationName,s.opacity,h.querySelectorAll('rect').length>100,[...h.querySelectorAll('rect')].every(r=>getComputedStyle(r).opacity==='1'&&getComputedStyle(r).animationName==='none')]")
    check('the heatmap is drawn whole at once: no animation on it or its days',heat==['none','1',True,True],heat);calm('dashboard')
    # The menu of a narrow window opens without sliding.
    cmd('WebDriver:SetWindowRect',width=420,height=900);wait_for("return getComputedStyle(document.querySelector('.menu-btn')).display!=='none'",'the window to be narrow enough for the menu button')
    click('.menu-btn');side=js("const s=getComputedStyle(document.querySelector('#side'));return [s.transitionDuration,s.transform]")
    check('the menu of a narrow window is open at once',side==['0s','none'],side);calm('menu, narrow window')
    js("document.body.classList.remove('nav-open')");cmd('WebDriver:SetWindowRect',width=1440,height=1000)
    # The avatar plays a move by itself in its editor; here it stands, with a button to play it.
    api(op,'flags',{'flags':{'avatars':True}},'PUT')
    api(op,'me',{'avatarCfg':{'v':1,'body':'average','build':'average','skin':'#a0603f','hair':'ponytail','hairColor':'#1c1714','eyes':'round','brows':'arched','mouth':'smile','top':'tshirt','topColor':'#2a7fa8','bottoms':'leggings','bottomsColor':'#2b2b2b','shoes':'trainers','shoesColor':'#f2f0e9','hat':'none','hatColor':'#c4572e','glasses':'round','wristbands':'yes','wristColor':'#d9a441','props':{'nunchaku':'#e0533a'}}},'PUT')
    load()
    page('still-avatar','Reduced motion: avatar editor, the avatar waits for Play','#/avatar')
    paused('avatar editor','#av-cv','[data-act=av-motion]','the avatar');calm('avatar editor');played('avatar editor','#av-cv','[data-act=av-motion]','the avatar')
    # The guided tour, for someone new: its steps and the dark sheet around the highlight do not fade.
    api(op,'flags',{'flags':{'tour':True,'avatars':False}},'PUT')
    js("return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
    load(BASE+'?tour#/signup');wait_for("return !!document.querySelector('#auth-form')",'the form for a new account',20)
    js("const f=document.querySelector('#auth-form');f.elements.username.value='calm';f.elements.password.value=f.elements.again.value='flowa-test-only';f.requestSubmit()")
    wait_for(lambda: js("return (document.querySelector('.nav-profile .tiny')||{}).textContent")=='@calm','@calm to be signed in',30);idle()
    js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()")
    TOUR="[...document.querySelectorAll('[role=alertdialog] button')]"
    FLOAT="const f=document.querySelector('.react-joyride__floater');if(!f)return null;const s=getComputedStyle(f);return [s.transitionDuration,s.opacity]"
    wait_for(f"return {TOUR}.length>0",'the guided tour to open',30);idle()
    check('the first step of the tour is there at once: no fade on it',lambda: js(FLOAT)==['0s','1'],lambda: js(FLOAT));calm('guided tour')
    page('still-tour','Reduced motion: guided tour','#/',full=False)
    js(f"{TOUR}.find(b=>b.textContent.startsWith('Next')).click()")
    wait_for(f"return {TOUR}.some(b=>b.textContent.startsWith('Back'))",'the second step of the tour');idle()
    check('the second step of the tour is there at once too',lambda: js(FLOAT)==['0s','1'],lambda: js(FLOAT));calm('guided tour, second step')
try:
    op=admin();start()
    # The second set, for less motion, is all this run takes: done() below still counts its checks.
    if STILL: reduced(op);json.dump(made,open(f'{out}/index.json','w'));raise SystemExit(0)
    # What a visitor sees first: the landing page with a demo and the map in small.
    page('landing','Landing page','#/')
    page('landing-phone','Landing page, phone','#/',w=420,ht=900)
    cmd('WebDriver:SetWindowRect',width=1440,height=1000)
    # Signed out: the menu lists what opens without an account, and a page that needs one says so in one sentence.
    MENU="return [...document.querySelectorAll('#side a.nav-a')].map(a=>[a.textContent.trim(),a.getAttribute('href')]).filter(x=>!x[1].startsWith('#/prop/'))"
    side=js(MENU);who=js("return document.querySelector('#side .nav-profile b').textContent")
    check('signed out: the menu is Welcome, Skill maps and Sign in, with Settings below',side==[['Welcome','#/'],['Skill maps','#/maps'],['Settings','#/settings']] and who=='Sign in',(side,who))
    walls={'practice':'Sign in to log your practice.','combos':'Sign in to build combos.','plan':'Sign in to follow the plan.','timeline':'Sign in to use the timeline.','bookmarks':'Sign in to keep bookmarks.','achievements':'Sign in to see your achievements.','avatar':'Sign in to make your avatar.','profile':'Sign in to see your profile.'}
    said={}
    for p in walls: nav('#/'+p);said[p]=js("return (document.querySelector('.auth p')||{}).textContent")
    check('signed out: each page that needs an account says what signing in is for',said==walls,said)
    page('signed-out','Signed out: a page that needs an account','#/timeline')
    page('signed-out-phone','Signed out, phone','#/timeline',w=420,ht=900)
    cmd('WebDriver:SetWindowRect',width=1440,height=1000)
    signin()
    if js("return !!document.querySelector('input[name=prop]')"): js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()");idle()
    # Some progress so the dashboard and map are not empty.
    for sid,v in [('F1',4),('F2',4),('F3',3),('F4',2)]:
        nav(f'#/prop/nunchaku/F/{sid}');js("for(const b of document.querySelectorAll(`[data-act=level][data-id=${arguments[0]}][data-v='${arguments[1]}']`))b.click()",sid,v);idle()
    nav('#/combos');js("const f=document.querySelector('#combo-form');f.name.value='Sample combo';f.requestSubmit()");idle()
    for sid in ['F5','F7','R1','D4']: add(sid)
    page('dashboard','Dashboard','#/')
    # The two streaks say what they count, the menu has no link out of Flowa, and Settings links to what is planned.
    lbl=js("return [[...document.querySelectorAll('.stat .lbl')].map(l=>l.textContent),document.querySelector('#main').innerText.includes('Streak across all your props')]")
    check('the dashboard names both streaks: this prop, and all props',('Nunchaku streak' in lbl[0]) and 'Streak' not in lbl[0] and lbl[1],lbl)
    out_links=js("return [...document.querySelectorAll('#side a')].map(a=>a.getAttribute('href')).filter(h=>!h.startsWith('#'))")
    check('the menu has no link out of Flowa unless the server names one',out_links==[],out_links)
    nav('#/settings')
    docs=js("return [...document.querySelectorAll('#main a[href^=\"https://github.com/\"]')].map(a=>[a.textContent.trim(),a.getAttribute('href'),a.rel,a.target])")
    check('Settings links to the roadmap and the epics in the repository',docs==[['Roadmap','https://github.com/TuanSSM/flowtopia/blob/main/docs/roadmap.md','noopener noreferrer','_blank'],['Epics','https://github.com/TuanSSM/flowtopia/blob/main/docs/epics.md','noopener noreferrer','_blank']],docs)
    page('map','Skill map','#/maps')
    page('skill','Skill guide with demo (F5)','#/prop/nunchaku/F/F5')
    page('combos','Combos','#/combos')
    page('practice','Practice','#/practice')
    page('plan','Plan','#/plan')
    # Community: a post with a tagged skill, then the timeline, achievements and the public profile.
    nav('#/timeline');js("pickAdd({kind:'skill',prop:'nunchaku',id:'F4'});pickAdd({kind:'skill',prop:'nunchaku',id:'F5'});const f=document.querySelector('#compose-form');f.body.value='Clean F4 today, finally. On to the figure-8 chain.';f.requestSubmit()");idle()
    page('timeline','Timeline','#/timeline')
    page('achievements','Achievements','#/achievements')
    page('profile-public','Public profile','#/u/admin')
    page('timeline-phone','Timeline, phone','#/timeline',w=420,ht=900)
    # Link cards, with previews from the stand-in for the three sites when the server can reach it.
    if os.environ.get('FLOWA_PREVIEW_BASE'):
        import stub;stub.start()
        lp=api(op,'posts',{'body':'Two clips from this week','links':['https://youtu.be/dQw4w9WgXcQ','https://www.instagram.com/reel/C9xYz_-AbCd/']})['posts'][0]['id']
        # The server fetches the previews after the post is saved.
        wait_for(lambda: all(c['status']=='ok' for c in json.loads(op.open(BASE+f'api/posts/{lp}',timeout=20).read())['posts'][0].get('links') or [{'status':''}]),'the previews of the two links',20)
        nav('#/practice') # leave the timeline, so it loads the new post
        page('links-phone','Timeline with link cards, phone','#/timeline',w=420,ht=900)
    # A post with a short conversation, to show threads.
    pid=json.loads(js("return fetch('api/posts',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({body:'Which drill fixed your wrist?'})}).then(r=>r.text())"))['posts'][0]['id']
    cid=json.loads(js("return fetch('api/posts/'+arguments[0]+'/comments',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({body:'Slow figure-8s in front of a mirror.'})}).then(r=>r.text())",pid))['comment']['id']
    js("return fetch('api/posts/'+arguments[0]+'/comments',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({body:'And a lighter pair to start.',parentId:arguments[1]})}).then(r=>r.text())",pid,cid)
    page('replies-phone','Post with replies, phone',f'#/post/{pid}',w=420,ht=900)
    # Moderation: another member reports the post above; the queue and the admin dashboard.
    api(op,'flags',{'flags':{'moderation':True}},'PUT');js("return fetch('api/me').then(r=>r.json()).then(d=>{flags=d.flags})");idle()
    other=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(other,'signup',{'username':'reporter','password':'flowa-test-only'});api(other,'me',{'props':['nunchaku']},'PUT')
    newbie=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(newbie,'signup',{'username':'newbie','password':'flowa-test-only'});api(newbie,'me',{'props':['nunchaku']},'PUT')
    np=api(newbie,'posts',{'body':'Selling cheap foam nunchaku, message me'})['posts'][0]['id']
    api(other,'reports',{'kind':'post','id':np,'reason':'spam','note':'Same post in three groups'})
    load()
    page('mod','Moderation queue','#/mod')
    page('admin-dashboard','Admin dashboard','#/admin/dashboard')
    page('admin-dashboard-phone','Admin dashboard, phone','#/admin/dashboard',w=420,ht=900)
    api(op,'flags',{'flags':{'catalogEditor':True}},'PUT');load()
    page('editor','Skill map editor','#/admin/maps/nunchaku')
    page('map-phone','Skill map, phone','#/maps',w=420,ht=900)
    page('combos-phone','Combos, phone','#/combos',w=420,ht=900)
    page('skill-phone','Skill guide, phone','#/prop/nunchaku/F/F5',w=420,ht=900)
    # The avatar editor, with avatars on and a saved avatar.
    api(op,'flags',{'flags':{'avatars':True}},'PUT')
    api(op,'me',{'avatarCfg':{'v':1,'body':'average','build':'average','skin':'#a0603f','hair':'ponytail','hairColor':'#1c1714','eyes':'round','brows':'arched','mouth':'smile','top':'tshirt','topColor':'#2a7fa8','bottoms':'leggings','bottomsColor':'#2b2b2b','shoes':'trainers','shoesColor':'#f2f0e9','hat':'none','hatColor':'#c4572e','glasses':'round','wristbands':'yes','wristColor':'#d9a441','props':{'nunchaku':'#e0533a'}}},'PUT')
    load()
    page('avatar-phone','Avatar editor, phone','#/avatar',w=420,ht=900)
    page('avatar','Avatar editor','#/avatar')
    # A contact sheet of demos, as shots.py makes it.
    cmd('WebDriver:SetWindowRect',width=1440,height=1000);figure(20)
    fig=js("return window.Figure3D?(window.Figure3D.ready?'3D figure':'drawn figure (no WebGL here)'):'drawn figure'")
    SZ=320;tiles=[]
    picks=[('nunchaku','F5','0.15'),('nunchaku','F7','0.4'),('nunchaku','R1','0.3'),('nunchaku','D4','0.6'),('poi','F2','0.2'),('poi','L2','0.5'),('staff','F2','0.3'),('doublestaff','T1','0.3'),('buugeng','F3','0.4')]
    for prop,sid,ph in picks:
        nav(f'#/prop/{prop}/{sid[0]}/{sid}')
        js("const D=window.Demo;D.state.playing=false;D.state.phase=Number(arguments[0]);D.mount()",ph);idle();js("window.Demo.mount()");frames()
        b=js("const c=document.querySelector('#demo-cv');return c?c.toDataURL('image/png').split(',')[1]:null")
        if b: tiles.append(Image.open(io.BytesIO(base64.b64decode(b))).convert('RGB').resize((SZ,SZ)))
    cols=3;rows=(len(tiles)+cols-1)//cols;sheet=Image.new('RGB',(cols*SZ,max(1,rows)*SZ),(30,30,30))
    for i,t in enumerate(tiles): sheet.paste(t,((i%cols)*SZ,(i//cols)*SZ))
    sheet.save(f'{out}/demos.png',optimize=True);made.append({'file':'demos.png','title':f'Demos ({len(tiles)} of them, {fig})'});check(f'demo sheet ({len(tiles)} tiles, {fig})',len(tiles)>0,len(tiles))
    cmd('WebDriver:SetWindowRect',width=1440,height=1000)
    # Home, with the follows flag on (it is off by default): the posts of a member the admin follows and
    # the admin's own, with Community beside it. Then the flag goes off again for the pages after.
    kai=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(kai,'signup',{'username':'kai','password':'flowa-test-only','displayName':'Kai Loops'});api(kai,'me',{'props':['poi']},'PUT')
    api(kai,'posts',{'body':'Three-beat weave at half speed, then at full. The turn finally sits on the beat.','tags':[{'prop':'poi','skill':'F2'}]})
    api(op,'flags',{'flags':{'follows':True}},'PUT');api(op,'users/kai/follow',{},'PUT')
    js("localStorage.setItem('fl-tl','home')");load();nav('#/practice')
    page('home','Home: the people you follow, and your own posts','#/timeline')
    check('Home is open, with Community beside it, and shows the followed member',js("return document.querySelector('.tl-tabs .on')?.dataset.v")=='home' and 'Kai Loops' in txt('[data-tour=feed]'))
    page('home-phone','Home, phone','#/timeline',w=420,ht=900)
    page('profile-follows','Public profile with Follow and the counts','#/u/kai')
    cmd('WebDriver:SetWindowRect',width=1440,height=1000)
    # Taken last but one, so the pictures before are as they were: then kai's post and the follow go,
    # with the flag and the remembered tab, before the first-run picture.
    posts_of=lambda name: json.loads(op.open(BASE+'api/posts?user='+name,timeout=20).read())['posts']
    kp=[p['id'] for p in posts_of('kai')]
    for pid in kp: api(kai,f'posts/{pid}',{},'DELETE')
    api(op,'users/kai/follow',{},'DELETE')
    gone=json.loads(op.open(BASE+'api/following',timeout=20).read())['following']==[]
    api(op,'flags',{'flags':{'follows':False}},'PUT');js("localStorage.removeItem('fl-tl')");load()
    check('after the Home pictures kai has no post, the admin follows nobody, and the flag is off again',kp!=[] and posts_of('kai')==[] and gone and not get_me(op)['flags']['follows'])
    # First run: a new member before any prop is chosen. The menu lists only what opens.
    new=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));api(new,'signup',{'username':'newcomer','password':'flowa-test-only'})
    js("return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
    load(BASE+'?first#/signin');signin('newcomer')
    side=js("return [...document.querySelectorAll('#side a,#side button')].map(e=>e.getAttribute('href')||e.dataset.act)")
    check('first run: the menu lists only the profile, the chooser and Settings',side==['#/profile','#/','#/settings'],side)
    page('first-run','First run: choosing props','#/')
    json.dump(made,open(f'{out}/index.json','w'))
finally:
    done()
