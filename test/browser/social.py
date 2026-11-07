# Community: profile bio and links, the timeline with likes, comments and mentions, and notifications.
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
    # Timeline: post, mention, like, comment.
    nav('#/timeline',2)
    check('the people list has both users',js("return [...document.querySelectorAll('.person b')].map(b=>b.textContent).sort().join()")=='Admin,Sam Spins' and '@' in txt('.person .tiny'))
    js("const f=document.querySelector('#compose-form');f.body.value='Worked on F7 today. Thanks @sam for the tip!';f.skill.value='F7';f.requestSubmit()");time.sleep(1.5)
    post=wait(lambda: js("return [...document.querySelectorAll('.post')].find(p=>p.textContent.includes('Worked on F7'))?.dataset.post"))
    check('a post appears at once, with the skill tag and the mention linked',bool(post) and js("return document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .chip[href=\"#/prop/nunchaku/F/F7\"]')!==null&&document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .post-body a[href=\"#/u/sam\"]')!==null",post))
    n2=get(sam,'notifications')
    check('the mentioned user is notified',n2['unread']==1 and n2['items'][0]['kind']=='mention',n2)
    api(sam,f'posts/{post}/like',{});api(sam,f'posts/{post}/comments',{'body':'Nice one @admin, keep it up'})
    sp=api(sam,'posts',{'body':'Poi flowers all evening','prop':'poi','skill':'F2'})['posts'][0]['id']
    api(sam,f'posts/{post}/like',{});api(sam,f'posts/{post}/like',{})  # unlike and like again: still one notification
    mine=get(op,'notifications')
    kinds=sorted(i['kind'] for i in mine['items'])
    check('the author is told once about the like and once about the comment, not about their own mention',kinds==['comment','like'] and mine['unread']==2,kinds)
    click('[data-act=bell]');time.sleep(1.2)
    check('the bell shows the unread count and opens a panel with both notifications',txt('.bell-n')=='2' and js("return document.querySelectorAll('.ntf-panel .ntf.unread').length")==2 and 'liked your post' in txt('.ntf-panel'),txt('.ntf-panel')[:120])
    click('[data-act=ntf-read-all]');time.sleep(1)
    check('mark all read clears the badge',js("return !document.querySelector('.bell-n')") and get(op,'notifications')['unread']==0)
    js("document.body.click()");nav('#/practice',1);nav('#/timeline',2.5)
    check("the other user's post is in the feed with its prop and skill",js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .chip[href=\"#/prop/poi/F/F2\"]')",str(sp)) and js("return document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-body')?.textContent",post).startswith('Nice one'))
    click(f'.post[data-post="{sp}"] [data-act=like]');time.sleep(1.2)
    liked=get(op,f'posts/{sp}')['posts'][0]
    check('liking updates the count and is saved',liked['liked'] and liked['likes']==1 and txt(f'.post[data-post="{sp}"] .act.on .num')=='1',liked.get('likes'))
    js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-form');f.body.value='Which flower?';f.requestSubmit()",str(sp));time.sleep(1.5)
    check('a comment from the form shows up under the post',js("return [...document.querySelectorAll('.post[data-post=\"'+arguments[0]+'\"] .cmt-body')].map(c=>c.textContent)",str(sp))==['Which flower?'] and get(sam,'notifications')['items'][0]['kind']=='comment')
    check('only the author or an admin can delete a post',refused(sam,f'posts/{post}',{},'DELETE') and not refused(op,f'posts/{sp}',{},'DELETE'))
finally:
    done()
