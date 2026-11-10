# Contrast: on the main pages, in both themes, every piece of text, every icon and the edge of every field
# stands out from what is behind it: 4.5:1 for text, 3:1 for large text (24 px, or 18.66 px in bold), for
# icons and for the edge of a field (WCAG 2.2, 1.4.3 and 1.4.11).
# test/contrast.js reads the same from the stylesheet, in lint, with no browser. This walk reads it from the
# computed styles of the page as drawn, so it also sees a colour set in a template and text that inherits its
# colour or lands on a surface the stylesheet did not plan for. It does not see hover and focus states: those
# are in test/contrast.js alone.
# Not judged, and counted: text over a picture, a gradient or a canvas (the demo, avatars, the share preview),
# where the colour behind is not one colour; a control that is switched off, which the standard leaves out;
# checkboxes, radio buttons and sliders, which the browser draws. A page that lies dimmed behind an open dialog
# is judged as it stands without the dialog.
# CONTRAST_LIST=1 prints every pair of colours seen, the closest to its limit first, to work through.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
# A colour as the browser reports it, in any of the ways browsers write one: rgb(), rgba(), color(srgb ...)
# for a color-mix(), and hex. A colour over another is alpha compositing, straight (not premultiplied) in and out.
# What is behind an element is found by walking up its ancestors: each background is laid under what was
# gathered so far, an ancestor's opacity fades the text and the background alike, and the walk ends at the
# first background that is opaque. A background picture or gradient on the way, or a canvas, picture or frame
# lying under the element, ends it without an answer.
LIB=r"""
const root=document.querySelector(arguments[0]||'body');
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
const klass=e=>(e.getAttribute('class')||'').trim();
const cls=e=>e.tagName.toLowerCase()+(klass(e)?'.'+klass(e).split(/\s+/).join('.'):'');
const where=e=>{const p=e.parentElement&&e.parentElement.closest('[class]');return cls(e)+(p?' in '+cls(p):'')};
const shown=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden'&&!e.closest('[hidden]')&&(!e.checkVisibility||e.checkVisibility());
const media=[...document.querySelectorAll('canvas,img,video,iframe')].filter(shown).map(m=>[m,m.getBoundingClientRect()]);
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
const paint=(s,v)=>/^currentcolor$/i.test(v)?s.color:v;  // a browser may hand back the word instead of the colour it stands for
const big=e=>{const s=getComputedStyle(e),px=parseFloat(s.fontSize),w=parseInt(s.fontWeight)||400;return px>=24||(px>=18.66&&w>=700)};
const out={n:0,off:0,native:0,unknown:[],bad:[],all:[]},once=new Set();
const judge=(e,kind,fg,need)=>{
  if(!fg){out.unknown.push([where(e),kind+' in a colour written in a way this check does not read']);return}
  if(fg[3]===0)return;
  if(e.closest(':disabled,[disabled],[aria-disabled=true]')){out.off++;return}
  const g=behind(e,fg);
  if(g.why){out.unknown.push([where(e),kind+' '+g.why]);return}
  rate(e,kind,g.t,g.b,need);
};
const rate=(e,kind,f,b,need)=>{
  const r=ratio(f,b),row=[where(e),kind,hex(f),hex(b),Math.round(r*100)/100,need,e.id||''];out.n++;
  if(arguments[1])out.all.push(row);
  if(r<need&&!once.has(row.slice(0,4).join()+need)){once.add(row.slice(0,4).join()+need);out.bad.push(row)}
};
"""
# Every visible text, icon and field under the root. A text is judged by the element its words are in; the
# words of an svg <text> are painted with fill. An icon is an svg.i, painted with stroke, or with fill when
# it has no stroke. A field (a box to type in, a list to choose from) is judged three times: the words typed
# into it, the placeholder while it is empty, and its edge. The edge is found when the border on any side,
# the outline or the fill of the field differs by 3:1 from the surface the field sits on.
CHECK=LIB+r"""
const texts=new Set(),w=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);
for(let n;n=w.nextNode();)if(/\S/.test(n.nodeValue)&&!n.parentElement.closest('script,style,noscript,template,title,option,optgroup,select,textarea'))texts.add(n.parentElement);
for(const e of texts){if(!shown(e))continue;const s=getComputedStyle(e);judge(e,'text',P(e instanceof SVGElement?paint(s,s.fill):s.color),big(e)?3:4.5)}
for(const e of root.querySelectorAll('svg.i')){if(!shown(e))continue;const s=getComputedStyle(e),c=s.stroke!=='none'?s.stroke:s.fill;if(c!=='none')judge(e,'icon',P(paint(s,c)),3)}
for(const e of root.querySelectorAll('input,select,textarea')){
  if(!shown(e))continue;
  const ty=(e.getAttribute('type')||'text').toLowerCase(),s=getComputedStyle(e);
  if(/^(hidden|button|submit|reset|image)$/.test(ty))continue;
  if(parseFloat(s.opacity)===0)continue;
  if(/^(checkbox|radio|range|file|color)$/.test(ty)){out.native++;continue}
  judge(e,'typed text',P(s.color),big(e)?3:4.5);
  if(e.getAttribute('placeholder')&&!e.value){const p=getComputedStyle(e,'::placeholder'),c=P(p.color),o=parseFloat(p.opacity);judge(e,'placeholder',c&&c.slice(0,3).concat(c[3]*(o>=0?o:1)),big(e)?3:4.5)}
  if(e.closest(':disabled,[disabled]')){out.off++;continue}
  const inside=behind(e,[0,0,0,0]),outside=e.parentElement?behind(e.parentElement,[0,0,0,0]):inside;
  if(inside.why||outside.why){out.unknown.push([where(e),'edge '+(inside.why||outside.why)]);continue}
  let best=inside.b;const lines=['Top','Right','Bottom','Left'].filter(k=>parseFloat(s['border'+k+'Width'])>0&&!/none|hidden/.test(s['border'+k+'Style'])).map(k=>P(s['border'+k+'Color']));
  if(parseFloat(s.outlineWidth)>0&&s.outlineStyle!=='none')lines.push(P(s.outlineColor));
  for(const c of lines){if(!c)continue;const v=over(c,inside.b);if(ratio(v,outside.b)>ratio(best,outside.b))best=v}
  rate(e,'edge',best,outside.b,3);
}
return out;
"""
# The same arithmetic in Python, to hold the page's against: relative luminance and contrast ratio of sRGB colours.
def lum(c):
    f=lambda v: v/12.92 if v<=0.04045 else ((v+0.055)/1.055)**2.4
    r,g,b=(f(v/255) for v in c);return 0.2126*r+0.7152*g+0.0722*b
def ratio(a,b): x,y=sorted((lum(a),lum(b)));return round((y+0.05)/(x+0.05),2)
W,K=(255,255,255),(0,0,0);G=(148,148,148);H=(127.5,)*3  # white, black, the grey that is 3.03:1 on white, and half white over black
# Text, icons and fields made for the purpose, on white, with answers known beforehand. Ids that start with
# f must fail, with u must be left unjudged, with x are switched off; every other must pass.
PROBE="""<style>#ct-probe #pph::placeholder{color:#767676;opacity:1}#ct-probe #fph::placeholder{color:#949494;opacity:1}#ct-probe input{width:80px;height:24px;outline:0}</style>
<div style="background:#fff;color:#000;font:16px/1.5 sans-serif;padding:4px">
<p id="k21">Aa</p><p id="f777" style="color:#777">Aa</p><p id="p767" style="color:#767676">Aa</p>
<div style="background:#000"><p id="half" style="color:rgba(255,255,255,.5)">Aa</p><div style="background:rgba(255,255,255,.5)"><p id="veil" style="color:#000">Aa</p></div></div>
<p id="big24" style="color:#949494;font-size:24px">Aa</p><p id="big18" style="color:#949494;font-size:18.66px;font-weight:700">Aa</p>
<p id="f18" style="color:#949494;font-size:18.66px">Aa</p><p id="f18b" style="color:#949494;font-size:18px;font-weight:700">Aa</p>
<div style="background:#123f7a"><div><span><b id="inh" style="color:#fff">Aa</b></span></div></div>
<div style="opacity:.5"><p id="ffade">Aa</p></div>
<div style="background:linear-gradient(#fff,#eee)"><p id="ugrad">Aa</p></div>
<div style="position:relative;height:30px"><canvas width="60" height="30" style="position:absolute;left:0;top:0"></canvas><span id="ucanvas" style="position:absolute;left:4px;top:2px">Aa</span></div>
<button id="xoff" disabled style="color:#ccc;background:#fff">Aa</button>
<svg class="i" id="ic3" viewBox="0 0 24 24" style="color:#949494"><path d="M2 12h20"/></svg><svg class="i" id="fic" viewBox="0 0 24 24" style="color:#999"><path d="M2 12h20"/></svg>
<svg class="i" id="icfill" viewBox="0 0 24 24" style="stroke:none;fill:#949494"><path d="M2 2h20v20H2z"/></svg>
<input id="ed3" type="text" style="border:1px solid #949494;background:#fff;color:#000"><input id="fed" type="text" style="border:1px solid #ccc;background:#fff;color:#000">
<input id="edfill" type="text" style="border:0;background:#123f7a;color:#fff"><input id="edline" type="text" style="border:0;border-bottom:1px solid #949494;background:transparent;color:#000">
<input id="pph" type="text" placeholder="Aa" style="border:1px solid #000;background:#fff;color:#000"><input id="fph" type="text" placeholder="Aa" style="border:1px solid #000;background:#fff;color:#000">
<input id="nat" type="checkbox"></div>"""
tot=dict(n=0,bad=0,unknown=0,off=0,native=0);stops=set();listed={};unknown={};pairs={}
def theme(t):
    js("document.documentElement.dataset.theme=arguments[0];localStorage.setItem('nf-theme',arguments[0])",t);idle()
def page(name,h=None,act=None,themes=('dark','light')):
    if h is not None: nav(h)
    if act: js(act);idle()
    stops.add(name)  # the tour's stops come by twice, once in each theme
    for t in themes:
        theme(t);r=js(CHECK,None,bool(os.environ.get('CONTRAST_LIST')))
        for k in ('n','off','native'): tot[k]+=r[k]
        tot['bad']+=len(r['bad']);tot['unknown']+=len(r['unknown'])
        for d,why in r['unknown']: unknown.setdefault(f'{d}: {why}',[0,f'{name}, {t}'])[0]+=1
        for d,kind,f,b,v,need,_ in r['all']: pairs.setdefault((t,kind,f,b),[v,need,d,name])
        # Each failing pair is written out once, at the stop where it is first met.
        new=[]
        for d,kind,f,b,v,need,_ in r['bad']:
            k=(t,d,kind,f,b)
            if k not in listed: listed[k]=name;new.append(f'{d}: {kind} {f} on {b} is {v}:1, needs {need}:1')
        old=len(r['bad'])-len(new)
        # A stop where nothing was judged was not looked at; that is a failure, not a pass.
        check(f'{name}, {t}: text, icons and field edges stand out',r['n']>0 and not r['bad'],
              (new+([f'and {old} listed at an earlier stop'] if old else [])) or f"{r['n']} judged, {len(r['unknown'])} not judged, {r['off']} switched off")
    theme(themes[0])
try:
    op=admin();api(op,'flags',{'flags':{'moderation':True,'catalogEditor':True}},'PUT');api(op,'me',{'props':['nunchaku','poi']},'PUT')
    pid=api(op,'posts',{'body':'Which drill fixed your wrist?'})['posts'][0]['id']
    cid=api(op,'posts/%d/comments'%pid,{'body':'Slow figure-8s in front of a mirror.'})['comment']['id']
    other=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(other,'signup',{'username':'rae','password':'flowa-test-only'});api(other,'me',{'props':['nunchaku']},'PUT')
    op2=api(other,'posts',{'body':'First clean upspin today'})['posts'][0]['id']
    api(other,'posts/%d/comments'%pid,{'body':'A mirror, yes.','parentId':cid})
    start(1440,1100)
    # The arithmetic and the walk, on the pieces made for the purpose: every answer is known beforehand.
    js("const d=document.createElement('div');d.id='ct-probe';d.innerHTML=arguments[0];document.body.append(d)",PROBE)
    parse=js(LIB+"return ['rgb(1, 2, 3)','rgba(1, 2, 3, 0.5)','rgb(1 2 3 / 50%)','color(srgb 0.2 0.4 1 / 0.25)','color(srgb 1 0 0)','#abc','#0a141e80','transparent','oklch(0.5 0.1 20)'].map(P)")
    check('a colour is read from rgb(), rgba(), color(srgb), hex and transparent, and no other way of writing one is guessed at',
          parse[:6]==[[1,2,3,1],[1,2,3,0.5],[1,2,3,0.5],[51,102,255,0.25],[255,0,0,1],[170,187,204,1]] and parse[6][:3]==[10,20,30] and abs(parse[6][3]-128/255)<1e-9 and parse[7]==[0,0,0,0] and parse[8] is None,lambda: parse)
    r=js(CHECK,'#ct-probe',True);got={row[6]:row for row in r['all']};val=lambda i: got[i][4] if i in got else None
    check('black on white is 21:1, #777 on white 4.48:1 and #767676 on white 4.54:1',[val('k21'),val('f777'),val('p767')]==[21,4.48,4.54],lambda: got)
    check('half white over black is the grey between them, as text and as a background',val('half')==val('veil')==ratio(H,K)==5.28 and got['veil'][3]=='#808080',lambda: (got.get('half'),got.get('veil'),ratio(H,K)))
    check('text of 24 px, or of 18.66 px in bold, needs 3:1 and smaller text 4.5:1',[got[i][4:6] for i in ('big24','big18','f18','f18b')]==[[ratio(G,W),3],[ratio(G,W),3],[ratio(G,W),4.5],[ratio(G,W),4.5]] and ratio(G,W)==3.03,lambda: got)
    check('a background set on an ancestor is the one behind the text',val('inh')==ratio(W,(0x12,0x3f,0x7a))==10.41,lambda: got.get('inh'))
    check('text that an ancestor fades is as faint as it looks',val('ffade')==ratio(H,W),lambda: (got.get('ffade'),ratio(H,W)))
    check('an icon is judged by its stroke, or its fill when it has no stroke, at 3:1',[val('ic3'),val('icfill'),val('fic')]==[ratio(G,W),ratio(G,W),ratio((0x99,)*3,W)],lambda: got)
    check('a field is found by its border, by a line under it or by its fill, at 3:1',[got[i][1:6] for i in ('ed3','edline','edfill','fed') if got[i][1]=='edge']==[['edge','#949494','#ffffff',3.03,3],['edge','#949494','#ffffff',3.03,3],['edge','#123f7a','#ffffff',10.41,3],['edge','#cccccc','#ffffff',ratio((0xcc,)*3,W),3]],lambda: got)
    ph={row[6]:row[4] for row in r['all'] if row[1]=='placeholder'}
    check('a placeholder is judged as text',ph=={'pph':4.54,'fph':3.03},lambda: ph)
    bad=sorted(row[6] for row in r['all'] if row[4]<row[5]);unk=sorted(u[0].split(' ')[0]+': '+u[1] for u in r['unknown'])
    # f18 and f18b are the same pair of colours at the same need, so the list of failures names it once.
    check('each piece that must fail is found and no other',bad==sorted(('f777','f18','f18b','ffade','fic','fed','fph')) and len(r['bad'])==6,lambda: (bad,r['bad']))
    check('text on a gradient or over a canvas is left unjudged and said so, a switched off button and a checkbox are counted apart',
          len(unk)==2 and 'gradient' in unk[0]+unk[1] and 'over canvas' in unk[0]+unk[1] and r['off']==1 and r['native']==1,lambda: (unk,r['off'],r['native']))
    js("document.querySelector('#ct-probe').remove()")
    # Signed out.
    page('landing','#/')
    page('sign in','#/signin')
    page('skill maps, signed out','#/maps')
    signin()
    # The switch on the page sets the theme the way this walk does: by data-theme on the root element.
    nav('#/settings');click('[data-act=theme][data-v=light]')
    check('the theme switch sets data-theme on the page, which is what this walk sets',lambda: js("return document.documentElement.dataset.theme")=='light')
    click('[data-act=theme][data-v=dark]')
    # Some progress, so the maps, the heatmap and the quests show every level.
    for sid,v in [('F1',4),('F2',3),('F3',2),('F4',1)]:
        nav(f'#/prop/nunchaku/F/{sid}');js("for(const b of document.querySelectorAll(`[data-act=level][data-id=${arguments[0]}][data-v='${arguments[1]}']`))b.click()",sid,v);idle()
    page('dashboard','#/')
    page('skill maps','#/maps')
    for v in js("return [...document.querySelectorAll('[data-act=view]')].map(b=>b.dataset.v)"): page(f'skill maps, {v} view',act=f"document.querySelector('[data-act=view][data-v={v}]').click()")
    page('skill map of another prop','#/prop/poi')
    page('branch graph','#/prop/nunchaku/F')
    page('skill guide','#/prop/nunchaku/F/F5')
    nav('#/combos');page('combos, none yet')
    js("const f=document.querySelector('#combo-form');f.name.value='Sample combo';f.requestSubmit()");idle()
    for sid in ['F5','F7','R1']:
        js("const s=document.querySelector('[data-pick=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()",sid);idle()
    page('combos, a combo open','#/combos')
    nav('#/practice')
    js("const s=document.querySelector('[data-pick=focus-add]');s.value='F7';s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()");idle()
    page('practice, a skill chosen')
    js("const f=document.querySelector('#log-form');f.minutes.value='35';f.requestSubmit()")
    wait_for("return document.querySelector('#toast').textContent==='Session saved.'",'the session to be saved');idle()
    page('practice, a session logged and the message shown','#/practice')
    page('plan','#/plan')
    page('timeline','#/timeline')
    page('composer with the skill picker',act="document.querySelector('[data-act=pick-open]').click()")
    js("document.querySelector('[data-act=pick-add]').click()");idle()
    page('composer with a skill tagged')
    js("document.querySelector('[data-act=tag-rm]').click()")
    page('post with replies',f'#/post/{pid}')
    page('reply form',act="document.querySelector('[data-act=cmt-reply]').click()")
    js("document.querySelector('[data-act=reply-cancel]')?.click()")
    page('notifications',act="document.querySelector('#bell [data-act=bell]').click()")
    js("document.querySelector('#bell [data-act=bell]').click()")
    page('achievements','#/achievements')
    page('share dialog',act="document.querySelector('[data-act=share-streak]').click()")
    js("document.querySelector('[data-act=modal-close]')?.click()")
    nav(f'#/post/{op2}');js("document.querySelector('.post [data-act=bookmark]').click()");idle()
    page('bookmarks','#/bookmarks')
    page('profile','#/profile')
    page('public profile','#/u/rae')
    page('settings','#/settings')
    page('avatar editor','#/avatar')
    for t in js("return [...document.querySelectorAll('.av-tabs [role=tab]')].map(b=>b.dataset.v)"): page(f'avatar editor, {t}',act=f"document.querySelector('.av-tabs [data-v={t}]').click()")
    page('search',act="openPalette()")
    js("document.querySelector('#pal-q').value='spin';document.querySelector('#pal-q').dispatchEvent(new Event('input',{bubbles:true}))");page('search results')
    js("closePalette()")
    nav(f'#/post/{op2}')
    page('report dialog',act="document.querySelector('[data-act=report]').click()")
    js("document.querySelector('#report-form').reason.value='spam';document.querySelector('#report-form').requestSubmit()")
    wait_for("return document.querySelector('#modal').hidden",'the report to be sent');idle()
    # Staff pages.
    page('moderation queue','#/mod')
    page('moderation, action dialog',act="document.querySelector('[data-act=mod-open]').click()")
    js("document.querySelector('[data-act=modal-close]')?.click()")
    page('moderation, history','#/mod/history')
    page('admin dashboard','#/admin')
    page('admin accounts','#/admin/accounts')
    page('admin features','#/admin/features')
    page('admin audit log','#/admin/audit')
    page('skill map editor, list of maps','#/admin/maps')
    nav('#/admin/maps/poi');wait_for("return document.querySelectorAll('.ed-skill').length>0",'the editor to list the skills',20)
    for v in js("return [...document.querySelectorAll('[data-act=ed-view]')].map(b=>b.dataset.v)"):
        page(f'skill map editor, {v}',act=f"document.querySelector('[data-act=ed-view][data-v={v}]').click()")
    # A narrow window, where the menu is a drawer over the page.
    cmd('WebDriver:SetWindowRect',width=700,height=900)
    wait_for("return innerWidth<=820&&getComputedStyle(document.querySelector('.menu-btn')).display!=='none'",'the window to be narrow enough for the menu button')
    page('dashboard, narrow window','#/')
    page('menu, narrow window',act="document.querySelector('.menu-btn').click()")
    js("document.body.classList.remove('nav-open')")
    # Someone new: the page that asks which props they spin, and the guided tour, which is turned on for
    # them alone (it would cover the pages above). The tour takes its colours from the theme when it opens,
    # so it is opened once in each theme, by a new member each time.
    api(op,'flags',{'flags':{'tour':True,'avatars':False}},'PUT')
    cmd('WebDriver:SetWindowRect',width=1440,height=1100)
    TOUR="[...document.querySelectorAll('[role=alertdialog] button')]"
    for t in ('dark','light'):
        js("return fetch('api/signout',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}).then(r=>r.status)")
        js("localStorage.setItem('nf-theme',arguments[0])",t);load(BASE+f'?new={t}#/signup')
        wait_for("return !!document.querySelector('#auth-form')",'the form for a new account',20)
        page('create an account',themes=(t,))
        js("const f=document.querySelector('#auth-form');f.elements.username.value=arguments[0];f.elements.password.value=f.elements.again.value='flowa-test-only';f.requestSubmit()",'new'+t)
        wait_for(lambda: js("return (document.querySelector('.nav-profile .tiny')||{}).textContent")=='@new'+t,f'@new{t} to be signed in',30);idle()
        page('choosing props, first visit',themes=(t,))
        js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()")
        wait_for(f"return {TOUR}.length>0",'the guided tour to open',30);idle()
        page('guided tour',themes=(t,))
        js(f"{TOUR}.find(b=>b.textContent.startsWith('Next')).click()")
        wait_for(f"return {TOUR}.some(b=>b.textContent.startsWith('Back'))",'the second step of the tour');idle()
        page('guided tour, second step',themes=(t,))
    print(f"{len(stops)} stops in 2 themes: {tot['n']} pairs of colours judged, {tot['bad']} below their contrast ({len(listed)} different), {tot['unknown']} not judged, {tot['off']} on controls that are switched off, {tot['native']} controls the browser draws",flush=True)
    # What was not judged is said, once per kind, never passed: it shows as skipped.
    for k,(n,first) in sorted(unknown.items()): print(f'SKIP not judged, {n} times, first at {first}: {k}',flush=True)
    if os.environ.get('CONTRAST_LIST'):
        for (t,kind,f,b),(v,need,d,name) in sorted(pairs.items(),key=lambda p: p[1][0]/p[1][1]): print(f'PAIR {t} {kind} {f} on {b} {v}:1 of {need}:1 {d} ({name})')
finally: done()
