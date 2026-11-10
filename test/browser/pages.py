# Screenshots of the main pages for a pull request: pages.py <out dir>
# Signed in as the test admin with nunchaku chosen, a sample combo, and a few skills marked.
# Writes <name>.png per page at desktop and phone width, a demo contact sheet, and index.json
# ([{file, title}]) in the order to show them.
import sys,os,io,json
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
from PIL import Image
out=sys.argv[1];os.makedirs(out,exist_ok=True);made=[]
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
def add(sid):
    js("const s=document.querySelector('[data-pick=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()",sid);idle()
try:
    op=admin();start()
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
