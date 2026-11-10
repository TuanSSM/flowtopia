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
def compose(text):
    wait_for("return !!document.querySelector('#compose-form textarea')",'the composer to show',20)
    js("const t=document.querySelector('#compose-form textarea');t.focus();t.value=arguments[0];t.dispatchEvent(new Event('input',{bubbles:true}))",text);idle()
SITES=('youtube.com','youtu.be','ytimg.com','tiktok','instagram','cdninstagram','fbcdn')
try:
    if not os.environ.get('FLOWA_PREVIEW_BASE'): print('SKIP link cards: FLOWA_PREVIEW_BASE is not set, so the server cannot reach the stub')
    else:
        srv=stub.start()
        op=admin();api(op,'me',{'props':['nunchaku']},'PUT');api(op,'flags',{'flags':{'moderation':True,'videoEmbeds':False}},'PUT')  # play in place is on by default; the test turns it on itself
        start();signin();nav('#/timeline')
        compose('New drill video https://youtu.be/dQw4w9WgXcQ?si=share123 have a look')
        check('a pasted YouTube link is offered as a card',js("return !document.querySelector('.link-offer').hidden") and 'YouTube link found' in txt('.link-offer'))
        click('[data-act=link-attach]');idle()
        check('attaching moves the link out of the text into a chip',js("return document.querySelectorAll('.draft-chips .link-chip').length")==1 and 'youtu.be' not in js("return document.querySelector('#compose-form textarea').value"))
        js("document.querySelector('#compose-form').requestSubmit()")
        pid=wait_for("return [...document.querySelectorAll('.post')].find(p=>p.textContent.includes('New drill video'))?.dataset.post",'the new post to show',20)
        sel=f'.post[data-post="{pid}"] .lc'
        # The server fetches the preview after the post is saved, and the page asks again every 3 s until it has it.
        ok=poll(lambda: js("const c=document.querySelector(arguments[0]);return c&&c.querySelector('.lc-title')&&c.querySelector('img')?[c.querySelector('.lc-title').textContent,c.querySelector('img').getAttribute('src')]:null",sel),20)[0]
        check('the card shows the title and a thumbnail served by Flowa',bool(ok) and ok[0]=='Six directions drill' and ok[1].startswith('api/previews/'),ok)
        check('the card links to the canonical address, in a new tab',js("const a=document.querySelector(arguments[0]+' a.lc-meta');return [a.href,a.target,a.rel]",sel)==['https://www.youtube.com/watch?v=dQw4w9WgXcQ','_blank','noopener noreferrer'])
        # A TikTok short link resolves to its video, and an Instagram reel; both are tall cards.
        p2=api(op,'posts',{'body':'More','links':['https://vm.tiktok.com/ZMabc1234/','https://www.instagram.com/reel/C9xYz_-AbCd/']})['posts'][0]['id']
        nav('#/practice');nav('#/timeline')
        cards=poll(lambda: (lambda c: c if c and all(x['status']=='ok' for x in c) else None)(get(op,f'api/posts/{p2}')['posts'][0].get('links')),20)[0]
        check('a TikTok short link and an Instagram reel get previews',bool(cards) and [c['title'] for c in cards]==['Poi flowers, slow','Staff spins on the beach'] and all(c['ratio']=='9:16' for c in cards),cards)
        nav('#/practice');nav('#/timeline')
        check('tall videos get tall cards',lambda: js("return document.querySelectorAll('.post[data-post=\"'+arguments[0]+'\"] .lc-media.tall img').length",str(p2))==2,timeout=20)
        leaks=js("return performance.getEntriesByType('resource').map(e=>e.name).filter(u=>arguments[0].some(s=>new URL(u).hostname.includes(s)))",list(SITES))
        check('the browser made no request to Instagram, TikTok or YouTube',leaks==[],leaks[:3])
        # Play in place only with the flag; the frame comes from youtube-nocookie.
        check('without the flag there is no play button',not js("return !!document.querySelector('[data-act=yt-play]')"))
        api(op,'flags',{'flags':{'videoEmbeds':True}},'PUT')
        csp=js("return fetch('/').then(r=>r.headers.get('Content-Security-Policy'))")
        check('the flag adds youtube-nocookie as a frame source and nothing else','frame-src https://www.youtube-nocookie.com' in csp and csp.count('https://')==1,csp)
        load(BASE+'?embeds=1#/timeline')
        check('before pressing play there is no frame',js("return document.querySelectorAll('iframe').length")==0)
        wait_for(lambda: js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=yt-play]')",str(pid)),'the play button on the card',20)
        js("document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=yt-play]').click()",str(pid));idle()
        src=js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] iframe');return f&&[f.getAttribute('src'),f.getAttribute('referrerpolicy')]",str(pid))
        check('pressing play loads the video from youtube-nocookie',src==['https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?autoplay=1','no-referrer'],src)
        # Hiding a post stops its preview being served to others.
        def account(name):
            o=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            api(o,'signup',{'username':name,'password':'flowa-test-only'});api(o,'me',{'props':['nunchaku']},'PUT');return o
        poster,viewer,mo=account('poster'),account('viewer'),account('moe')
        api(op,f"admin/users/{get(mo,'api/me')['user']['id']}/role",{'role':'moderator'},'PUT')
        hp=api(poster,'posts',{'body':'my clip','links':['https://youtu.be/aBcDeFgHiJk']})['posts'][0]['id']
        card=poll(lambda: (lambda c: c if c and c[0]['status']=='ok' else None)(get(poster,f'api/posts/{hp}')['posts'][0].get('links')),20)[0]
        thumb=card[0]['thumb'] if card else ''
        before=code(viewer,thumb) if thumb else 0
        api(mo,'mod/actions',{'action':'hide','kind':'post','id':hp,'reason':'spam'})
        check('a hidden post’s preview is no longer served to others',before==200 and code(viewer,thumb)==404,(before,code(viewer,thumb) if thumb else None))
        # A link that was attached and then taken off is not lost, and only a link the server takes is offered.
        load(BASE+'?draft=1#/timeline')
        offer=lambda: js("const r=document.querySelector('.link-offer');return r.hidden?'':r.textContent.trim()")
        draft=lambda: js("return [document.querySelector('#compose-form textarea').value,document.querySelectorAll('.draft-chips .link-chip').length]")
        compose('Watch this (https://youtu.be/dQw4w9WgXcQ).')
        check('a link at the end of a sentence is offered without the bracket and the full stop','YouTube link found' in offer(),offer())
        click('[data-act=link-attach]');idle()
        check('attaching takes exactly the link out of the text',draft()==['Watch this ().',1],draft())
        click('[data-act=link-rm]');idle()
        check('removing the card puts the link back in the text and offers it again',draft()==['Watch this (). https://youtu.be/dQw4w9WgXcQ',0] and 'YouTube link found' in offer(),(draft(),offer()))
        click('[data-act=link-attach]');idle()
        compose('Same one again https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=5')
        check('the same video written another way is not offered twice',offer()=='' and draft()[1]==1,(offer(),draft()))
        click('[data-act=link-rm]');idle()
        compose('')
        for bad,why in (('https://www.youtube.com/watch?v=short','a video id of the wrong length'),('https://youtube.com:8443/watch?v=dQw4w9WgXcQ','a port in the address'),('https://www.instagram.com/flowa_test/','a profile, not a post')):
            compose('Look '+bad)
            check(f'no card is offered for {why}',offer()=='',offer())
        compose('')
        try: api(op,'posts',{'body':'x','links':['https://www.youtube.com/watch?v=short']});named=None
        except urllib.error.HTTPError as e: named=(e.code,json.loads(e.read()).get('error',''))
        check('a link the server refuses is named in the answer',bool(named) and named[0]==400 and 'https://www.youtube.com/watch?v=short' in named[1] and named[1].startswith('This link cannot be a card'),named)
        # With link cards off, the composer offers nothing.
        api(op,'flags',{'flags':{'linkCards':False}},'PUT')
        load(BASE+'?off=1#/timeline')
        compose('https://youtu.be/dQw4w9WgXcQ')
        check('with link cards off no card is offered',js("return document.querySelector('.link-offer').hidden"))
finally:
    done()
