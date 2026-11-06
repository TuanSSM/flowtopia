# Every skill with a demo draws; clips move and pictures hold still; captions and controls fit the kind of clip.
import sys,os,json
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
index=json.load(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'..','..','web','clips','index.json')))
SNAP="const c=document.querySelector('#demo-cv');if(!c)return null;const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let h=0,set=new Set();for(let i=0;i<d.length;i+=4*41){h=(h*31+d[i]+d[i+1]*3+d[i+2]*7)>>>0;set.add(d[i]<<16|d[i+1]<<8|d[i+2])}return [h,set.size]"
text=lambda: js("return document.querySelector('.demo').textContent")
def ready():
    for _ in range(16):
        if js("return !!window.Demo.clip()"): return
        time.sleep(.25)
try:
    admin();start()
    for _ in range(40):
        if js("return Object.keys(window.Demo.index()).length")>0: break
        time.sleep(.5)
    check('the list of demos loads',js("return Object.keys(window.Demo.index()).length")==len(index),len(index))
    bad=[];stills=0
    for key,e in index.items():
        prop,sid=key.split(':');nav(f'#/prop/{prop}/{sid[0]}/{sid}',0.8);ready()
        a=js(SNAP);got=js("return [...document.querySelectorAll('.demo-var .chip')].map(c=>c.textContent)");time.sleep(.4);b=js(SNAP)
        if a and b and a[0]==b[0]: time.sleep(.37);b=js(SNAP)
        pic=e['v'][0]['m']=='still';stills+=pic;names=[v['n'] for v in e['v']]
        if not a or a[1]<4 or ((a[0]!=b[0])==pic) or (len(names)>1 and got!=names): bad.append((key,a,got[:2]))
    check(f'all {len(index)} skills with demos draw; clips move and the {stills} picture skills hold still',not bad,bad[:6])
    missing=js("const ix=window.Demo.index();return Promise.all(Object.values(ix).flatMap(e=>e.v.map(v=>fetch('clips/'+v.f+'.json').then(r=>r.ok?r.json():null).then(c=>c&&c.n>0&&c.props?null:v.f)))).then(a=>a.filter(Boolean))")
    check('every variant has a clip file',not missing,missing[:5])
    nav('#/prop/nunchaku/F/F1',1.5);ready()
    check('grip skills show a labelled picture, with no play or GIF buttons','A still picture' in text() and js("return !document.querySelector('[data-k=play]')&&!document.querySelector('[data-k=gif]')"))
    nav('#/prop/poi/L/L2',1.5);check('solved poi demos say so','Solved: the head follows the path' in text())
    nav('#/prop/nunchaku/F/F5',1.5);ready();check('single-prop demo has a hand switch',js("return !!document.querySelector('[data-act=demo][data-k=hand]')"))
    for _ in range(80):
        if js("return !!(window.Figure3D&&(window.Figure3D.ready||window.Figure3D.failed))"): break
        time.sleep(.5)
    solid=js("const D=window.Demo,c=D.clip(),cv=document.createElement('canvas');cv.width=cv.height=360;const x=cv.getContext('2d');let n=0;const o=x.stroke.bind(x);x.stroke=()=>{n++;o()};D.draw(x,360,c,0.2,'angle',false,D.colors(),false);const d=x.getImageData(0,0,360,360).data;const set=new Set();for(let i=0;i<d.length;i+=4*7)set.add(d[i]>>3<<10|d[i+1]>>3<<5|d[i+2]>>3);return [window.Figure3D.ready,n,set.size]")
    check('the 3D model loads and draws a shaded figure (many tones, few outline strokes)',solid[0] and solid[2]>60 and solid[1]<60,solid)
    js("window.Demo.figure(false)")
    flat=js("const D=window.Demo,c=D.clip(),cv=document.createElement('canvas');cv.width=cv.height=360;const x=cv.getContext('2d');let n=0;const o=x.stroke.bind(x);x.stroke=()=>{n++;o()};D.draw(x,360,c,0.2,'angle',false,D.colors(),false);return n")
    check('with the 3D figure off, the drawn figure with fingers and thumbs is used',flat>60,flat)
    js("window.Demo.figure(true)")
    nav('#/prop/poi/B/B1',1.5);check('two-prop demo has no hand switch',js("return !document.querySelector('[data-act=demo][data-k=hand]')"))
    nav('#/prop/staff/M/M1',2);ready()
    b64=js("const D=window.Demo,g=D.gif(D.clip(),'angle',false,D.colors());return [g.length,String.fromCharCode(g[0],g[1],g[2])]")
    check('GIF export works',b64[1]=='GIF' and b64[0]>20000,b64)
    nav('#/prop/nunchaku/S/S12',1);check('the corrected description shows',"around the opposite wrist" in js("return document.querySelector('#panel').textContent"))
finally:
    done()
