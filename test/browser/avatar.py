# Avatars: the first-run prompt, the editor (preview, swatches, Surprise me and Undo, save), the
# avatar in demos with its body preset and with the flag off, the headshot as profile picture,
# someone else's avatar on their profile, stickers on the unlock card, the milestone post and every
# streak overlay template, every part against the body's capsules in five poses, and the frame
# budget (FRAME_BUDGET_MS, 33 by default). Screenshots go to $SHOTS when it is set.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
SHOTS=os.environ.get('SHOTS')
BUDGET=float(os.environ.get('FRAME_BUDGET_MS','33'))
def get(op,path): return json.loads(op.open(BASE+'api/'+path,timeout=20).read())
def account(name,onboard=True):
    o=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(o,'signup',{'username':name,'password':'flowa-test-only'})
    if onboard: api(o,'me',{'props':['nunchaku']},'PUT')
    return o
def signin_as(name):
    js("return api('signout','POST').then(r=>r.ok)")
    load(BASE+f'?as={name}#/signin') # a new query string forces a full load
    wait_for("return !!document.querySelector('#auth-form')",'the sign-in form',20)
    js("const f=document.querySelector('#auth-form');f.elements.username.value=arguments[0];f.elements.password.value='flowa-test-only';f.requestSubmit()",name)
    wait_for(lambda: js("return (document.querySelector('.nav-profile .tiny')||{}).textContent")=='@'+name,f'@{name} to be signed in',30);idle()
def my_cfg(): return js("return fetch('api/me').then(r=>r.json()).then(d=>JSON.stringify(d.user.avatarCfg||{}))")
def snap(sel): return js("const c=document.querySelector(arguments[0]);if(!c)return null;const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let h=0;for(let i=0;i<d.length;i+=4*7)h=(h*31+d[i]+d[i+1]*3+d[i+2]*7)>>>0;return h",sel)
def shot_if(name):
    if SHOTS: os.makedirs(SHOTS,exist_ok=True);shot(f'{SHOTS}/{name}.png')
n_reload=[0]
def reload(h):
    # A full load, so the page reads the flags and the member's avatar again.
    # Unsaved progress would hold the page with a prompt, so wait for it to be saved first.
    idle('the progress to be saved')
    n_reload[0]+=1;load(BASE+f'?r={n_reload[0]}'+h)
def figure_ready(): return wait_for("return !!(window.Figure3D&&window.Figure3D.ready)",'the 3D figure to load',30)
# The preview turns slowly, so it is drawn again and again; this waits for a drawing other than `prev`.
def redrawn(prev,what='the preview to be drawn again'): return wait_for(lambda: (lambda h: h if h and h!=prev else None)(snap('#av-cv')),what)
def wearing(): return json.loads(js("return JSON.stringify(window.Figure3D.avatar())") or 'null')
HEAVY={'v':1,'body':'tall','build':'broad','frame':'feminine','skin':'#5b3422','hair':'curly','hairColor':'#d6b47a','eyes':'wide','brows':'thick','mouth':'grin','top':'hoodie','topColor':'#c4572e','bottoms':'trousers','bottomsColor':'#1f2a44','shoes':'boots','shoesColor':'#2b2b2b','hat':'bucket','hatColor':'#2f6f4f','glasses':'square','wristbands':'yes','wristColor':'#d9a441','props':{'nunchaku':'#e0533a'}}
try:
    op=admin();api(op,'me',{'props':['nunchaku']},'PUT')
    api(op,'flags',{'flags':{'avatars':True,'community':True,'game':True,'model3d':True}},'PUT')
    start()
    # ---- First run: after picking props a new member is offered the editor, and can skip or keep.
    account('newbie',onboard=False);signin_as('newbie')
    # Before props are chosen the menu lists only what opens: the profile, the chooser and Settings.
    menu=lambda: js("return [...document.querySelectorAll('#side a,#side button')].map(e=>e.getAttribute('href')||e.dataset.act)")
    check('before props are chosen the menu lists only the profile, the chooser and Settings',menu()==['#/profile','#/','#/settings'],menu())
    nav('#/settings')
    check('Settings opens before props are chosen',txt('#main h1')=='Settings' and js("return document.querySelector('#side a[href=\"#/settings\"]').getAttribute('aria-current')")=='page',txt('#main h1'))
    nav('#/profile')
    check('so does the profile, with Sign out',txt('#main h1')=='Profile' and js("return !!document.querySelector('#main [data-act=signout]')"),txt('#main h1'))
    nav('#/practice')
    check('every other page still asks for props first',js("return !!document.querySelector('#props-form[data-first]')") and menu()==['#/profile','#/','#/settings'],menu())
    js("const f=document.querySelector('#props-form');f.querySelector('input[value=nunchaku]').checked=true;f.requestSubmit()")
    wait_for("return !!document.querySelector('.av-first')",'the avatar offer',20)
    check('the full menu is back once props are chosen',len(menu())>8 and '#/practice' in menu(),menu())
    btns=js("return [...document.querySelectorAll('#main .btn')].filter(b=>/av-save|av-skip|av-remove/.test(b.dataset.act||'')||b.getAttribute('href')==='#/profile').map(b=>b.textContent)")
    check('the avatar offer has one set of buttons: keep or skip',btns==['Keep this avatar','Skip for now'],btns)
    first=js("return window.AV.draft&&JSON.stringify(window.AV.draft)") or '{}'
    check('after choosing props a new member is offered the editor',js("return location.hash")=='#/avatar/first' and js("return !!document.querySelector('.av-first')"))
    check('the editor starts from a random complete avatar',json.loads(first).get('v')==1 and len(json.loads(first))>=20,first)
    click('[data-act=av-skip]')
    check('skipping goes to the dashboard and saves nothing',lambda: js("return location.hash") in ('#/','') and my_cfg()=='{}',my_cfg)
    account('keeper',onboard=False);signin_as('keeper')
    js("const f=document.querySelector('#props-form');f.querySelector('input[value=nunchaku]').checked=true;f.requestSubmit()")
    wait_for("return !!document.querySelector('.av-first')",'the avatar offer',20)
    draft=js("return JSON.stringify(window.AV.draft)")
    click('.av-first [data-act=av-save]');wait_for(lambda: my_cfg()!='{}','the avatar to be saved',20)
    check('keeping the offered avatar saves it',json.loads(my_cfg())==json.loads(draft),(my_cfg(),draft))
    # ---- The editor.
    nav('#/avatar');figure_ready();redrawn(None,'the preview to be drawn')
    btns=js("return [...document.querySelectorAll('#main .btn')].filter(b=>/av-save|av-skip|av-remove/.test(b.dataset.act||'')||b.getAttribute('href')==='#/profile').map(b=>b.textContent)")
    check('opened later, the editor has its own Save, Remove and Back',btns==['Save avatar','Remove avatar','Back to profile'],btns)
    shot_if('avatar-editor')
    a=snap('#av-cv');click('[data-act=av-tab][data-v=hair]');idle()
    cur=json.loads(js("return JSON.stringify(window.AV.draft)"))
    other=[v for v in ['curly','bun','long','buzz'] if v!=cur['hair']][0]
    click(f'[data-act=av-set][data-k=hair][data-v={other}]')
    b=redrawn(a,'the preview with the new hair')
    click('[data-act=av-tab][data-v=face]');idle()
    tone=[t for t in ['#2f1b12','#f6dccb'] if t!=cur['skin']][0]
    click(f'[data-act=av-set][data-k=skin][data-v="{tone}"]')
    c=redrawn(b,'the preview with the new skin tone')
    check('changing hair and then skin tone changes the preview',a and b and c and a!=b and b!=c,(a,b,c))
    # Frames: the feminine and masculine frames each draw a different figure from the neutral one.
    click('[data-act=av-tab][data-v=body]');idle()
    fr={}
    for f in ['neutral','feminine','masculine']:
        click(f'[data-act=av-set][data-k=frame][data-v={f}]');wait_for(lambda: js("return window.Figure3D.body().frame")==f,f'the figure to take the {f} frame')
        fr[f]=redrawn(c,f'the preview with the {f} frame');c=fr[f]
    check('the neutral, feminine and masculine frames each draw a different figure',all(fr.values()) and len(set(fr.values()))==3 and js("return window.Figure3D.body().frame")=='masculine',js("return window.Figure3D.body()"))
    # Turning: a drag (pointer events) turns the figure.
    y0=js("return window.Demo.previewState.yaw")
    js("const c=document.querySelector('#av-cv'),r=c.getBoundingClientRect(),o={bubbles:true,pointerId:1,clientY:r.top+50};c.dispatchEvent(new PointerEvent('pointerdown',{...o,clientX:r.left+50}));c.dispatchEvent(new PointerEvent('pointermove',{...o,clientX:r.left+150}));c.dispatchEvent(new PointerEvent('pointerup',{...o,clientX:r.left+150}))")
    check('dragging the preview turns it',abs(js("return window.Demo.previewState.yaw")-y0)>0.5)
    before=js("return JSON.stringify(window.AV.draft)")
    click('[data-act=av-random]');idle()
    rnd=js("return JSON.stringify(window.AV.draft)")
    click('[data-act=av-undo]');idle()
    check('Surprise me changes the avatar and Undo brings back the previous one exactly',rnd!=before and js("return JSON.stringify(window.AV.draft)")==before)
    js("document.querySelector('#av-headshot').checked=true;document.querySelector('#av-headshot').dispatchEvent(new Event('change',{bubbles:true}))")
    click('.av-panel [data-act=av-save]');wait_for(lambda: json.loads(my_cfg())==json.loads(before),'the avatar to be saved',20)
    reload('#/avatar');wait_for("return !!(window.AV&&window.AV.draft)",'the editor to open the saved avatar',20)
    check('the saved avatar is there after a reload',json.loads(js("return JSON.stringify(window.AV.draft||{})"))==json.loads(before) and json.loads(my_cfg())==json.loads(before))
    # ---- Headshot as profile picture: in the menu, on posts and on comments.
    pic=js("return document.querySelector('.nav-profile img.avatar')?.getAttribute('src')||''")
    check('the headshot is the profile picture in the menu',pic.startswith('data:image/jpeg'),pic[:30])
    nav('#/timeline')
    js("const t=document.querySelector('#compose-form textarea');t.value='My avatar is here';t.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('#compose-form').requestSubmit()")
    pid=wait_for("return document.querySelector('.post')?.dataset.post",'the post to show',20);idle()
    js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-form');if(f){f.body.value='And here';f.requestSubmit()}",pid)
    # Others see the picture by URL (lists and feeds do not carry it inline); it must load.
    check('the headshot shows on the post and on the comment',lambda: js("const p=document.querySelector('.post[data-post=\"'+arguments[0]+'\"]'),ok=i=>!!i&&i.src.includes('/api/users/')&&i.complete&&i.naturalWidth>0;return !!p&&ok(p.querySelector('header img.avatar'))&&[...p.querySelectorAll('.cmt img.avatar')].some(ok)",pid))
    # ---- Someone else's avatar plays a skill they have clean, on their profile.
    bea=account('bea');api(bea,'me',{'avatarCfg':HEAVY},'PUT')
    api(bea,'progress/nunchaku',{'rev':0,'state':{'skills':{'F5':{'O':2},'F3':{'O':1}}}},'PUT')
    nav('#/u/bea');figure_ready()
    check("on someone's profile their avatar plays a skill they have clean",lambda: js("return document.querySelector('#av-other')?.dataset.key")=='nunchaku:F5' and wearing()==HEAVY,lambda: (js("return document.querySelector('#av-other')?.dataset.key"),wearing()))
    shot_if('avatar-profile')
    # ---- Demos play on the member's avatar and body preset; off with the flag.
    api(op,'flags',{'flags':{'demos':True}},'PUT')
    js("return fetch('api/me',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({avatarCfg:arguments[0]})}).then(r=>r.ok)",HEAVY)
    reload('#/prop/nunchaku/F/F5');figure_ready();poll(lambda: js("return !!window.Demo.clip()") and wearing()==HEAVY)
    worn=js("return JSON.stringify(window.Figure3D.avatar())")
    body=js("return [window.Demo.body.sc,window.Demo.body.bd]")
    note=js("return document.querySelector('.demo-body')?.textContent||''")
    fail=js("return fetch('clips/presets.json').then(r=>r.json()).then(p=>{const f=window.Demo.clip()?.f;return f&&p.fail[f]?p.fail[f]['tall-broad']||'':''})")
    check("a demo plays in the member's avatar",json.loads(worn or 'null')==HEAVY,worn)
    check('on their body preset when the clip passes there, and on the standard body with a note when it does not',(body==[1.035,1.1] and not note) if not fail else (body==[1,1] and 'standard body' in note and fail in note),(body,note,fail))
    shot_if('avatar-demo')
    api(op,'flags',{'flags':{'avatars':False}},'PUT')
    reload('#/prop/nunchaku/F/F5');figure_ready();wait_for("return !!window.Demo.clip()",'the demo to load its clip',20)
    check('with avatars off demos use the standard figure and body',js("return window.Figure3D.avatar()") is None and js("return [window.Demo.body.sc,window.Demo.body.bd]")==[1,1])
    api(op,'flags',{'flags':{'avatars':True}},'PUT')
    reload('#/prop/nunchaku/F/F5');figure_ready();wait_for("return !!window.Demo.clip()",'the demo to load its clip',20)
    # ---- Frame time: one frame of a demo (nunchaku F5, 720 px) on each path the player has, the median of 60
    # drawings after 20 to warm up (with one, the path timed first read a third slower than the heavier one
    # timed after it: the browser was still compiling the drawing code). The 3D figure is skinned and drawn through WebGL, in software here; the
    # drawn figure is what a browser without WebGL gets. A pixel is read back after each drawing: a canvas
    # only notes what to draw until someone looks, and without the look the drawn figure would time at nothing. The "frame ms" line goes into the pull request's
    # comment (process_pr.yml), so a change to the player or the figure shows what it cost before it merges.
    FRAME="""const D=window.Demo,c=D.clip();if(!c)return null;const cv=document.createElement('canvas');cv.width=cv.height=720;const x=cv.getContext('2d'),col=D.colors(),who=arguments[0],flat=arguments[1];
      if(flat)D.figure(false);
      try{for(let i=0;i<20;i++)D.draw(x,720,c,i/20,'angle',false,col,false,who);const t=[];for(let i=0;i<60;i++){const t0=performance.now();D.draw(x,720,c,i/60,'angle',false,col,false,who);x.getImageData(360,360,1,1);t.push(performance.now()-t0)}
        t.sort((a,b)=>a-b);return Math.round((t[29]+t[30])/2*10)/10}
      finally{if(flat)D.figure(true)}"""
    base=js(FRAME,None,False);ms=js(FRAME,HEAVY,False);flat=js(FRAME,None,True)
    print(f'frame ms: 3D figure {base}, 3D figure with the heaviest avatar {ms}, drawn figure {flat} (median of 60 frames; budget {BUDGET:.0f} for the heaviest avatar)')
    check(f'one demo frame with the heaviest avatar draws within {BUDGET:.0f} ms ({ms} ms; the standard figure takes {base} ms)',ms is not None and ms<=BUDGET,ms)
    check(f'the drawn figure, for a browser without WebGL, draws a frame too and the 3D figure is back afterwards ({flat} ms)',flat is not None and flat<=BUDGET and js("return !!(window.Figure3D&&window.Figure3D.ready)") and js(FRAME,None,False) is not None,flat)
    # ---- Parts: every part, in five poses, shows and stays close to the skin (measured against the mesh).
    POSES={'stand':{},'arms up':{'hL':[0.25,1.95,0.05],'hR':[-0.25,1.95,0.05]},'deep bend':{'pelvis':[0,0.78,-0.12],'spine':[0,0.75,0.66],'hL':[0.2,0.75,0.35],'hR':[-0.2,0.75,0.35]},'turn':{'face':[0.8,0,0.6]},'hand behind back':{'hR':[-0.1,1.0,-0.2]}}
    CFGS=[HEAVY,{**HEAVY,'hair':'ponytail','hat':'cap','glasses':'round','eyes':'sleepy','mouth':'open','brows':'arched'},{**HEAVY,'hair':'long','hat':'beanie','glasses':'none','mouth':'smile','brows':'angled','eyes':'narrow'},{**HEAVY,'hair':'bun','hat':'none','mouth':'neutral','brows':'straight','eyes':'round'},{**HEAVY,'hair':'buzz','hat':'none'},{**HEAVY,'hair':'short','hat':'none'}]
    worst=js("""const D=window.Demo,P=arguments[0],C=arguments[1],out={},least={};
      const cv=document.createElement('canvas');cv.width=cv.height=200;const x=cv.getContext('2d'),col=D.colors();
      for(const cfg of C)for(const [name,pose] of Object.entries(P)){
        D.draw(x,200,{fr:[pose],props:[],dur:1,still:true},0,0.4,false,col,true,cfg);
        for(const [part,w] of Object.entries(window.Figure3D.partReport())){if(!out[part])out[part]={};out[part][name]=Math.max(out[part][name]??-1,w.out);least[part]=Math.min(least[part]??1,w.out)}}
      return {out,least}""",POSES,CFGS) or {}
    least=worst.get('least',{});worst=worst.get('out',{})
    print('parts, furthest outside the skin by pose (m):',worst)
    print('parts, least proud of the skin (m):',least)
    # Face parts, glasses and wristbands sit on the skin; hats have brims and hair hangs (a ponytail,
    # long hair), so they reach further, as real ones do. Every part shows: at least 2 mm proud of the skin.
    LIMITS={'hat':0.07,'hair':0.10}
    over={k:v for k,v in worst.items() if max(v.values())>LIMITS.get(k,0.03)}
    moved={k:v for k,v in worst.items() if max(v.values())-min(v.values())>0.003}
    hidden={k:v for k,v in least.items() if v<0.002}
    check('face parts, glasses and wristbands stay within 3 cm of the skin, hats within 7 cm and hair within 10 cm',worst and len(worst)>=7 and not over,over or worst)
    check('every part stands at least 2 mm proud of the skin, so it shows (glasses and eyes used to sit inside the face)',worst and not hidden,hidden)
    check('every part follows its bone through five poses',worst and not moved,moved)
    # ---- Stickers: the unlock card and the milestone post show the avatar.
    api(op,'flags',{'flags':{'demos':False}},'PUT')
    nav('#/practice')
    js("const s=document.querySelector('[data-pick=focus-add]');if(s){s.value='F7';s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()}");idle()
    js("const f=document.querySelector('#log-form');f.minutes.value='20';f.requestSubmit()")
    check('unlocking First steps shows the avatar sticker on the card',lambda: js("return !document.querySelector('#unlock').hidden&&document.querySelector('#unlock').textContent.includes('First steps')&&!!document.querySelector('#unlock img.unlock-sticker')"),timeout=20)
    shot_if('avatar-unlock')
    nav('#/practice');nav('#/timeline')
    check('the milestone post shows the avatar sticker',lambda: js("const p=[...document.querySelectorAll('.post')].find(p=>p.querySelector('.ms')&&p.textContent.includes('First steps'));return !!p&&!!p.querySelector('img.ms-sticker')&&p.querySelector('img.ms-sticker').src.startsWith('data:image/png')"),timeout=20)
    # ---- Streak overlay: every template, with and without the avatar.
    nav('#/');js("document.querySelector('[data-act=share-streak]')?.click()");idle()
    overlays=lambda: js("""const out=[];for(const t of ['classic','minimal','card','bold','week']){const h={};for(const a of ['none','beside','replace']){const c=streakCanvas({format:'story',template:t,place:'bottom',avatar:a}),x=c.getContext('2d'),d=x.getImageData(0,0,c.width,c.height).data;let s=0;for(let i=0;i<d.length;i+=4*97)s=(s*31+d[i]+d[i+3])>>>0;h[a]=[s,d[3]]}out.push([t,h.none[0]!==h.beside[0]&&h.none[0]!==h.replace[0]&&h.beside[0]!==h.replace[0],h.none[1]===0&&h.beside[1]===0&&h.replace[1]===0])}return out""")
    check('every streak overlay template draws differently with the avatar beside or in place of the flame, and stays transparent',lambda: (lambda res: res and all(r[1] and r[2] for r in res))(overlays()),overlays)
    check('the share dialog offers the avatar',js("return document.querySelectorAll('[data-act=share-av]').length")==3)
    js("document.querySelector('[data-act=share-av][data-v=beside]')?.click()");idle()
    shot_if('avatar-overlay')
finally:
    done()
