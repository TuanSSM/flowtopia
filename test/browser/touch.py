# Phones: with a touch screen (a coarse pointer) at phone width, every control a finger has to hit is at
# least 44 x 44 px, no page scrolls sideways, and the words do not ask for a keyboard.
import sys,os
os.environ['FLOWA_POINTER']='coarse'
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
# Every visible control with its box. A link inside a sentence is left out: its height is the line's, and
# making it taller would pull the lines apart. A tick box or radio button is measured by its label.
MEASURE=r"""
const out=[],seen=new Set();
for(let e of document.querySelectorAll('a[href],button,select,input,textarea,summary,label.btn,[data-act],[role=button],[role=tab]')){
  if(e.matches('input[type=hidden],input[type=file]'))continue;
  if(e.matches('input[type=checkbox],input[type=radio]')&&e.closest('label'))e=e.closest('label');
  if(seen.has(e)||!e.getClientRects().length)continue;seen.add(e);
  const cs=getComputedStyle(e);if(cs.visibility==='hidden'||cs.pointerEvents==='none')continue;
  if(e.tagName==='A'&&cs.display==='inline'&&e.parentElement.textContent.trim().length>e.textContent.trim().length+1)continue;
  const r=e.getBoundingClientRect();
  if(r.width<43.5||r.height<43.5)out.push(e.tagName.toLowerCase()+(e.className&&e.className.baseVal===undefined?'.'+String(e.className).trim().split(/\s+/).join('.'):'')+(e.dataset.act?'[act='+e.dataset.act+']':'')+' "'+(e.getAttribute('aria-label')||e.textContent||'').trim().slice(0,18)+'" '+Math.round(r.width)+'x'+Math.round(r.height));
}
return out;
"""
kinds=set()
def page(name,h=None,wait=1.5,act=None):
    if h is not None: nav(h,wait)
    if act: js(act);time.sleep(1)
    small=js(MEASURE);kinds.update(x.split(' "')[0]+'  '+x.rsplit(' ',1)[1] for x in small);wide=js("return document.documentElement.scrollWidth-innerWidth")
    check(f'{name}: every control is at least 44 x 44 px',not small,small[:6] if len(small)<=6 else small[:6]+[f'and {len(small)-6} more'])
    check(f'{name}: the page does not scroll sideways',wide<=0,wide)
try:
    op=admin();api(op,'flags',{'flags':{'moderation':True,'avatars':True}},'PUT');api(op,'me',{'props':['nunchaku','poi']},'PUT')
    pid=api(op,'posts',{'body':'Which drill fixed your wrist?'})['posts'][0]['id']
    cid=api(op,'posts/%d/comments'%pid,{'body':'Slow figure-8s in front of a mirror.'})['comment']['id']
    api(op,'posts/%d/comments'%pid,{'body':'And a lighter pair to start.','parentId':cid})
    other=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(other,'signup',{'username':'rae','password':'flowa-test-only'});api(other,'me',{'props':['nunchaku']},'PUT')
    op2=api(other,'posts',{'body':'First clean upspin today'})['posts'][0]['id'];api(other,'posts/%d/comments'%pid,{'body':'A mirror, yes.'})
    start(450,900);wide=phone(390)
    check('the browser reports a touch screen within 4 px of 390 px wide',js("return matchMedia('(pointer: coarse)').matches&&matchMedia('(hover: none)').matches")and abs(wide-390)<=4,wide)
    # Signed out.
    page('landing','#/',2.5)
    land=js("return document.querySelector('#main').innerText")
    check('the landing page names the Search button, not a keyboard shortcut','Ctrl' not in land and 'Search button' in land,land[:300])
    check('the search button shows no keyboard shortcut on a touch screen',js("const k=document.querySelector('.search-btn kbd');return !k||getComputedStyle(k).display==='none'"))
    live=[s for s in json.load(urllib.request.urlopen(BASE+'catalog/nunchaku.json'))['skills'] if not s.get('retired')]
    dots=js("return document.querySelectorAll('.land .map-thumb i').length")
    check('the landing page shows the skill map as a thumbnail, a dot for each skill, linked to the map',dots==len(live) and js("return document.querySelector('.land .map-thumb').getAttribute('href')")=='#/prop/nunchaku',(dots,len(live)))
    # The demo is drawn when its clip has loaded: the picture then holds more than the one background colour.
    drawn=lambda: js("const c=document.querySelector('.land #demo-cv');if(!c)return false;const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data,s=new Set();for(let i=0;i<d.length;i+=4*997)s.add(d[i]+','+d[i+1]+','+d[i+2]);return s.size>3")
    for _ in range(40):
        if drawn(): break
        time.sleep(.5)
    check('the landing page plays one demo, the signature move',drawn() and txt('.land h2')=='Nunchaku: '+next(s['name'] for s in live if s['id']=='F5') and js("return document.querySelectorAll('.land canvas').length")==1,txt('.land h2'))
    click('.land [data-k=play]');time.sleep(.8)
    check('its controls work on the landing page',txt('.land [data-k=play]')=='Play' and drawn(),txt('.land [data-k=play]'))
    page('sign in','#/signin')
    page('skill maps, signed out','#/maps',2)
    signin()
    page('dashboard','#/',2)
    page('menu',act="document.querySelector('.menu-btn').click()")
    js("document.body.classList.remove('nav-open')")
    page('skill maps','#/maps',2)
    page('branch graph','#/prop/nunchaku/F',2)
    page('skill guide','#/prop/nunchaku/F/F5',3)
    nav('#/combos',2);js("const f=document.querySelector('#combo-form');f.name.value='Sample combo';f.requestSubmit()");time.sleep(1.2)
    for sid in ['F5','F7','R1']:
        js("const s=document.querySelector('[data-pick=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()",sid);time.sleep(1)
    page('combos','#/combos',2.5)
    page('practice','#/practice',2)
    page('plan','#/plan',2)
    page('timeline','#/timeline',3)
    page('post with replies',f'#/post/{pid}',3)
    page('notifications',act="document.querySelector('#bell').click()")
    js("document.querySelector('#bell').click()")
    page('achievements','#/achievements',2.5)
    page('bookmarks','#/bookmarks',2)
    page('profile','#/profile',2)
    page('public profile','#/u/rae',2.5)
    page('settings','#/settings',2)
    page('avatar editor','#/avatar',4)
    # Every tab of the editor, at two phone widths: rows of colours and options wrap, and nothing is wider than the screen.
    tabs=js("return [...document.querySelectorAll('.av-tabs [role=tab]')].map(b=>b.dataset.v)")
    for width in (360,390):
        got=phone(width);nav('#/avatar',2)
        for t in tabs: page(f'avatar editor, {t}, {got} px',act=f"document.querySelector('.av-tabs [data-v={t}]').click()")
    page('search',act="openPalette()")
    js("document.querySelector('#pal-q').value='spin';document.querySelector('#pal-q').dispatchEvent(new Event('input',{bubbles:true}))");page('search results')
    js("closePalette()")
    # Forms and dialogs that open on a page.
    nav(f'#/post/{pid}',3)
    page('reply form',act="document.querySelector('[data-act=cmt-reply]').click()")
    js("document.querySelector('[data-act=reply-cancel]')?.click()")
    nav(f'#/post/{op2}',3)
    page('report dialog',act="document.querySelector('[data-act=report]').click()")
    js("document.querySelector('[data-act=modal-close]')?.click()")
    nav('#/timeline',3)
    page('skill picker',act="document.querySelector('[data-act=pick-open]').click()")
    js("document.querySelector('[data-act=pick-close]')?.click()")
    page('moderation','#/mod',3)
    page('admin','#/admin',3)
    page('admin accounts','#/admin/accounts',3)
    # TOUCH_LIST=1 lists each kind of control that is too small, once, to work through.
    if os.environ.get('TOUCH_LIST'): print('\n'.join(sorted(kinds)))
finally: done()
