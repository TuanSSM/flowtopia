# Keyboard: Flowa worked with the keys alone. One person signs in, logs a practice session, builds a combo and
# publishes it, and replies to a comment, without a mouse; each step is read back through the API.
# On the way, at each page and in each dialog:
#   - Tab is pressed until the focus is back where it began, and the stops are held against the controls the
#     page shows (the list a11y.py names): every one is reached, nothing hidden, inert, outside the window or
#     under something else takes the focus, and no control asks for a place of its own with a positive tabindex.
#   - The order is reading order: no control is focused after one that lies more than a row (24 px) below it,
#     unless it starts to the right of that one's right edge, as the next column of a layout does.
#   - Each control that has the focus shows it: an outline of at least 2 px that differs by 3:1 from what it
#     is drawn on (WCAG 2.2, 2.4.7 and 1.4.11), in the dark and in the light theme, and that no box cuts off.
#   - A dialog takes the focus when it opens, keeps Tab and Shift+Tab inside, closes on Escape, and hands the
#     focus back to the control that opened it; the page behind it cannot be reached meanwhile.
#   - A control that was used still has the focus when the page has been drawn again; one that deleted or
#     removed something gives it to a control that deletes nothing.
#   - For a person who uses the pointer, nothing of this moves the focus or scrolls the page, and a playing
#     combo costs the page no work for it.
# The browser is sent real key presses (h.py: key, type_text, tab_to) and moves the focus itself; the checks
# about the pointer send it real clicks (h.py: point). The page's own JS is asked where the focus is and what
# is drawn there; it is never told to focus or to click. Set from outside, and only to look: the theme, for
# the moment a ring is read in the other one; a listener that keeps a file chooser of the system from opening
# when its button is pressed; one that keeps a click from focusing a button, as Safari does; the answer yes to
# the browser's own question before a comment is deleted (it is its own dialog, not the page's); a redraw at a
# known time, which a reply from the server would cause at an unknown one; and a count of the page's watcher.
# Seen, counted and left: beside a wide page a skill's guide lies over the right 420 px of it, so the bell, the
# search button and what else lies there take the focus under the guide, as they lie under it for a mouse.
# KEYS_LIST=1 prints every stop of every round, to work through.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
def get(o,path): return json.loads(o.open(BASE+'api/'+path,timeout=20).read())
ROW=24      # px: how far above an earlier stop a later one may begin before the order counts as broken
RING=2      # px: the thinnest ring that counts
NEED=3      # the contrast a ring needs against what it is drawn on
# What counts as a control: the list a11y.py asks a name of.
SEL='a[href],button,summary,label.btn,input,select,textarea,[contenteditable]:not([contenteditable=false]),[data-act],[tabindex]:not([tabindex="-1"]),[role=button],[role=tab],[role=link],[role=menuitem],[role=switch],[role=checkbox],[role=radio],[role=option]'
# The colour arithmetic is the one of contrast.py, copied: a colour as the browser writes it, one colour over
# another, relative luminance, the contrast of two colours, and what lies behind an element (its ancestors'
# backgrounds laid under each other up to the first that is opaque; a picture, a gradient or a canvas on the
# way ends the walk without an answer).
LIB=r"""
const SEL="""+json.dumps(SEL)+r""";
const K=window.__kb||(window.__kb={m:new WeakMap(),els:new Map(),n:0});
const idOf=e=>{let i=K.m.get(e);if(!i){i=++K.n;K.m.set(e,i);K.els.set(i,e)}return i};
const sq=s=>(s||'').replace(/\s+/g,' ').trim();
const cls=e=>e.tagName.toLowerCase()+(e.id?'#'+e.id:'')+(typeof e.className==='string'&&e.className.trim()?'.'+e.className.trim().split(/\s+/).join('.'):'');
const what=e=>{const t=sq(e.getAttribute('aria-label')||e.textContent||e.getAttribute('placeholder')).slice(0,28);
  return cls(e)+(e.dataset.act?'[act='+e.dataset.act+']':'')+(e.dataset.change?'[change='+e.dataset.change+']':'')+(e.getAttribute('type')?'[type='+e.getAttribute('type')+']':'')+(e.getAttribute('name')?'[name='+e.getAttribute('name')+']':'')+(t?' "'+t+'"':'')};
// What a control is, in words that stay the same when the page draws it again with another count or label.
const same=e=>[e.tagName,e.id,e.getAttribute('href')||'',e.getAttribute('name')||'',e.getAttribute('type')||'',e.getAttribute('value')||'',JSON.stringify(Object.assign({},e.dataset,{tip:''}))].join('|');
const shown=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden'&&!e.closest('[hidden],[inert],[aria-hidden=true]');
const P=s=>{s=(s||'').trim();let m;
  const pc=(x,one)=>x.endsWith('%')?parseFloat(x)/100*one:parseFloat(x),al=x=>x==null?1:pc(x,1);
  if(s==='transparent')return [0,0,0,0];
  if(m=/^#([0-9a-f]{3,8})$/i.exec(s)){let h=m[1];if(h.length<5)h=[...h].map(c=>c+c).join('');const n=i=>parseInt(h.slice(i,i+2),16);return [n(0),n(2),n(4),h.length>6?n(6)/255:1]}
  if(m=/^rgba?\(([^()]*)\)$/i.exec(s)){const v=m[1].split(/[\s,\/]+/).filter(Boolean);if(v.length<3)return null;return [pc(v[0],255),pc(v[1],255),pc(v[2],255),al(v[3])]}
  if(m=/^color\(srgb\s+([^()]*)\)$/i.exec(s)){const v=m[1].split(/[\s\/]+/).filter(Boolean);if(v.length<3)return null;return [pc(v[0],1)*255,pc(v[1],1)*255,pc(v[2],1)*255,al(v[3])]}
  return null};
const over=(f,b)=>{const a=f[3]+b[3]*(1-f[3]);return a?[0,1,2].map(i=>(f[i]*f[3]+b[i]*b[3]*(1-f[3]))/a).concat(a):[0,0,0,0]};
const lum=c=>{const [r,g,b]=c.slice(0,3).map(v=>{v/=255;return v<=0.04045?v/12.92:((v+0.055)/1.055)**2.4});return 0.2126*r+0.7152*g+0.0722*b};
const ratio=(a,b)=>{const x=lum(a),y=lum(b);return (Math.max(x,y)+0.05)/(Math.min(x,y)+0.05)};
const hex=c=>'#'+c.slice(0,3).map(v=>Math.round(v).toString(16).padStart(2,'0')).join('');
const media=[...document.querySelectorAll('canvas,img,video,iframe')].filter(m=>m.getClientRects().length>0).map(m=>[m,m.getBoundingClientRect()]);
const behind=(e,fg)=>{
  let T=fg,B=[0,0,0,0];const r=e.getBoundingClientRect(),x=r.left+r.width/2,y=r.top+r.height/2;
  for(let a=e;a;a=a.parentElement){
    const s=getComputedStyle(a);
    for(const [m,q] of media)if(a!==e&&a.contains(m)&&!m.contains(e)&&x>=q.left&&x<=q.right&&y>=q.top&&y<=q.bottom)return {why:'over '+cls(m)};
    if(s.backgroundImage!=='none')return {why:'on a gradient or a picture, the background of '+cls(a)};
    const c=P(s.backgroundColor);if(!c)return {why:'on a colour written in a way this check does not read: '+s.backgroundColor};
    B=over(B,c);T=over(T,c);
    const o=parseFloat(s.opacity);if(o<1){T=T.slice(0,3).concat(T[3]*o);B=B.slice(0,3).concat(B[3]*o)}
    if(B[3]>=.999)return {t:T,b:B};
  }
  return {why:'on nothing: no ancestor has a background'};
};
// Which sides of a ring that reaches `room` px beyond its control a box around it cuts away. A box that
// scrolls shows a control at its edge when the browser has just scrolled to it, so there only the two
// ends of the list count. A control that is fixed to the window is cut by nothing but the window.
const cut=(x,room)=>{
  if(room<=0)return '';
  const r=x.getBoundingClientRect();
  for(let n=x;n&&n!==document.documentElement;n=n.parentElement){
    if(getComputedStyle(n).position==='fixed')break;
    const a=n.parentElement;if(!a||a===document.documentElement)break;
    const s=getComputedStyle(a);if(s.overflowX==='visible'&&s.overflowY==='visible')continue;
    const b=a.getBoundingClientRect(),L=b.left+a.clientLeft,T=b.top+a.clientTop,R=L+a.clientWidth,B=T+a.clientHeight,side=[];
    if(s.overflowX!=='visible'){if(r.left-L<room&&a.scrollLeft<=0)side.push('left');if(R-r.right<room&&a.scrollLeft>=a.scrollWidth-a.clientWidth-1)side.push('right')}
    if(s.overflowY!=='visible'){if(r.top-T<room&&a.scrollTop<=0)side.push('top');if(B-r.bottom<room&&a.scrollTop>=a.scrollHeight-a.clientHeight-1)side.push('bottom')}
    if(side.length)return side.join(' and ')+' by '+cls(a);
  }
  return '';
};
// The ring a focused control shows: its own outline, or the outline of what stands for it when the control
// itself cannot be seen (a radio button drawn as a word shows the ring on the word, a field inside a button
// on the button). A shadow with no blur that reaches at least as far counts as a ring too.
const ring=e=>{
  const own=getComputedStyle(e),r0=e.getBoundingClientRect(),c=[];
  if(parseFloat(own.opacity)>0&&r0.width>2&&r0.height>2)c.push(e);
  if(e.matches('input,select,textarea')){if(e.nextElementSibling)c.push(e.nextElementSibling);const l=e.closest('label');if(l)c.push(l)}
  for(const x of c){
    const s=getComputedStyle(x);let w=parseFloat(s.outlineWidth)||0,col=null,off=0;
    if(s.outlineStyle!=='none'&&w>0){col=P(/^currentcolor$/i.test(s.outlineColor)?s.color:s.outlineColor);off=parseFloat(s.outlineOffset)||0}
    else{w=0;for(const sh of s.boxShadow.split(/,(?![^(]*\))/)){const m=/^(.+?)\s+(-?[\d.]+)px\s+(-?[\d.]+)px\s+([\d.]+)px\s+(-?[\d.]+)px$/.exec(sh.trim());
      if(m&&+m[2]===0&&+m[3]===0&&+m[4]===0&&+m[5]>w){w=+m[5];col=P(m[1])}}}
    if(!w||!col||col[3]===0)continue;
    const o={on:x===e?'':cls(x),w,off,color:hex(col)},out=x.parentElement?behind(x.parentElement,col):{why:'on nothing'};
    if(out.why)o.why=out.why;else{o.bg=hex(out.b);o.out=Math.round(ratio(out.t,out.b)*100)/100}
    // A ring drawn inside the edge of its control, as on a text box, also stands on the control's own fill.
    if(off<0&&!o.why){const inn=behind(x,col);if(inn.why)o.why=inn.why;else{o.inbg=hex(inn.b);o.inn=Math.round(ratio(inn.t,inn.b)*100)/100}}
    o.cut=cut(x,off+Math.min(w,"""+str(RING)+r"""));
    return o;
  }
  return null;
};
"""
# The stop the focus is on now. The ring is read twice, with the page switched to each theme for the moment
# of reading and its transitions off, so a colour on its way from one theme to the other is not read.
STOP=LIB+r"""
const e=document.activeElement,box=arguments[0]?document.querySelector(arguments[0]):null;
if(!e||e===document.body||e===document.documentElement)return {out:true};
const r=e.getBoundingClientRect(),s=getComputedStyle(e),root=document.documentElement,had=root.getAttribute('data-theme');
const st=document.createElement('style');st.textContent='*,*::before,*::after{transition:none!important}';document.head.append(st);
const rings={};for(const t of ['dark','light']){root.setAttribute('data-theme',t);rings[t]=ring(e)}
if(had===null)root.removeAttribute('data-theme');else root.setAttribute('data-theme',had);st.remove();
// Under something else: what is drawn at the middle of the part of the control that is in the window.
const under=()=>{
  const x0=Math.max(r.left,0),x1=Math.min(r.right,innerWidth),y0=Math.max(r.top,0),y1=Math.min(r.bottom,innerHeight);
  if(x1<=x0||y1<=y0)return '';
  const t=document.elementFromPoint((x0+x1)/2,(y0+y1)/2),l=e.closest('label');
  return !t||e.contains(t)||t.contains(e)||(l&&l.contains(t))?'':cls(t.closest('[id]')||t)};
return {id:idOf(e),what:what(e),same:same(e),inside:!box||box.contains(e),whole:box===e,control:e.matches(SEL),seen:e.matches(':focus-visible'),
  hidden:e.closest('[hidden]')?'inside [hidden]':e.closest('[inert]')?'inside [inert]':e.closest('[aria-hidden=true]')?'inside aria-hidden':s.visibility==='hidden'?'not visible':
    r.width<1||r.height<1?'has no size':r.right<=0||r.bottom<=0||r.left>=innerWidth||r.top>=innerHeight?'outside the window':'',
  under:under(),scrolls:!e.matches(SEL)&&/auto|scroll/.test(s.overflowX+s.overflowY)&&(e.scrollHeight>e.clientHeight||e.scrollWidth>e.clientWidth),
  ring:rings};
"""
# The controls on show, under a root: [number, what it is, why Tab may pass it by]. A control that is switched
# off takes no focus; of the radio buttons of one group Tab stops at one, and the arrow keys reach the rest.
CONTROLS=LIB+r"""
const root=document.querySelector(arguments[0]||'body'),rows=[];
for(const e of root.querySelectorAll(SEL)){
  if(e.matches('input[type=hidden]')||!shown(e))continue;
  if(!e.matches('a,button,summary,label,input,select,textarea,[contenteditable],[tabindex],[role]')&&getComputedStyle(e).pointerEvents==='none')continue;
  rows.push([idOf(e),what(e),e.matches(':disabled,[aria-disabled=true]')?'off':e.matches('input[type=radio]')?'radio:'+e.name+':'+(e.form?idOf(e.form):0):'',same(e)]);
}
return {rows,positive:[...document.querySelectorAll('[tabindex]')].filter(e=>e.tabIndex>0&&shown(e)).map(what),
  // What the page shows outside the root and has not put out of reach, for a dialog that must keep the page behind it away.
  beyond:arguments[0]?[...document.querySelectorAll(SEL)].filter(e=>!root.contains(e)&&shown(e)&&!e.matches(':disabled,input[type=hidden]')&&!e.closest('#tour-root,.react-joyride__tooltip')).length:0};
"""
# Where each stop lies on the page: [left, top, width, height], all read at one moment with the window
# scrolled to its top for that moment, so what sticks to the window (the menu, the bar at the top) is where
# it starts and everything else is where the page puts it. A stop inside a box that scrolls is taken where it
# lies with that box at its start, and no further out than the box itself: a long list is as tall as its box.
# Before them, which stop comes first in the page: a round begins wherever the focus was, and its order is
# told from the top.
PLACES=LIB+r"""
const els=arguments[0].map(i=>K.els.get(i)),top=els.reduce((a,e,i)=>a<0||(e.isConnected&&els[a].isConnected&&(els[a].compareDocumentPosition(e)&2))?i:a,-1);
const x=scrollX,y=scrollY;scrollTo({left:0,top:0,behavior:'instant'});
const place=e=>{
  if(!e||!e.isConnected)return null;const r=e.getBoundingClientRect();if(r.width<1||r.height<1)return null;let x=r.left,y=r.top;
  for(let a=e.parentElement;a&&a!==document.body&&a!==document.documentElement;a=a.parentElement){
    const s=getComputedStyle(a);if(s.overflowX==='visible'&&s.overflowY==='visible')continue;
    const b=a.getBoundingClientRect();x+=a.scrollLeft;y+=a.scrollTop;
    x=Math.min(Math.max(x,b.left),Math.max(b.left,b.right-r.width));y=Math.min(Math.max(y,b.top),Math.max(b.top,b.bottom-r.height));
    if(s.position==='fixed')break}
  return [x,y,r.width,r.height]};
const out=[top].concat(els.map(place));
scrollTo({left:x,top:y,behavior:'instant'});return out;
"""
LIST=bool(os.environ.get('KEYS_LIST'))
tot=dict(rounds=0,stops=0,controls=0,unreached=0,noring=0,unjudged=0,astray=0,covered=0,order=0,used=0,dropped=0,dialogs=0,baddialogs=0)
offenders={}   # each kind of control without a ring, once
def walk(box=None,limit=700,back=False):
    # Presses Tab (Shift+Tab with back) until the focus is back on the first control it reached. Returns the
    # stops in order, how the round ended, and how many presses left the focus on no element of the page.
    # A date field keeps the focus while Tab walks its day, month and year; more presses than that is a trap.
    seq=[];ids=set();last=None;same=gone=0;end='limit'
    for _ in range(limit):
        key(TAB,hold=(SHIFT,) if back else ());s=js(STOP,box)
        if s.get('out'):
            gone+=1;last=None
            if gone>12: end='lost';break
            continue
        if s['id']==last:
            same+=1
            if same>8: end='stuck';break
            continue
        if s['id'] in ids:
            # Back at a stop it has been on: from there to here is one whole round. It is the first stop, unless
            # the page drew that one again meanwhile (the bell is drawn again each minute, the menu with any
            # page); then the round is told from the first stop that is still the element it was.
            i=[x['id'] for x in seq].index(s['id']);seq=seq[i:];end='round';break
        ids.add(s['id']);last=s['id'];same=0;seq.append(s)
    if LIST: print('\n'.join(f'    {i:3} {s["what"]}' for i,s in enumerate(seq)),flush=True)
    return seq,end,gone
ENDS={'round':'','limit':'the focus did not come back to where it began within the limit','lost':'the focus left the page and did not come back','stuck':'the focus is stuck'}
def mirror(seq,rev):
    # True when the round back is the round there, the other way. Both are rounds, so they may begin at
    # different stops: one begins anew when the page draws its first stop again while it is on its way.
    a=[s['same'] for s in reversed(seq)];b=[s['same'] for s in rev]
    return len(a)==len(b) and (not a or any(a[i:]+a[:i]==b for i in range(len(a))))
def rings(name,seq):
    # Each stop shows a ring in each theme. Left unjudged, and counted: a ring over a picture, a gradient or a canvas.
    bad=[]
    for s in seq:
        for t in ('dark','light'):
            r=s['ring'][t];why=''
            if not r: why='no ring'+('' if s['seen'] else ' (the browser does not count it as focused by keyboard)')
            elif r['w']<RING: why=f'a ring of {r["w"]:g} px'
            elif r.get('why'): tot['unjudged']+=1
            elif r['out']<NEED: why=f'a ring in {r["color"]} on {r["bg"]}, {r["out"]}:1'
            elif r.get('inn',NEED)<NEED: why=f'a ring in {r["color"]} inside the control on {r["inbg"]}, {r["inn"]}:1'
            elif r['cut']: why=f'a ring cut off at the {r["cut"]}'
            if why: bad.append(f'{s["what"]}: {why}, {t}');offenders.setdefault(s['what'].split(' "')[0]+': '+why.split(',')[0],name)
    tot['noring']+=len({b.rsplit(',',1)[0] for b in bad})
    check(f'{name}: each stop shows a ring of {RING} px or more at {NEED}:1, in both themes',not bad,lambda: f'{len(bad)}: '+'; '.join(bad[:5]))
def order(name,seq):
    # A later stop that begins more than a row above an earlier one breaks the order, unless it starts at or
    # beyond that one's right edge: the next column of a layout begins at the top again.
    if not seq: return
    pos=js(PLACES,[s['id'] for s in seq]);top=max(pos[0],0);pos=pos[1+top:]+pos[1:1+top];seq=seq[top:]+seq[:top];bad=[]
    for j,L in enumerate(pos):
        if not L or seq[j]['under']: continue  # what lies under a skill's guide is counted where it is found, not here
        for i in range(j):
            E=pos[i]
            if E and not seq[i]['under'] and L[1]+L[3]<=E[1] and E[1]-L[1]>ROW and L[0]<E[0]+E[2]-1: bad.append(f'{seq[j]["what"]} comes after {seq[i]["what"]}, which lies {E[1]-L[1]:.0f} px below it');break
    tot['order']+=len(bad)
    check(f'{name}: Tab follows reading order',not bad,lambda: f'{len(bad)} out of order, the first: {bad[0]}')
def astray(name,seq,box=None):
    # Stops that should not be: hidden, inert, outside the window, under something else, or outside the dialog on top.
    # A box that scrolls may take the focus itself, so the arrow keys scroll it (Firefox stops Tab at each one that
    # has more than it shows): that is a stop, not a fault, and it is held to a ring like any other.
    # Seen and left, and counted: beside a wide page a skill's guide lies over the right 420 px of it, the bell and
    # the search button with it. What is under the guide takes the focus under it, as it is under it for a mouse.
    guide=[s for s in seq if s['under']=='aside#panel.panel' and not box and js("return innerWidth>820")];tot['covered']+=len(guide)
    bad=[f'{s["what"]}: {s["hidden"] or ("under "+s["under"] if s["under"] else "outside the dialog" if not s["inside"] else "not a control")}' for s in seq if s not in guide and (s['hidden'] or s['under'] or not s['inside'] or not (s['control'] or s['scrolls']))]
    tot['astray']+=len(bad)
    check(f'{name}: nothing hidden, inert, outside the window or under something else takes the focus',not bad,lambda: f'{len(bad)}: '+'; '.join(bad[:5]))
def reached(name,seq,box=None):
    # A control is known by its number; one the page drew again since the round passed it (the menu and the bell
    # are, when a count in them changes), by what it is: its kind, its address, its name and its data. A stop on
    # an element that is no longer on show stands for one such control only, so of two that look the same (two
    # Load more buttons) each needs a stop of its own.
    c=js(CONTROLS,box);got={s['id'] for s in seq};now={r[0] for r in c['rows']};groups={r[2] for r in c['rows'] if r[0] in got and r[2].startswith('radio:')}
    spare={}
    for s in seq:
        if s['id'] not in now: spare[s['same']]=spare.get(s['same'],0)+1
    miss=[]
    for r in c['rows']:
        if r[0] in got or r[2]=='off' or r[2] in groups: continue
        if spare.get(r[3],0)>0: spare[r[3]]-=1;continue
        miss.append(r[1])
    tot['controls']+=len(c['rows']);tot['unreached']+=len(miss)
    check(f'{name}: Tab reaches every control on show',not miss,lambda: f'{len(miss)} of {len(c["rows"])} not reached: '+'; '.join(miss[:6]))
    check(f'{name}: no control asks for a place of its own in the order (a positive tabindex)',not c['positive'],c['positive'])
    return c
def page(name,limit=700,back=False):
    # One round of Tab over the page as it stands, held against the page. With back, a round of Shift+Tab
    # after it: the same stops the other way, and none of them under the bar at the top of the window.
    # The short messages lie over the page while they show, and one of them holds a link: they are waited out.
    wait_for("return !document.querySelector('#unlock').firstChild&&!document.querySelector('#toast').textContent",'the achievement card and the message at the bottom to go',15);idle()
    seq,end,gone=walk(None,limit);tot['rounds']+=1;tot['stops']+=len(seq)
    check(f'{name}: Tab comes back to where it began',end=='round',lambda: f'{ENDS[end]} after {len(seq)} stops; the last: {seq[-1]["what"] if seq else "none"}')
    reached(name,seq);astray(name,seq);order(name,seq);rings(name,seq)
    if back:
        rev,end2,gone2=walk(None,limit,back=True);tot['rounds']+=1;tot['stops']+=len(rev)
        check(f'{name}: Shift+Tab goes the same round the other way',end2=='round' and mirror(seq,rev),lambda: f'{ENDS[end2]}; {len(seq)} stops forward, {len(rev)} back')
        astray(name+', backwards',rev)
    return seq
def within(name,box):
    # The stops of something that is open beside its button and is no dialog: Tab is pressed while the focus stays in it.
    # The last press took the focus out of it; Shift+Tab takes it back in, where Escape is pressed next.
    seq=[];ids=set()
    for _ in range(80):
        s=js(STOP,box)
        if s.get('out') or not s['inside'] or s['id'] in ids: break
        ids.add(s['id']);seq.append(s);key(TAB)
    # A skill's guide takes the focus as a whole when it opens: that is where Tab goes on from, not a stop of
    # its own, and like the content of a page it shows no ring.
    key(TAB,hold=(SHIFT,));seq=[s for s in seq if not s['whole']];tot['stops']+=len(seq);rings(name,seq)
def used(name,sel,press=ENTER,then=None,stays=None):
    # Goes to a control, presses it, and looks whether the focus is still on it (or on `stays`) once the page
    # has drawn what the press changed.
    if not tab_to(sel,name): return
    key(press);idle()
    if then: wait_for(then,f'{name} to take effect')
    kept(name,stays or sel)
def kept(name,sel,words='the focus is still on it when the page has been drawn again, and shows'):
    # A control that was used has the focus after the redraw, and the browser still draws its ring.
    tot['used']+=1;ok=poll(lambda: on(sel) and js("return document.activeElement.matches(':focus-visible')"),3)[1]
    if not ok: tot['dropped']+=1
    check(f'{name}: {words}',ok,active)
def dialog(name,opener,box,press=ENTER,modal=True,back_to=None,close=ESC,inert=True,shut=None):
    # Opens a dialog from the control that has it, and holds it to what a dialog owes the keyboard.
    if not tab_to(opener,f'the control that opens {name}'): return
    key(press)
    seen=f"const b=document.querySelector({json.dumps(box)});return !!b&&b.getClientRects().length>0"
    if not wait_for(seen,f'{name} to open'): return
    idle();tot['dialogs']+=1;n0=len(errs)
    check(f'{name}: the focus moves into it when it opens',lambda: js("const b=document.querySelector(arguments[0]);return !!b&&b.contains(document.activeElement)",box),active,timeout=3)
    if modal:
        seq,end,gone=walk(box,300);tot['rounds']+=1;tot['stops']+=len(seq)
        check(f'{name}: Tab goes round inside it',end=='round' and gone==0 and all(s['inside'] for s in seq),lambda: f'{ENDS[end] or "left the dialog"}; {gone} presses on no element; outside: '+'; '.join(s['what'] for s in seq if not s['inside'])[:300])
        c=reached(name,seq,box);astray(name,seq,box);order(name,seq);rings(name,seq)
        rev,end2,gone2=walk(box,300,back=True)
        check(f'{name}: Shift+Tab goes round inside it, the other way',end2=='round' and gone2==0 and mirror(seq,rev),lambda: f'{ENDS[end2]}; forward {[s["what"] for s in seq][:8]}, back {[s["what"] for s in rev][:8]}')
        if inert: check(f'{name}: the page behind it is out of reach',c['beyond']==0,f'{c["beyond"]} controls of the page are still on show to the keyboard')
    else: within(name,box)
    key(close)
    wait_for(shut or f"const b=document.querySelector({json.dumps(box)});return !b||b.getClientRects().length===0",f'{name} to close on Escape')
    check(f'{name}: closing it hands the focus back to the control that opened it',lambda: on(back_to or opener),active,timeout=3)
    if len(errs)>n0: tot['baddialogs']+=1
def go(h,name):
    # A page is opened from the menu: Tab to its link, Enter.
    if not tab_to(f'#menu a[href="{h}"]',f'{name} in the menu'): return
    key(ENTER);wait_for(f"return location.hash==={json.dumps(h)}",f'the address of {name}');idle()
# A place in the window where a click lands on no control and outside the content: the page as a whole takes the focus.
BLANK="for(let y=8;y<innerHeight;y+=12)for(let x=8;x<innerWidth;x+=16){const t=document.elementFromPoint(x,y);if(t&&t!==document.documentElement&&!t.closest(arguments[0])&&!t.closest('#main,[tabindex],label,#panel,#modal,#palette'))return [x,y]}return null"
def middle(sel): return js("const r=document.querySelector(arguments[0]).getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2]",sel)
# What the focus is on that deletes or removes something: the rule of app.js, written again.
WIPES="const e=document.activeElement;return !!e&&/(^|-)(del|rm|remove)(-|$)/.test(e.dataset&&e.dataset.act||'')"
try:
    # What the journey needs and a keyboard cannot make: a second member, her post and her comment under it.
    op=admin();api(op,'flags',{'flags':{'moderation':True,'catalogEditor':True}},'PUT');api(op,'me',{'props':['nunchaku']},'PUT')
    rae=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(rae,'signup',{'username':'rae','password':'flowa-test-only'});api(rae,'me',{'props':['nunchaku']},'PUT')
    rp=api(rae,'posts',{'body':'First clean upspin today'})['posts'][0]['id']
    rc=api(rae,f'posts/{rp}/comments',{'body':'Ten minutes of slow figure-8s first.'})['comment']['id']
    start(1440,1100)

    # A visitor. The first Tab stop of every page is the link that skips the menu.
    key(TAB)
    check('the first Tab stop is a link that skips to the content',lambda: on('a.skip'),active,timeout=3)
    key(ENTER)
    check('the skip link puts the focus on the content, and does not scroll its top under the bar',lambda: on('#main') and js("return scrollY")==0,lambda: (active(),js("return scrollY")),timeout=3)
    key(TAB)
    check('and the next Tab stop is the first control of the content',lambda: js("const m=document.querySelector('#main'),e=document.activeElement;return m.contains(e)&&e===[...m.querySelectorAll(arguments[0])].find(x=>x.getClientRects().length>0)",SEL),active,timeout=3)
    used('landing: Pause, under the demo','#main [data-act=demo][data-k=play]')
    page('landing')

    # Sign in.
    go('#/signin','Sign in')
    wait_for("return !!document.querySelector('#auth-form')",'the sign-in form')
    page('sign in')
    tab_to('#auth-form [name=username]','the username box');type_text('admin');key(TAB)
    check('sign in: Tab goes from the username to the password',on('#auth-form [name=password]'),active)
    type_text('flowa-test-only');key(ENTER)
    who=lambda: js("return (document.querySelector('.nav-profile .tiny')||{}).textContent")
    check('sign in: a name, a password and Enter sign a person in',lambda: who()=='@admin',who,timeout=30);idle()
    check('sign in: the focus is on the page that opened, not lost',lambda: js("const e=document.activeElement;return document.querySelector('#main').contains(e)"),active,timeout=3)
    page('dashboard')

    # Log a session.
    go('#/practice','Practice')
    page('practice')
    tab_to('#log-form [name=minutes]','the minutes box');key('a',hold=(CTRL,));type_text('40')
    check('practice: the minutes box takes what is typed',js("return document.querySelector('#log-form').minutes.value")=='40',lambda: js("return document.querySelector('#log-form').minutes.value"))
    tab_to('[data-pick=focus-add]','the list of skills');key(DOWN,DOWN)
    skill=js("return document.querySelector('[data-pick=focus-add]').value")
    key(ENTER);idle()
    chips=lambda: js("return [...document.querySelectorAll('#log-form [data-act=focus-del]')].map(b=>b.dataset.id)")
    check('practice: the arrow keys choose a skill and Enter adds it, with the focus left on the list',lambda: skill!='' and chips()==[skill] and on('[data-pick=focus-add]'),lambda: (skill,chips(),active()))
    check('practice: the minutes typed before are still there',js("return document.querySelector('#log-form').minutes.value")=='40')
    # A second skill, then Remove on the first: the focus must not land on the second one's Remove, where Enter
    # again would take that one away too.
    key(DOWN);other=js("return document.querySelector('[data-pick=focus-add]').value");key(ENTER);idle()
    wait_for(lambda: chips()==[skill,other],'a second skill chosen')
    if tab_to(f'#log-form [data-act=focus-del][data-id="{skill}"]','Remove, on the first skill chosen'): key(ENTER)
    check('practice: Remove takes the skill away, and the focus goes to a control that removes nothing, not to the next Remove',lambda: chips()==[other] and js("return document.querySelector('#main').contains(document.activeElement)&&document.activeElement.matches(':focus-visible')") and not js(WIPES),lambda: (chips(),active()),timeout=3)
    skill=other
    tab_to('#log-form button[type=submit]','Save session');key(ENTER)
    sessions=lambda: [(s['minutes'],s['focus']) for s in get(op,'progress/nunchaku')['state'].get('sessions',[])]
    check('practice: the session is on the server, with its minutes and its skill',lambda: sessions()==[(40,[skill])],sessions,timeout=20);idle()
    kept('practice: Save session','#log-form button[type=submit]')
    page('practice, a session logged')

    # Build a combo and publish it.
    go('#/combos','Combos')
    tab_to('#combo-form [name=name]','the box for a combo name');type_text('Keyboard loop');key(ENTER);idle()
    wait_for("return !!document.querySelector('[data-pick=combo-add]')",'the new combo to open')
    kept('combos: the name of a new combo','#combo-form [name=name]')
    # The box has the focus back, put there for the keys. A click into it says the person is about to type: a
    # redraw that comes meanwhile (asked for here, as a reply from the server would cause it) keeps the box.
    was=active();point(*middle('#combo-form [name=name]'));js("render()")
    check('combos: a box clicked after the keys put the focus back in it keeps the focus through a redraw',lambda: active()==was and on('#combo-form [name=name]'),active,timeout=3)
    if tab_to('[data-pick=combo-add]','the list of skills to add'): key(*[DOWN]*5,ENTER);idle()
    kept('combos: the list of skills, when one was added','[data-pick=combo-add]')
    # The page is drawn once more when the suggestions for the next skill arrive.
    wait_for("return !!document.querySelector('.nt-list')",'the suggestions for the next skill',20);idle()
    kept('combos: the list of skills, when the suggestions arrived','[data-pick=combo-add]')
    if tab_to('[data-pick=combo-add]','the list of skills to add'): key(*[DOWN]*7,ENTER);idle()
    seq=lambda: js("return [...document.querySelectorAll('.cb-steps .cb-id')].map(e=>e.textContent)")
    check('combos: two skills are added from the list with the arrow keys and Enter',lambda: len(seq())==2,seq)
    made=seq()
    used('combos: one more repetition','[data-act=combo-rep][data-d="1"]')
    used('combos: how the first link went','[data-act=combo-tr]')
    used('combos: Pause, under the player','[data-act=combo-demo][data-k=play]')
    # The same button with the mouse, to play: the page is drawn again, the button that was clicked is gone and the
    # focus rests on the page as a whole, as it does for most people with a mouse. The clock then changes its
    # words twice a frame; the page's watcher (afterDraw in app.js, counted from outside) has nothing to do for them.
    PLAY='[data-act=combo-demo][data-k=play]'
    js("window.__ad=0;window.__words=0;const o=window.afterDraw;window.__adWas=o;window.afterDraw=function(){window.__ad++;return o.apply(this,arguments)};window.__mo=new MutationObserver(()=>window.__words++);window.__mo.observe(document.querySelector('#main'),{childList:true,subtree:true,characterData:true})")
    point(*middle(PLAY));wait_for("return window.Demo.combo.playing",'the combo to play after a click');idle()
    js("window.__ad=0;window.__words=0");time.sleep(1)  # fixed: a second of a playing combo is sampled
    n=js("return [window.__ad,window.__words,document.activeElement===document.body]")
    # Each change of the page's elements (a step of the combo coming up, the bell) is still looked at: 2 at most in the second.
    check('combos: with the pointer in use, a playing combo does not set the page watcher to work each frame',n[0]<=2 and n[1]>=10 and n[2],lambda: f'{n[0]} runs over 1 s, {n[1]} changes on the page, the focus on the page as a whole: {n[2]}')
    js("window.afterDraw=window.__adWas;window.__mo.disconnect()")
    point(*middle(PLAY));wait_for("return !window.Demo.combo.playing",'the combo to stop after a click');idle()
    page('combos, one open')
    if tab_to('[data-act=combo-publish]','Publish'): key(ENTER)
    wait_for("return location.hash==='#/timeline'&&!!document.querySelector('#compose-form .combo-chip')",'the composer to open with the combo')
    check('publish: the cursor is in the text of the post',lambda: on('#compose-form textarea'),active,timeout=3)
    type_text('By keyboard.')
    if tab_to('#compose-form button[type=submit]','Post'): key(ENTER)
    posted=lambda: [(p['body'],p['data']['combo']['name'],p['data']['combo']['seq']) for p in get(op,'posts')['posts'] if (p.get('data') or {}).get('combo')]
    check('publish: the post is on the server, with the combo and its skills',lambda: posted()==[('New combo: Keyboard loop. By keyboard.','Keyboard loop',made)],posted,timeout=20);idle()
    kept('publish: Post','#compose-form button[type=submit]')

    # Reply to a comment.
    if js("return location.hash")!='#/timeline': go('#/timeline','Timeline')
    page('timeline',back=True)
    # The bar at the top of the window stays there, and the browser counts a control under it as in view. The
    # page is scrolled from outside the keyboard to put Comment under the bar; Tab then goes to it from Like.
    # The window is made low for it, so the page has room to scroll that far.
    cmd('WebDriver:SetWindowRect',width=1440,height=600);wait_for("return innerHeight<700",'the window to be low')
    if tab_to('.post [data-act=like]','Like, under the first post'):
        was=js("scrollBy(0,document.activeElement.getBoundingClientRect().top-20);const r=document.activeElement.nextElementSibling.getBoundingClientRect();return r.top<document.querySelector('.top').getBoundingClientRect().bottom")
        key(TAB)
        check('a control that lies under the bar at the top when Tab reaches it is moved out from under it',lambda: was and on('[data-act=cmt-toggle]') and js(STOP,None)['under']=='',lambda: ('it was under the bar' if was else 'it could not be put under the bar',active(),js(STOP,None)['under']),timeout=3)
    cmd('WebDriver:SetWindowRect',width=1440,height=1100);wait_for("return innerHeight>700",'the window to be tall again')
    if tab_to(f'[data-act=cmt-reply][data-id="{rc}"]','Reply, under the comment'): key(ENTER)
    check('reply: Reply opens a box with the cursor in it',lambda: on('.reply-form input[name=body]'),active,timeout=3)
    page('timeline, a reply being written')
    if tab_to('.reply-form input[name=body]','the reply box'): type_text('Same here, it helps.');key(ENTER)
    replies=lambda: [(c['body'],c.get('parentId')) for c in get(op,f'comments/{rc}/replies')['comments']]
    check('reply: the reply is on the server, under the comment',lambda: replies()==[('@rae Same here, it helps.',rc)],replies,timeout=20);idle()
    kept('reply',f'[data-act=cmt-reply][data-id="{rc}"]','when it is sent the focus is back on Reply')
    used('timeline: Like',f'.post[data-post="{rp}"] [data-act=like]',then=f"return document.querySelector('.post[data-post=\"{rp}\"] [data-act=like]').getAttribute('aria-pressed')==='true'")
    used('timeline: Bookmark',f'.post[data-post="{rp}"] [data-act=bookmark]',press=SPACE)
    # Delete, on the reply: the focus goes to a control before it that deletes nothing, not to the Delete of the
    # comment above it, where Enter again would delete that one.
    mine=[c['id'] for c in get(op,f'comments/{rc}/replies')['comments']]
    js("window.__confirm=window.confirm;window.confirm=()=>true")
    if mine and tab_to(f'[data-act=cmt-del][data-id="{mine[0]}"]','Delete, under the reply'): key(ENTER)
    check('timeline: Delete takes the reply away',lambda: get(op,f'comments/{rc}/replies')['comments']==[],timeout=20);idle()
    check('timeline: and the focus goes to a control that deletes nothing, not to the next Delete',lambda: js("return document.querySelector('#main').contains(document.activeElement)&&document.activeElement.matches(':focus-visible')") and not js(WIPES),active,timeout=3)
    js("window.confirm=window.__confirm")
    # A person with a mouse: typing in a box, then a click beside the content, then the search by its key and
    # Escape. Then the same with a click on Report that does not focus the button (as in Safari, and Firefox on
    # macOS) and Escape. Each time the box that had the focus is out of sight, and neither the focus nor the
    # scroll may move. The window is made low, so the page scrolls far enough.
    cmd('WebDriver:SetWindowRect',width=1440,height=600);wait_for("return innerHeight<700",'the window to be low')
    BOX='#compose-form textarea';REP=f'.post[data-post="{rp}"] [data-act=report]'
    away="const r=document.querySelector(arguments[0]).getBoundingClientRect();return r.bottom<=document.querySelector('.top').getBoundingClientRect().bottom"
    if tab_to(BOX,'the box for a post'):
        type_text('Half')
        js("document.querySelector(arguments[0]).scrollIntoView({block:'center'})",REP);wait_for(lambda: js(away,BOX),'the box for a post to be out of sight')
        spot=js(BLANK,SEL);y0=js("return scrollY")
        point(*spot);key('/');wait_for("return !document.querySelector('#palette').hidden",'the search to open by its key')
        key(ESC);wait_for("return document.querySelector('#palette').hidden",'the search to close')
        check('pointer: after a click beside the content, the search by its key and Escape leave the focus and the scroll alone',lambda: active() is None and js("return scrollY")==y0,lambda: (spot,active(),y0,js("return scrollY")),timeout=3)
    if tab_to(BOX,'the box for a post'):
        type_text(' done')
        js("window.__nofocus=e=>{if(e.target.closest('button'))e.preventDefault()};addEventListener('mousedown',window.__nofocus,true)")
        js("document.querySelector(arguments[0]).scrollIntoView({block:'center'})",REP);wait_for(lambda: js(away,BOX),'the box for a post to be out of sight')
        point(*js(BLANK,SEL));point(*middle(REP));wait_for("return !!document.querySelector('#report-form')",'the report dialog to open by a click')
        y0=js("return scrollY");key(ESC);wait_for("return document.querySelector('#modal').hidden",'the report dialog to close')
        check('pointer: a dialog opened by a click and closed by Escape leaves the focus and the scroll alone',lambda: not on(BOX) and not on(REP) and js("return scrollY")==y0,lambda: (active(),y0,js("return scrollY")),timeout=3)
        js("removeEventListener('mousedown',window.__nofocus,true)")
    if tab_to(BOX,'the box for a post'): key('a',hold=(CTRL,));key(BACKSPACE)  # the draft is not left for the steps that follow
    cmd('WebDriver:SetWindowRect',width=1440,height=1100);wait_for("return innerHeight>700",'the window to be tall again')

    # Dialogs and what opens over the page.
    dialog('search','.search-btn','#palette .pal-box')
    # Search is worked with the keys: typed words, then Enter on the result that has the focus.
    if tab_to('.search-btn','Search'):
        key(ENTER);wait_for("return !!document.querySelector('#pal-q')",'the search to open');type_text('pra');key(TAB,TAB)
        want=js("const e=document.activeElement;return e.matches('.pal-i:not(.on)')?e.getAttribute('href'):''")
        key(ENTER)
        check('search: Enter on a result that has the focus opens that one',lambda: want!='' and js("return location.hash")==want and js("return document.querySelector('#palette').hidden"),lambda: (want,js("return location.hash")),timeout=3);idle()
        if not js("return document.querySelector('#palette').hidden"): key(ESC)  # the search must not stay over the steps that follow
        go('#/timeline','Timeline')
    dialog('notifications','#bell [data-act=bell]','.ntf-panel',modal=False)
    dialog('the trick picker','[data-act=pick-open]','.picker',modal=False,back_to='#compose-form textarea')
    dialog('report',f'.post[data-post="{rp}"] [data-act=report]','#modal .modal-box')
    # The search opened over a dialog is on top of it: one Escape closes the search alone, the next the dialog.
    if tab_to(f'.post[data-post="{rp}"] [data-act=report]','Report'):
        key(ENTER);wait_for("return !!document.querySelector('#report-form')",'the report dialog')
        key('k',hold=(CTRL,));wait_for("return !document.querySelector('#palette').hidden",'the search to open over the dialog')
        key(ESC)
        check('search over a dialog: one Escape closes the search alone, and the focus is back in the dialog',lambda: js("return document.querySelector('#palette').hidden&&!document.querySelector('#modal').hidden&&document.querySelector('#modal').contains(document.activeElement)"),active,timeout=3)
        key(ESC)
        check('search over a dialog: the next Escape closes the dialog, and the focus is back on Report',lambda: js("return document.querySelector('#modal').hidden") and on(f'.post[data-post="{rp}"] [data-act=report]'),active,timeout=3)
    # A report is sent with the keys: the first reason, then Send.
    if tab_to(f'.post[data-post="{rp}"] [data-act=report]','Report'):
        key(ENTER);wait_for("return !!document.querySelector('#report-form')",'the report dialog');key(SPACE)
        if tab_to('#report-form button[type=submit]','Send report'): key(ENTER)
        wait_for("return document.querySelector('#modal').hidden",'the report to be sent');idle()
        check('report: when it is sent the focus is back on the button that opened it',lambda: on(f'.post[data-post="{rp}"] [data-act=report]'),active,timeout=3)
    # Her profile and her post, each opened from a link in the post.
    if tab_to(f'.post[data-post="{rp}"] a.post-name','her name, on the post'):
        key(ENTER);wait_for("return location.hash==='#/u/rae'&&!!document.querySelector('.prof-head')",'her profile');idle()
        check('a page opened from a link in the page: the focus is on the page that opened, not lost',lambda: js("return document.querySelector('#main').contains(document.activeElement)"),active,timeout=3)
        page('public profile')
    go('#/timeline','Timeline')
    if tab_to(f'.post[data-post="{rp}"] a[href="#/post/{rp}"]','the time of the post, which opens it'):
        key(ENTER);wait_for(f"return location.hash==='#/post/{rp}'",'the post on its own page');idle()
        page('a post')
    go('#/achievements','Achievements')
    page('achievements')
    dialog('share your streak','[data-act=share-streak]','#modal .modal-box')
    if tab_to('[data-act=share-streak]','Share'):
        key(ENTER);wait_for("return !!document.querySelector('#share-cv')",'the share dialog')
        used('share: the format','[data-act=share-fmt][data-v=square]',then="return document.querySelector('[data-act=share-fmt][data-v=square]').getAttribute('aria-pressed')==='true'")
        key(LEFT)
        check('share: the left arrow moves to the choice before',lambda: on('[data-act=share-fmt][data-v=story]'),active,timeout=3)
        key(ESC);wait_for("return document.querySelector('#modal').hidden",'the share dialog to close')
    go('#/mod','Moderation')
    wait_for("return !!document.querySelector('[data-act=mod-open]')",'the report in the queue')
    page('moderation')
    check('moderation: the tab that is open says so',js("return [...document.querySelectorAll('.seg a[href^=\"#/mod\"]')].map(a=>a.getAttribute('aria-current'))")==['page',None],lambda: js("return [...document.querySelectorAll('.seg a[href^=\"#/mod\"]')].map(a=>a.getAttribute('aria-current'))"))
    dialog('a moderation action','[data-act=mod-open][data-do=mute]','#modal .modal-box')
    go('#/admin','Admin')
    wait_for("return document.querySelectorAll('.admin-tabs a').length>1",'the admin sections')
    page('admin')
    cur=lambda: js("return [...document.querySelectorAll('.admin-tabs a')].filter(a=>a.getAttribute('aria-current')==='page').map(a=>a.getAttribute('href'))")
    check('admin: the section that is open says so',len(cur())==1,cur)
    if tab_to('.admin-tabs a[href="#/admin/features"]','Features, in the admin sections'):
        key(ENTER);wait_for("return location.hash==='#/admin/features'",'the features section');idle()
        check('admin: a section opened with Enter says so and keeps the focus',lambda: cur()==['#/admin/features'] and on('.admin-tabs a[href="#/admin/features"]'),lambda: (cur(),active()),timeout=3)
        page('admin features')
    if tab_to('.admin-tabs a[href="#/admin/accounts"]','Accounts, in the admin sections'):
        key(ENTER);wait_for("return !!document.querySelector('select[data-change=user-role]')",'the accounts');idle()
        page('admin accounts')
    # The skill map editor: the prop that is open says so, and so does the view.
    if tab_to('.admin-tabs a[href="#/admin/maps"]','Catalogs, in the admin sections'):
        key(ENTER);wait_for("return !!document.querySelector('[data-act=ed-view]')",'the editor',20);idle()
        check('editor: the prop that is open says so',js("return [...document.querySelectorAll('.seg a[href^=\"#/admin/maps/\"]')].filter(a=>a.getAttribute('aria-current')==='page').map(a=>a.getAttribute('href'))")==['#/admin/maps/nunchaku'])
        used('editor: the branches view','[data-act=ed-view][data-v=branches]')
        check('editor: the view chosen says so',lambda: js("return [...document.querySelectorAll('[data-act=ed-view]')].filter(b=>b.getAttribute('aria-pressed')==='true').map(b=>b.dataset.v)")==['branches'],timeout=3)
        page('skill map editor, branches')

    # Choices that showed by colour alone: each says which is chosen, and the arrow keys walk them.
    go('#/settings','Settings')
    page('settings')
    pressed=lambda act: js("return [...document.querySelectorAll('[data-act='+arguments[0]+']')].filter(b=>b.getAttribute('aria-pressed')==='true').map(b=>b.dataset.v)",act)
    used('settings: the light theme','[data-act=theme][data-v=light]')
    check('settings: the theme chosen says so',lambda: pressed('theme')==['light'] and js("return document.documentElement.dataset.theme")=='light',lambda: pressed('theme'))
    key(RIGHT)
    check('settings: the right arrow moves to the next choice',lambda: on('[data-act=theme][data-v=dark]'),active,timeout=3)
    key(SPACE);idle()
    check('settings: Space chooses it',lambda: pressed('theme')==['dark'] and on('[data-act=theme][data-v=dark]'),lambda: (pressed('theme'),active()),timeout=3)
    used('settings: the left hand','[data-act=dominant][data-v=L]')
    check('settings: the hand chosen says so, and is saved',lambda: pressed('dominant')==['L'] and get(op,'progress/nunchaku')['state']['settings']['dominant']=='L',lambda: pressed('dominant'),timeout=20)
    used('settings: the right hand','[data-act=dominant][data-v=R]')
    # The button that opens a file. The system's file chooser is kept shut, so only the press is seen.
    js("window.__files=0;for(const i of document.querySelectorAll('input[type=file]'))i.addEventListener('click',e=>{e.preventDefault();window.__files++})")
    if tab_to('#main button[data-act=file]','Import JSON'):
        key(ENTER);key(SPACE)
        check('settings: Enter and Space on Import JSON each open the file chooser',lambda: js("return window.__files")==2,lambda: js("return window.__files"),timeout=3)
    go('#/profile','Profile')
    page('profile')
    # A field that saves as it is left: Tab still arrives at the control after it, though the page was drawn again.
    go('#/plan','Plan')
    page('plan')
    if tab_to('[data-change=setting][data-k=event]','the box for the target event'):
        after=js("const l=[...document.querySelectorAll(arguments[0])].filter(e=>e.getClientRects().length&&!e.disabled),i=l.indexOf(document.activeElement);return l[i+1].dataset.id||''",SEL)
        type_text('Open 2027');key(TAB)
        check('plan: a field that saves when it is left is saved',lambda: get(op,'progress/nunchaku')['state']['settings']['event']=='Open 2027',lambda: get(op,'progress/nunchaku')['state']['settings'],timeout=20)
        tot['used']+=1;ok=poll(lambda: js("const e=document.activeElement;return e.matches('[data-change=gate]')&&e.dataset.id===arguments[0]",after),3)[1];tot['dropped']+=0 if ok else 1
        check('plan: and Tab arrives at the control after it',ok,active)

    # Skill maps, a branch and a skill's guide.
    go('#/maps','Skill maps')
    page('skill maps')
    go('#/prop/nunchaku/F','the first branch')
    page('branch graph')
    used('branch: the list view','[data-act=view][data-v=list]')
    check('branch: the view chosen says so',lambda: pressed('view')==['list'],lambda: pressed('view'))
    page('branch list')
    used('branch: the graph view','[data-act=view][data-v=graph]')
    dialog('a skill guide','#main a.node[href$="/F/F5"]','#panel',modal=False)
    if tab_to('#main a.node[href$="/F/F5"]','the skill F5 in the graph'):
        key(ENTER);wait_for("return !document.querySelector('#panel').hidden",'the skill guide');idle()
        used('skill guide: Pause, under the demo','#panel [data-act=demo][data-k=play]')
        page('skill guide')
        used('skill guide: a level','#panel [data-act=level][data-v="2"]',then="return document.querySelector('#panel [data-act=level][data-v=\"2\"]').getAttribute('aria-pressed')==='true'")
        used('skill guide: the level back','#panel [data-act=level][data-v="0"]')
        key(ESC);wait_for("return document.querySelector('#panel').hidden",'the skill guide to close')
    used('menu: a group of branches closes and opens','#menu [data-act=nav-toggle]')
    key(ENTER);idle()

    # A narrow window: the menu is a drawer behind a button, and a skill's guide covers the page.
    cmd('WebDriver:SetWindowRect',width=700,height=900)
    wait_for("return innerWidth<=820&&getComputedStyle(document.querySelector('.menu-btn')).display!=='none'",'the window to be narrow enough for the menu button')
    page('branch graph, narrow window')
    dialog('the menu, narrow window','.menu-btn','#side',shut="return !document.body.classList.contains('nav-open')")
    dialog('a skill guide, narrow window','#main a.node[href$="/F/F5"]','#panel')
    cmd('WebDriver:SetWindowRect',width=1440,height=1100)
    wait_for("return innerWidth>820",'the window to be wide again')

    # Someone new, by keyboard from the first key: sign out, an account, the props, the avatar offered, the tour.
    go('#/profile','Profile')
    api(op,'flags',{'flags':{'tour':True}},'PUT')
    if tab_to('[data-act=signout]','Sign out'): key(ENTER)
    wait_for(lambda: who()!='@admin','the person to be signed out',20);idle()
    if tab_to('#main a[href="#/signup"]','Create an account'): key(ENTER)
    wait_for("return !!document.querySelector('#auth-form[data-mode=signup]')",'the form for a new account')
    if tab_to('#auth-form [name=username]','the username box'):
        type_text('newbie');key(TAB,TAB);type_text('flowa-test-only');key(TAB);type_text('flowa-test-only');key(ENTER)
    check('a new member: an account is made with the keys alone',lambda: who()=='@newbie',who,timeout=30)
    wait_for("return !!document.querySelector('#props-form')",'the page that asks for the props');idle()
    page('choosing props, first visit')
    if tab_to('#props-form input[name=prop]','the first prop'): key(SPACE)
    if tab_to('#props-form button[type=submit]','Continue'): key(ENTER)
    wait_for("return !!document.querySelector('[data-act=av-skip]')",'the avatar a new member is offered',30);idle()
    check('a new member: the focus is on the page that opened, not lost',lambda: js("return document.querySelector('#main').contains(document.activeElement)"),active,timeout=3)
    page('avatar offer, first visit')
    if tab_to('[data-act=av-skip]','Skip, under the avatar offered'): key(ENTER)
    TIP='.react-joyride__tooltip'
    if wait_for(f"return !!document.querySelector('{TIP}')",'the guided tour to open',30):
        idle();tot['dialogs']+=1;n0=len(errs)
        check('the guided tour: the focus moves into it when it opens',lambda: js("const b=document.querySelector(arguments[0]);return !!b&&b.contains(document.activeElement)",TIP),active,timeout=3)
        seq,end,gone=walk(TIP,100);tot['rounds']+=1;tot['stops']+=len(seq)
        check('the guided tour: Tab goes round inside it',end=='round' and gone==0 and all(s['inside'] for s in seq),lambda: f'{ENDS[end] or "left the tour"}; {gone} presses on no element; outside: '+'; '.join(s['what'] for s in seq if not s['inside'])[:300])
        reached('the guided tour',seq,TIP);rings('the guided tour',seq)
        rev,end2,gone2=walk(TIP,100,back=True)
        check('the guided tour: Shift+Tab goes round inside it, the other way',end2=='round' and gone2==0 and mirror(seq,rev),lambda: f'{ENDS[end2]}; forward {[s["what"] for s in seq]}, back {[s["what"] for s in rev]}')
        if tab_to(TIP+' [data-action=primary]','Next, in the tour'): key(ENTER)
        check('the guided tour: Enter on Next shows the second step, with the focus inside it',lambda: js("const b=document.querySelector(arguments[0]);return !!b&&!!b.querySelector('[data-action=back]')&&b.contains(document.activeElement)",TIP),active)
        key(ESC)
        wait_for(f"return !document.querySelector('{TIP}')",'the tour to close on Escape');idle()
        check('the guided tour: closing it leaves the focus on the page, not lost',lambda: js("const e=document.activeElement;return !!e&&e!==document.body&&e.getClientRects().length>0"),active,timeout=3)
        if len(errs)>n0: tot['baddialogs']+=1
    page('dashboard, first visit')
    print(f'{tot["rounds"]} rounds of Tab, {tot["stops"]} stops; {tot["controls"]} controls on show, {tot["unreached"]} not reached; {tot["astray"]} stops that should not be; {tot["order"]} out of reading order')
    print(f'{tot["covered"]} stops under a skill guide that lies beside a wide page: seen and left')
    print(f'{tot["noring"]} stops without a ring that can be seen ({len(offenders)} kinds), {tot["unjudged"]} rings over a picture or a gradient not judged')
    print(f'{tot["dialogs"]} dialogs opened, {tot["baddialogs"]} that did not take, keep or hand back the focus; {tot["used"]} controls used, {tot["dropped"]} lost the focus when the page was drawn again')
    if LIST: print('\n'.join(f'  {k} (first at: {v})' for k,v in sorted(offenders.items())))
finally: done()
