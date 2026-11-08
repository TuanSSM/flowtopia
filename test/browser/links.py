# Link cards: offering a pasted link, cards with previews the server fetched (from the stub in
# stub.py, so run with FLOWA_PREVIEW_BASE=http://127.0.0.1:3199), no requests to the three sites
# from the browser, playing YouTube in place, hiding, and the flag.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
import stub
def get(op,path): return json.loads(op.open(BASE+path,timeout=20).read())
def code(op,path):
    try: op.open(BASE+path,timeout=20);return 200
    except urllib.error.HTTPError as e: return e.code
def wait(fn,n=30,dt=.5):
    for _ in range(n):
        v=fn()
        if v: return v
        time.sleep(dt)
    return fn()
HOOK="window.__e=[];window.addEventListener('error',e=>__e.push(e.message+' @'+(e.filename||'').split('/').pop()+':'+e.lineno));window.addEventListener('unhandledrejection',e=>__e.push('rej '+String(e.reason)));document.addEventListener('securitypolicyviolation',e=>__e.push('CSP '+e.violatedDirective))"
def reload(url,t=3.5):
    # A full load replaces the page, so the error watcher from start() is put back.
    cmd('WebDriver:Navigate',url=url);time.sleep(t);js(HOOK)
def compose(text):
    wait(lambda: js("return !!document.querySelector('#compose-form textarea')"),n=40)
    js("const t=document.querySelector('#compose-form textarea');t.focus();t.value=arguments[0];t.dispatchEvent(new Event('input',{bubbles:true}))",text);time.sleep(.6)
SITES=('youtube.com','youtu.be','ytimg.com','tiktok','instagram','cdninstagram','fbcdn')
try:
    if not os.environ.get('FLOWA_PREVIEW_BASE'): print('SKIP link cards: FLOWA_PREVIEW_BASE is not set, so the server cannot reach the stub')
    else:
        srv=stub.start()
        op=admin();api(op,'me',{'props':['nunchaku']},'PUT');api(op,'flags',{'flags':{'moderation':True}},'PUT')
        start();signin();nav('#/timeline',2.5)
        compose('New drill video https://youtu.be/dQw4w9WgXcQ?si=share123 have a look')
        check('a pasted YouTube link is offered as a card',js("return !document.querySelector('.link-offer').hidden") and 'YouTube link found' in txt('.link-offer'))
        click('[data-act=link-attach]');time.sleep(.6)
        check('attaching moves the link out of the text into a chip',js("return document.querySelectorAll('.draft-chips .link-chip').length")==1 and 'youtu.be' not in js("return document.querySelector('#compose-form textarea').value"))
        js("document.querySelector('#compose-form').requestSubmit()");time.sleep(1.5)
        pid=wait(lambda: js("return [...document.querySelectorAll('.post')].find(p=>p.textContent.includes('New drill video'))?.dataset.post"))
        sel=f'.post[data-post="{pid}"] .lc'
        ok=wait(lambda: js("const c=document.querySelector(arguments[0]);return c&&c.querySelector('.lc-title')&&c.querySelector('img')?[c.querySelector('.lc-title').textContent,c.querySelector('img').getAttribute('src')]:null",sel),n=40)
        check('the card shows the title and a thumbnail served by Flowa',bool(ok) and ok[0]=='Six directions drill' and ok[1].startswith('api/previews/'),ok)
        check('the card links to the canonical address, in a new tab',js("const a=document.querySelector(arguments[0]+' a.lc-meta');return [a.href,a.target,a.rel]",sel)==['https://www.youtube.com/watch?v=dQw4w9WgXcQ','_blank','noopener noreferrer'])
        # A TikTok short link resolves to its video, and an Instagram reel; both are tall cards.
        p2=api(op,'posts',{'body':'More','links':['https://vm.tiktok.com/ZMabc1234/','https://www.instagram.com/reel/C9xYz_-AbCd/']})['posts'][0]['id']
        nav('#/practice',.6);nav('#/timeline',2)
        cards=wait(lambda: (lambda c: c if c and all(x['status']=='ok' for x in c) else None)(get(op,f'api/posts/{p2}')['posts'][0].get('links')),n=40)
        check('a TikTok short link and an Instagram reel get previews',bool(cards) and [c['title'] for c in cards]==['Poi flowers, slow','Staff spins on the beach'] and all(c['ratio']=='9:16' for c in cards),cards)
        nav('#/practice',.6);nav('#/timeline',3)
        check('tall videos get tall cards',wait(lambda: js("return document.querySelectorAll('.post[data-post=\"'+arguments[0]+'\"] .lc-media.tall img').length",str(p2))==2))
        leaks=js("return performance.getEntriesByType('resource').map(e=>e.name).filter(u=>arguments[0].some(s=>new URL(u).hostname.includes(s)))",list(SITES))
        check('the browser made no request to Instagram, TikTok or YouTube',leaks==[],leaks[:3])
        # Play in place only with the flag; the frame comes from youtube-nocookie.
        check('without the flag there is no play button',not js("return !!document.querySelector('[data-act=yt-play]')"))
        api(op,'flags',{'flags':{'videoEmbeds':True}},'PUT');time.sleep(2.2)
        csp=js("return fetch('/').then(r=>r.headers.get('Content-Security-Policy'))")
        check('the flag adds youtube-nocookie as a frame source and nothing else','frame-src https://www.youtube-nocookie.com' in csp and csp.count('https://')==1,csp)
        reload(BASE+'?embeds=1#/timeline')
        check('before pressing play there is no frame',js("return document.querySelectorAll('iframe').length")==0)
        wait(lambda: js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=yt-play]')",str(pid)),n=40)
        js("document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=yt-play]').click()",str(pid));time.sleep(1)
        src=js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] iframe');return f&&[f.getAttribute('src'),f.getAttribute('referrerpolicy')]",str(pid))
        check('pressing play loads the video from youtube-nocookie',src==['https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?autoplay=1','no-referrer'],src)
        # Hiding a post stops its preview being served to others.
        def account(name):
            o=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            api(o,'signup',{'username':name,'password':'flowa-test-only'});api(o,'me',{'props':['nunchaku']},'PUT');return o
        poster,viewer,mo=account('poster'),account('viewer'),account('moe')
        api(op,f"admin/users/{get(mo,'api/me')['user']['id']}/role",{'role':'moderator'},'PUT')
        hp=api(poster,'posts',{'body':'my clip','links':['https://youtu.be/aBcDeFgHiJk']})['posts'][0]['id']
        card=wait(lambda: (lambda c: c if c and c[0]['status']=='ok' else None)(get(poster,f'api/posts/{hp}')['posts'][0].get('links')),n=40)
        thumb=card[0]['thumb'] if card else ''
        before=code(viewer,thumb) if thumb else 0
        api(mo,'mod/actions',{'action':'hide','kind':'post','id':hp,'reason':'spam'})
        check('a hidden post’s preview is no longer served to others',before==200 and code(viewer,thumb)==404,(before,code(viewer,thumb) if thumb else None))
        # With link cards off, the composer offers nothing.
        api(op,'flags',{'flags':{'linkCards':False}},'PUT');time.sleep(2.2)
        reload(BASE+'?off=1#/timeline')
        compose('https://youtu.be/dQw4w9WgXcQ')
        check('with link cards off no card is offered',js("return document.querySelector('.link-offer').hidden"))
finally:
    done()
