# Screenshots of the main pages for a pull request: pages.py <out dir>
# Signed in as the test admin with nunchaku chosen, a sample combo, and a few skills marked.
# Writes <name>.png per page at desktop and phone width, a demo contact sheet, and index.json
# ([{file, title}]) in the order to show them.
import sys,os,io,json
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
from PIL import Image
out=sys.argv[1];os.makedirs(out,exist_ok=True);made=[]
def page(name,title,h,w=1440,ht=1000,wait=1.8,full=True):
    cmd('WebDriver:SetWindowRect',width=w,height=ht);nav(h,wait)
    img=Image.open(io.BytesIO(base64.b64decode(cmd('WebDriver:TakeScreenshot',full=full)['value']))).convert('RGB')
    if img.height>2600: img=img.crop((0,0,img.width,2600))
    img.save(f'{out}/{name}.png',optimize=True);made.append({'file':name+'.png','title':title});check(f'{name} ({img.width}x{img.height})',True)
def add(sid):
    js("const s=document.querySelector('[data-change=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}))",sid);time.sleep(1)
try:
    op=admin();start();signin()
    if js("return !!document.querySelector('input[name=prop]')"): js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()");time.sleep(3)
    # Some progress so the dashboard and map are not empty.
    for sid,v in [('F1',4),('F2',4),('F3',3),('F4',2)]:
        nav(f'#/prop/nunchaku/F/{sid}',1);js("for(const b of document.querySelectorAll(`[data-act=level][data-id=${arguments[0]}][data-v='${arguments[1]}']`))b.click()",sid,v);time.sleep(.4)
    nav('#/combos',2);js("const f=document.querySelector('#combo-form');f.name.value='Sample combo';f.requestSubmit()");time.sleep(1.2)
    for sid in ['F5','F7','R1','D4']: add(sid)
    time.sleep(1.5)
    page('dashboard','Dashboard','#/')
    page('map','Skill map','#/maps')
    page('skill','Skill guide with demo (F5)','#/prop/nunchaku/F/F5',wait=2.5)
    page('combos','Combos','#/combos',wait=2.5)
    page('practice','Practice','#/practice')
    page('plan','Plan','#/plan')
    # Community: a post with a tagged skill, then the timeline, achievements and the public profile.
    nav('#/timeline',2);js("const f=document.querySelector('#compose-form');f.body.value='Clean F4 today, finally. On to the figure-8 chain.';f.skill.value='F4';f.requestSubmit()");time.sleep(2)
    page('timeline','Timeline','#/timeline',wait=2.5)
    page('profile-public','Public profile','#/u/admin',wait=2.5)
    page('timeline-phone','Timeline, phone','#/timeline',w=420,ht=900,wait=2.5)
    page('map-phone','Skill map, phone','#/maps',w=420,ht=900)
    page('combos-phone','Combos, phone','#/combos',w=420,ht=900,wait=2.5)
    page('skill-phone','Skill guide, phone','#/prop/nunchaku/F/F5',w=420,ht=900,wait=2.5)
    # A contact sheet of demos, as shots.py makes it.
    cmd('WebDriver:SetWindowRect',width=1440,height=1000)
    for _ in range(40):
        if js("return !!(window.Figure3D&&(window.Figure3D.ready||window.Figure3D.failed))"): break
        time.sleep(.5)
    fig=js("return window.Figure3D?(window.Figure3D.ready?'3D figure':'drawn figure (no WebGL here)'):'drawn figure'")
    SZ=320;tiles=[]
    picks=[('nunchaku','F5','0.15'),('nunchaku','F7','0.4'),('nunchaku','R1','0.3'),('nunchaku','D4','0.6'),('poi','F2','0.2'),('poi','L2','0.5'),('staff','F2','0.3'),('doublestaff','T1','0.3'),('buugeng','F3','0.4')]
    for prop,sid,ph in picks:
        nav(f'#/prop/{prop}/{sid[0]}/{sid}',1.2)
        js("const D=window.Demo;D.state.playing=false;D.state.phase=Number(arguments[0]);D.mount()",ph);time.sleep(.6);js("window.Demo.mount()");time.sleep(.3)
        b=js("const c=document.querySelector('#demo-cv');return c?c.toDataURL('image/png').split(',')[1]:null")
        if b: tiles.append(Image.open(io.BytesIO(base64.b64decode(b))).convert('RGB').resize((SZ,SZ)))
    cols=3;rows=(len(tiles)+cols-1)//cols;sheet=Image.new('RGB',(cols*SZ,max(1,rows)*SZ),(30,30,30))
    for i,t in enumerate(tiles): sheet.paste(t,((i%cols)*SZ,(i//cols)*SZ))
    sheet.save(f'{out}/demos.png',optimize=True);made.append({'file':'demos.png','title':f'Demos ({len(tiles)} of them, {fig})'});check(f'demo sheet ({len(tiles)} tiles, {fig})',len(tiles)>0,len(tiles))
    json.dump(made,open(f'{out}/index.json','w'))
finally:
    done()
