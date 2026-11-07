# Profiles: bio and social links, saved as handles and shown on the public profile page.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
def get(op,path): return json.loads(op.open(BASE+'api/'+path,timeout=20).read())
def user2():
    op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    api(op,'signup',{'username':'sam','password':'flowa-test-only','displayName':'Sam Spins'});api(op,'me',{'props':['poi']},'PUT');return op
def refused(*a):
    # True when the server answers a 4xx (the helper raises on it).
    try: api(*a);return False
    except urllib.error.HTTPError as e: return 400<=e.code<500
def wait(fn,n=20,dt=.5):
    for _ in range(n):
        v=fn()
        if v: return v
        time.sleep(dt)
    return fn()
try:
    op=admin();sam=user2();start();signin()
    if js("return !!document.querySelector('input[name=prop]')"): js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()");time.sleep(3.5)
    # Profile: bio and social links, normalised to handles.
    nav('#/profile',1.5)
    js("const f=document.querySelector('#about-form');f.bio.value='Nunchaku since 2024. Working toward a 90 s routine. @sam taught me the wrist roll.';f.instagram.value='https://www.instagram.com/flowa_test/?hl=en';f.tiktok.value='@flowa.tok';f.youtube.value='youtube.com/@FlowaTube';f.requestSubmit()");time.sleep(1.5)
    me=get(op,'me')['user']
    check('bio and links save, with URLs reduced to handles',me['bio'].startswith('Nunchaku since') and me['links']=={'instagram':'flowa_test','tiktok':'flowa.tok','youtube':'FlowaTube'},me.get('links'))
    check('a bad link is refused',refused(op,'me',{'links':{'instagram':'not a handle'}},'PUT'))
    nav('#/u/admin',2)
    hrefs=js("return [...document.querySelectorAll('.prof-head a[target=_blank]')].map(a=>a.href)")
    check('the public profile shows the bio and three social buttons','Nunchaku since' in txt('.prof-bio') and hrefs==['https://www.instagram.com/flowa_test/','https://www.tiktok.com/@flowa.tok','https://www.youtube.com/@FlowaTube'],hrefs)
    check('a mention in the bio links to that profile',js("return document.querySelector('.prof-bio a[href=\"#/u/sam\"]')!==null"))
finally:
    done()
