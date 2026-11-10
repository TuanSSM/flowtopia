# Every skill with a demo draws; clips move and pictures hold still; captions and controls fit the kind of clip.
import sys,os,json
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
index=json.load(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'..','..','web','clips','index.json')))
# SNAP (h.py) reads the demo's canvas as a number that changes when the picture does, and its count of colours.
text=lambda: js("return document.querySelector('.demo').textContent")
def ready(): poll(lambda: js("return !!window.Demo.clip()"),4)  # a skill whose clip never comes fails the check that looks at it
try:
    admin();start()
    poll(lambda: js("return Object.keys(window.Demo.index()).length")>0,20)
    check('the list of demos loads',js("return Object.keys(window.Demo.index()).length")==len(index),len(index))
    bad=[];stills=0
    for key,e in index.items():
        prop,sid=key.split(':');nav(f'#/prop/{prop}/{sid[0]}/{sid}');ready()
        a=js(SNAP);got=js("return [...document.querySelectorAll('.demo-var .chip')].map(c=>c.textContent)")
        pic=e['v'][0]['m']=='still';stills+=pic;names=[v['n'] for v in e['v']]
        if pic: time.sleep(.4);b=js(SNAP)  # fixed: a picture counts as still only if it has not changed after a while
        else: b=poll(lambda: (lambda s: s if a and s and s[0]!=a[0] else None)(js(SNAP)),2)[0] or a  # a clip moves: wait for another frame
        if not a or a[1]<4 or ((a[0]!=b[0])==pic) or (len(names)>1 and got!=names): bad.append((key,a,got[:2]))
    check(f'all {len(index)} skills with demos draw; clips move and the {stills} picture skills hold still',not bad,bad[:6])
    missing=js("const ix=window.Demo.index();return Promise.all(Object.values(ix).flatMap(e=>e.v.map(v=>fetch('clips/'+v.f+'.json').then(r=>r.ok?r.json():null).then(c=>c&&c.n>0&&c.props?null:v.f)))).then(a=>a.filter(Boolean))")
    check('every variant has a clip file',not missing,missing[:5])
    nav('#/prop/nunchaku/F/F1');ready()
    check('grip skills show a labelled picture, with no play or GIF buttons','A still picture' in text() and js("return !document.querySelector('[data-k=play]')&&!document.querySelector('[data-k=gif]')"))
    nav('#/prop/poi/L/L2');check('solved poi demos say so','Solved: the head follows the path' in text())
    nav('#/prop/nunchaku/F/F5');ready();check('single-prop demo has a hand switch',js("return !!document.querySelector('[data-act=demo][data-k=hand]')"))
    figure(40)
    solid=js("const D=window.Demo,c=D.clip(),cv=document.createElement('canvas');cv.width=cv.height=360;const x=cv.getContext('2d');let n=0;const o=x.stroke.bind(x);x.stroke=()=>{n++;o()};D.draw(x,360,c,0.2,'angle',false,D.colors(),false);const d=x.getImageData(0,0,360,360).data;const set=new Set();for(let i=0;i<d.length;i+=4*7)set.add(d[i]>>3<<10|d[i+1]>>3<<5|d[i+2]>>3);return [window.Figure3D.ready,n,set.size]")
    webgl=js("const c=document.createElement('canvas');return !!(c.getContext('webgl2')||c.getContext('webgl'))")
    if webgl: check('the 3D model loads and draws a shaded figure (many tones, few outline strokes)',solid[0] and solid[2]>60 and solid[1]<60,[solid,js("return window.Figure3D&&window.Figure3D.reason")])
    else: print('SKIP the 3D model: this browser has no WebGL, so only the drawn figure can be tested',flush=True)
    js("window.Demo.figure(false)")
    flat=js("const D=window.Demo,c=D.clip(),cv=document.createElement('canvas');cv.width=cv.height=360;const x=cv.getContext('2d');let n=0;const o=x.stroke.bind(x);x.stroke=()=>{n++;o()};D.draw(x,360,c,0.2,'angle',false,D.colors(),false);return n")
    check('with the 3D figure off, the drawn figure with fingers and thumbs is used',flat>60,flat)
    js("window.Demo.figure(true)")
    if webgl and solid[0]:
        # A grip picture has no body of its own; the model's hand and forearm stand in, cut off behind the hand.
        nav('#/prop/nunchaku/F/F1');ready()
        pic=js("const D=window.Demo,c=D.clip(),cv=document.createElement('canvas');cv.width=cv.height=360;const x=cv.getContext('2d');D.draw(x,360,c,0,'angle',false,D.colors(),false);const d=x.getImageData(0,0,360,360).data;const set=new Set();let body=0;for(let i=0;i<d.length;i+=4*7){set.add(d[i]>>3<<10|d[i+1]>>3<<5|d[i+2]>>3);if(d[i]>120&&Math.abs(d[i]-d[i+2])<25)body++}return [!!c.hide,set.size,body]")
        check('a close-up grip picture shows the model hand (shaded, grey, not a drawn fist)',pic[0] and pic[1]>60 and pic[2]>300,pic)
    nav('#/prop/poi/B/B1');check('two-prop demo has no hand switch',js("return !document.querySelector('[data-act=demo][data-k=hand]')"))
    nav('#/prop/staff/M/M1');ready()
    b64=js("const D=window.Demo,g=D.gif(D.clip(),'angle',false,D.colors());return [g.length,String.fromCharCode(g[0],g[1],g[2])]")
    check('GIF export works',b64[1]=='GIF' and b64[0]>20000,b64)
    nav('#/prop/nunchaku/S/S12');check('the corrected description shows',"around the opposite wrist" in js("return document.querySelector('#panel').textContent"))
finally:
    done()
