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
def wait(fn,n=40,dt=.5):
    for _ in range(n):
        v=fn()
        if v: return v
        time.sleep(dt)
    return fn()
def account(name,onboard=True):
    o=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(o,'signup',{'username':name,'password':'flowa-test-only'})
    if onboard: api(o,'me',{'props':['nunchaku']},'PUT')
    return o
def signin_as(name):
    js("return api('signout','POST').then(r=>r.ok)")
    cmd('WebDriver:Navigate',url=BASE+f'?as={name}#/signin') # a new query string forces a full load
    if not wait(lambda: js("return !!document.querySelector('#auth-form')")): print('no sign-in form:',js("return location.href+' '+document.querySelector('#main')?.textContent.slice(0,200)"))
    js("const f=document.querySelector('#auth-form');f.elements.username.value=arguments[0];f.elements.password.value='flowa-test-only';f.requestSubmit()",name)
    wait(lambda: js("return !!document.querySelector('.nav-profile, #props-form')"));time.sleep(1.2)
def my_cfg(): return js("return fetch('api/me').then(r=>r.json()).then(d=>JSON.stringify(d.user.avatarCfg||{}))")
def snap(sel): return js("const c=document.querySelector(arguments[0]);if(!c)return null;const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let h=0;for(let i=0;i<d.length;i+=4*7)h=(h*31+d[i]+d[i+1]*3+d[i+2]*7)>>>0;return h",sel)
def shot_if(name):
    if SHOTS: os.makedirs(SHOTS,exist_ok=True);shot(f'{SHOTS}/{name}.png')
n_reload=[0]
def reload(h):
    # A full load, so the page reads the flags and the member's avatar again.
    # Unsaved progress would hold the page with a prompt, so wait for it to be saved first.
    wait(lambda: 'All changes saved' in (js("return document.body.textContent") or ''),n=20)
    n_reload[0]+=1;cmd('WebDriver:Navigate',url=BASE+f'?r={n_reload[0]}'+h);time.sleep(1)
    wait(lambda: js("return document.readyState==='complete'&&!!window.Demo"))
def figure_ready(): return wait(lambda: js("return !!(window.Figure3D&&window.Figure3D.ready)"),n=60)
HEAVY={'v':1,'body':'tall','build':'broad','frame':'feminine','skin':'#5b3422','hair':'curly','hairColor':'#d6b47a','eyes':'wide','brows':'thick','mouth':'grin','top':'hoodie','topColor':'#c4572e','bottoms':'trousers','bottomsColor':'#1f2a44','shoes':'boots','shoesColor':'#2b2b2b','hat':'bucket','hatColor':'#2f6f4f','glasses':'square','wristbands':'yes','wristColor':'#d9a441','props':{'nunchaku':'#e0533a'}}
try:
    op=admin();api(op,'me',{'props':['nunchaku']},'PUT')
    api(op,'flags',{'flags':{'avatars':True,'community':True,'game':True,'model3d':True}},'PUT')
    start()
    # ---- First run: after picking props a new member is offered the editor, and can skip or keep.
    account('newbie',onboard=False);signin_as('newbie')
    js("const f=document.querySelector('#props-form');f.querySelector('input[value=nunchaku]').checked=true;f.requestSubmit()")
    wait(lambda: js("return !!document.querySelector('.av-first')"))
    first=js("return window.AV.draft&&JSON.stringify(window.AV.draft)") or '{}'
    check('after choosing props a new member is offered the editor',js("return location.hash")=='#/avatar/first' and js("return !!document.querySelector('.av-first')"))
    check('the editor starts from a random complete avatar',json.loads(first).get('v')==1 and len(json.loads(first))>=20,first)
    click('[data-act=av-skip]');time.sleep(1.5)
    check('skipping goes to the dashboard and saves nothing',js("return location.hash") in ('#/','') and my_cfg()=='{}',my_cfg())
    account('keeper',onboard=False);signin_as('keeper')
    js("const f=document.querySelector('#props-form');f.querySelector('input[value=nunchaku]').checked=true;f.requestSubmit()")
    wait(lambda: js("return !!document.querySelector('.av-first')"))
    draft=js("return JSON.stringify(window.AV.draft)")
    click('.av-first [data-act=av-save]');wait(lambda: my_cfg()!='{}')
    check('keeping the offered avatar saves it',json.loads(my_cfg())==json.loads(draft),(my_cfg(),draft))
    # ---- The editor.
    nav('#/avatar',2);figure_ready();time.sleep(1.5)
    shot_if('avatar-editor')
    a=snap('#av-cv');click('[data-act=av-tab][data-v=hair]');time.sleep(.4)
    cur=json.loads(js("return JSON.stringify(window.AV.draft)"))
    other=[v for v in ['curly','bun','long','buzz'] if v!=cur['hair']][0]
    click(f'[data-act=av-set][data-k=hair][data-v={other}]');time.sleep(1)
    b=snap('#av-cv')
    click('[data-act=av-tab][data-v=face]');time.sleep(.4)
    tone=[t for t in ['#2f1b12','#f6dccb'] if t!=cur['skin']][0]
    click(f'[data-act=av-set][data-k=skin][data-v="{tone}"]');time.sleep(1)
    c=snap('#av-cv')
    check('changing hair and then skin tone changes the preview',a and b and c and a!=b and b!=c,(a,b,c))
    # Frames: the feminine and masculine frames each draw a different figure from the neutral one.
    click('[data-act=av-tab][data-v=body]');time.sleep(.4)
    fr={}
    for f in ['neutral','feminine','masculine']: click(f'[data-act=av-set][data-k=frame][data-v={f}]');time.sleep(1.2);fr[f]=snap('#av-cv')
    check('the neutral, feminine and masculine frames each draw a different figure',all(fr.values()) and len(set(fr.values()))==3 and js("return window.Figure3D.body().frame")=='masculine',js("return window.Figure3D.body()"))
    # Turning: a drag (pointer events) turns the figure.
    y0=js("return window.Demo.previewState.yaw")
    js("const c=document.querySelector('#av-cv'),r=c.getBoundingClientRect(),o={bubbles:true,pointerId:1,clientY:r.top+50};c.dispatchEvent(new PointerEvent('pointerdown',{...o,clientX:r.left+50}));c.dispatchEvent(new PointerEvent('pointermove',{...o,clientX:r.left+150}));c.dispatchEvent(new PointerEvent('pointerup',{...o,clientX:r.left+150}))")
    check('dragging the preview turns it',abs(js("return window.Demo.previewState.yaw")-y0)>0.5)
    before=js("return JSON.stringify(window.AV.draft)")
    click('[data-act=av-random]');time.sleep(.6)
    rnd=js("return JSON.stringify(window.AV.draft)")
    click('[data-act=av-undo]');time.sleep(.6)
    check('Surprise me changes the avatar and Undo brings back the previous one exactly',rnd!=before and js("return JSON.stringify(window.AV.draft)")==before)
    js("document.querySelector('#av-headshot').checked=true;document.querySelector('#av-headshot').dispatchEvent(new Event('change',{bubbles:true}))")
    click('.av-panel [data-act=av-save]');wait(lambda: json.loads(my_cfg())==json.loads(before))
    reload('#/avatar');wait(lambda: js("return !!(window.AV&&window.AV.draft)"))
    check('the saved avatar is there after a reload',json.loads(js("return JSON.stringify(window.AV.draft||{})"))==json.loads(before) and json.loads(my_cfg())==json.loads(before))
    # ---- Headshot as profile picture: in the menu, on posts and on comments.
    pic=js("return document.querySelector('.nav-profile img.avatar')?.getAttribute('src')||''")
    check('the headshot is the profile picture in the menu',pic.startswith('data:image/jpeg'),pic[:30])
    nav('#/timeline',2.5)
    js("const t=document.querySelector('#compose-form textarea');t.value='My avatar is here';t.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('#compose-form').requestSubmit()");time.sleep(2)
    pid=wait(lambda: js("return document.querySelector('.post')?.dataset.post"))
    js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-form');if(f){f.body.value='And here';f.requestSubmit()}",pid);time.sleep(2)
    # Others see the picture by URL (lists and feeds do not carry it inline); it must load.
    time.sleep(1)
    check('the headshot shows on the post and on the comment',js("const p=document.querySelector('.post[data-post=\"'+arguments[0]+'\"]'),ok=i=>!!i&&i.src.includes('/api/users/')&&i.complete&&i.naturalWidth>0;return !!p&&ok(p.querySelector('header img.avatar'))&&[...p.querySelectorAll('.cmt img.avatar')].some(ok)",pid))
    # ---- Someone else's avatar plays a skill they have clean, on their profile.
    bea=account('bea');api(bea,'me',{'avatarCfg':HEAVY},'PUT')
    api(bea,'progress/nunchaku',{'rev':0,'state':{'skills':{'F5':{'O':2},'F3':{'O':1}}}},'PUT')
    nav('#/u/bea',3);figure_ready();time.sleep(1.5)
    worn=js("return JSON.stringify(window.Figure3D.avatar())")
    check("on someone's profile their avatar plays a skill they have clean",js("return document.querySelector('#av-other')?.dataset.key")=='nunchaku:F5' and json.loads(worn or 'null')==HEAVY,(js("return document.querySelector('#av-other')?.dataset.key"),worn))
    shot_if('avatar-profile')
    # ---- Demos play on the member's avatar and body preset; off with the flag.
    api(op,'flags',{'flags':{'demos':True}},'PUT')
    js("return fetch('api/me',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({avatarCfg:arguments[0]})}).then(r=>r.ok)",HEAVY)
    reload('#/prop/nunchaku/F/F5');time.sleep(3);figure_ready();time.sleep(2)
    worn=js("return JSON.stringify(window.Figure3D.avatar())")
    body=js("return [window.Demo.body.sc,window.Demo.body.bd]")
    note=js("return document.querySelector('.demo-body')?.textContent||''")
    fail=js("return fetch('clips/presets.json').then(r=>r.json()).then(p=>{const f=window.Demo.clip()?.f;return f&&p.fail[f]?p.fail[f]['tall-broad']||'':''})")
    check("a demo plays in the member's avatar",json.loads(worn or 'null')==HEAVY,worn)
    check('on their body preset when the clip passes there, and on the standard body with a note when it does not',(body==[1.035,1.1] and not note) if not fail else (body==[1,1] and 'standard body' in note and fail in note),(body,note,fail))
    shot_if('avatar-demo')
    api(op,'flags',{'flags':{'avatars':False}},'PUT')
    reload('#/prop/nunchaku/F/F5');time.sleep(3);figure_ready();time.sleep(1.5)
    check('with avatars off demos use the standard figure and body',js("return window.Figure3D.avatar()") is None and js("return [window.Demo.body.sc,window.Demo.body.bd]")==[1,1])
    api(op,'flags',{'flags':{'avatars':True}},'PUT')
    reload('#/prop/nunchaku/F/F5');time.sleep(3);figure_ready();time.sleep(1)
    # ---- Frame budget: drawing one demo frame, on the standard figure and with the heaviest avatar.
    FRAME="""const D=window.Demo,c=D.clip();if(!c)return null;const cv=document.createElement('canvas');cv.width=cv.height=720;const x=cv.getContext('2d'),col=D.colors(),who=arguments[0];
      D.draw(x,720,c,0,'angle',false,col,false,who);const t=[];for(let i=0;i<30;i++){const t0=performance.now();D.draw(x,720,c,i/30,'angle',false,col,false,who);t.push(performance.now()-t0)}t.sort((a,b)=>a-b);return Math.round(t[15]*10)/10"""
    base=js(FRAME,None);ms=js(FRAME,HEAVY)
    print(f'demo frame, median of 30: standard figure {base} ms, heaviest avatar {ms} ms')
    check(f'one demo frame with the heaviest avatar draws within {BUDGET:.0f} ms ({ms} ms; the standard figure takes {base} ms)',ms is not None and ms<=BUDGET,ms)
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
    nav('#/practice',2)
    js("const s=document.querySelector('[data-change=focus-add]');if(s){s.value='F7';s.dispatchEvent(new Event('change',{bubbles:true}))}");time.sleep(.6)
    js("const f=document.querySelector('#log-form');f.minutes.value='20';f.requestSubmit()")
    card=wait(lambda: js("return !document.querySelector('#unlock').hidden&&document.querySelector('#unlock').textContent.includes('First steps')&&!!document.querySelector('#unlock img.unlock-sticker')"),n=20)
    check('unlocking First steps shows the avatar sticker on the card',card)
    shot_if('avatar-unlock')
    nav('#/practice',1);nav('#/timeline',3)
    post=wait(lambda: js("const p=[...document.querySelectorAll('.post')].find(p=>p.querySelector('.ms')&&p.textContent.includes('First steps'));return !!p&&!!p.querySelector('img.ms-sticker')&&p.querySelector('img.ms-sticker').src.startsWith('data:image/png')"),n=20)
    check('the milestone post shows the avatar sticker',post)
    # ---- Streak overlay: every template, with and without the avatar.
    nav('#/',2);js("document.querySelector('[data-act=share-streak]')?.click()");time.sleep(1.5)
    res=js("""const out=[];for(const t of ['classic','minimal','card','bold','week']){const h={};for(const a of ['none','beside','replace']){const c=streakCanvas({format:'story',template:t,place:'bottom',avatar:a}),x=c.getContext('2d'),d=x.getImageData(0,0,c.width,c.height).data;let s=0;for(let i=0;i<d.length;i+=4*97)s=(s*31+d[i]+d[i+3])>>>0;h[a]=[s,d[3]]}out.push([t,h.none[0]!==h.beside[0]&&h.none[0]!==h.replace[0]&&h.beside[0]!==h.replace[0],h.none[1]===0&&h.beside[1]===0&&h.replace[1]===0])}return out""")
    check('every streak overlay template draws differently with the avatar beside or in place of the flame, and stays transparent',res and all(r[1] and r[2] for r in res),res)
    check('the share dialog offers the avatar',js("return document.querySelectorAll('[data-act=share-av]').length")==3)
    js("document.querySelector('[data-act=share-av][data-v=beside]')?.click()");time.sleep(1.5)
    shot_if('avatar-overlay')
finally:
    done()
