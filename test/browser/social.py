# Community and gamification: profile bio and links, the timeline with likes, comments and
# mentions, notifications, achievements with their milestone post, and the streak overlay.
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
def wait(fn,n=60,dt=.5):  # up to 30 s: a save, the badge sync and the post can be slow on the Pi
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
    check('the people list has both users, with a level each',js("return [...document.querySelectorAll('.person b')].map(b=>b.textContent).sort().join()")=='Admin,Sam Spins' and 'Level' in txt('.person .tiny'))
    js("const f=document.querySelector('#compose-form');f.body.value='Worked on F7 today. Thanks @sam for the tip!';f.skill.value='F7';f.requestSubmit()");time.sleep(1.5)
    post=wait(lambda: js("return [...document.querySelectorAll('.post')].find(p=>p.textContent.includes('Worked on F7'))?.dataset.post"))
    check('a post appears at once, with the skill tag and the mention linked',bool(post) and js("return document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .chip[href=\"#/prop/nunchaku/F/F7\"]')!==null&&document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .post-body a[href=\"#/u/sam\"]')!==null",post))
    n2=get(sam,'notifications')
    check('the mentioned user is notified',n2['unread']==1 and n2['items'][0]['kind']=='mention',n2)
    first=get(op,'me')
    check('the first post unlocks a badge',any(a['key']=='first-post' for a in first['achievements']),[a['key'] for a in first['achievements']])
    api(sam,f'posts/{post}/like',{});api(sam,f'posts/{post}/comments',{'body':'Nice one @admin, keep it up'})
    sp=api(sam,'posts',{'body':'Poi flowers all evening','prop':'poi','skill':'F2'})['posts'][0]['id']
    api(sam,f'posts/{post}/like',{});api(sam,f'posts/{post}/like',{})  # unlike and like again: still one notification
    # Earlier suites on the same database may already have unlocked badges, so count kinds, not items.
    mine=get(op,'notifications');kinds=[i['kind'] for i in mine['items']];unread=mine['unread']
    check('the author is told once about the like, once about the comment and about the badge, not about their own mention',kinds.count('like')==1 and kinds.count('comment')==1 and kinds.count('achievement')>=1 and 'mention' not in kinds and unread==len(kinds),kinds)
    click('[data-act=bell]');time.sleep(1.2)
    check('the bell shows the unread count and opens a panel with every notification',txt('.bell-n')==str(unread) and js("return document.querySelectorAll('.ntf-panel .ntf.unread').length")==unread and 'liked your post' in txt('.ntf-panel') and 'Said hello' in txt('.ntf-panel'),(txt('.bell-n'),unread))
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
    # Achievements: a session unlocks badges, which post a milestone and show in the bell.
    nav('#/practice',1.5)
    js("const f=document.querySelector('#log-form');f.minutes.value='35';f.requestSubmit()");time.sleep(1)
    ach=wait(lambda: [a['key'] for a in get(op,'me')['achievements'] if a['key']=='sessions-1'])
    check('logging a session unlocks First steps',ach==['sessions-1'],ach)
    check('an unlock card is shown',js("return !document.querySelector('#unlock').hidden&&document.querySelector('#unlock').textContent.includes('First steps')"))
    ms=wait(lambda: [p for p in get(op,'posts')['posts'] if p['kind']=='milestone' and 'sessions-1' in p['data']['achievements']])
    check('one milestone post is made for the batch',len(ms)==1 and 'first-post' not in ms[0]['data']['achievements'],[p['data'] for p in ms])
    check('the user is notified of the badge',any(i['kind']=='achievement' and i['text']=='sessions-1' for i in get(op,'notifications')['items']))
    stats=wait(lambda: get(op,'me')['user'].get('stats') or None)
    check('level, XP and streak are reported for the profile',stats and stats['streak']==1 and stats['xp']>=45 and stats['level']>=1 and stats['sessions']==1,stats)
    nav('#/achievements',2)
    check('the achievements page shows the badge unlocked, the level and a one-day streak',js("return [...document.querySelectorAll('.badge.on b')].map(b=>b.textContent)").count('First steps')==1 and txt('.level-ring b')=='1' and txt('.streak-card .val').startswith('1'))
    check('weekly challenges show progress',js("return document.querySelectorAll('.ch').length")==4 and '1 of 4' in txt('.ch .tiny'))
    # Plan: quests with XP.
    nav('#/plan',2)
    check('the plan shows quests with skill chips and XP on each gate',js("return document.querySelectorAll('.quest').length")==5 and js("return document.querySelectorAll('.quest.on .chip.on').length")==1 and '+150 XP' in txt('.quest .check .chip') and js("return document.querySelectorAll('.quest .qs').length")>20)
    check('the plan has an XP card, challenges and a focus skill',txt('[data-tour=xp] h2')=='Nunchaku XP' and js("return !!document.querySelector('[data-tour=challenges]')") and js("return !!document.querySelector('[data-tour=focus] .sk-row')"))
    # Streak overlay.
    nav('#/achievements',1.5);click('[data-act=share-streak]');time.sleep(1.5)
    size=js("const c=window.streakCanvas({format:'story'});const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let op=0,tr=0;for(let i=3;i<d.length;i+=4*97){if(d[i]>200)op++;else if(d[i]===0)tr++}return [c.width,c.height,op,tr]")
    check('the story overlay is 1080 by 1920 and mostly transparent with drawn pixels',size[0]==1080 and size[1]==1920 and size[2]>200 and size[3]>size[2]*3,size)
    sq=js("const c=window.streakCanvas({format:'square',bg:true});const d=c.getContext('2d').getImageData(0,0,4,4).data;return [c.width,c.height,d[3]]")
    check('the square version with a background is opaque',sq==[1080,1080,255],sq)
    shot('/tmp/flowa-test/share.png')
    # Templates and placement.
    tp=js("const out={};for(const t of ['classic','minimal','card','bold','week']){const c=window.streakCanvas({format:'story',template:t,place:'bottom'});const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let op=0,tr=0,mid=0,ys=0,n=0;for(let i=3;i<d.length;i+=4*97){const a=d[i];if(a>200)op++;else if(a===0)tr++;else mid++;if(a>0){ys+=Math.floor(i/4/c.width);n++}}out[t]=[op,tr,mid,Math.round(ys/n)]}return out")
    check('every template draws something on a mostly transparent canvas',all(v[0]>150 and v[1]>v[0]*2 for v in tp.values()),tp)
    check('the card template has a translucent panel',tp['card'][2]>400,tp['card'])
    ys=js("return ['top','middle','bottom'].map(p=>{const c=window.streakCanvas({format:'story',template:'minimal',place:p});const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let ys=0,n=0;for(let i=3;i<d.length;i+=4*97){if(d[i]>0){ys+=Math.floor(i/4/c.width);n++}}return Math.round(ys/n)})")
    check('placement moves the block from the top to the bottom',ys[0]<ys[1]<ys[2] and ys[0]<700 and ys[2]>1200,ys)
    check('the dialog offers five templates and three positions',js("return document.querySelectorAll('.tpl').length")==5 and js("return document.querySelectorAll('[data-act=share-place]').length")==3 and js("return [...document.querySelectorAll('.tpl canvas')].every(c=>c.getContext('2d').getImageData(c.width>>1,c.height>>1,1,1).data[3]>0)"))
    click('[data-act=share-tpl][data-v=bold]');time.sleep(1)
    check('choosing a template is remembered',js("return document.querySelector('.tpl.on').dataset.v")=='bold' and json.loads(js("return localStorage.getItem('fl-share')"))['tpl']=='bold')
    click('[data-act=share-tpl][data-v=classic]');time.sleep(1)
    check('the share dialog previews the overlay with download and post buttons',js("return !document.querySelector('#modal').hidden&&document.querySelector('#share-cv').width>0&&!!document.querySelector('[data-act=share-do][data-v=download]')&&!!document.querySelector('[data-act=share-do][data-v=post]')"))
    click('[data-act=share-do][data-v=post]');time.sleep(2.5)
    shared=wait(lambda: [p for p in get(op,'posts')['posts'] if p['image'].startswith('data:image/jpeg') and 'streak' in p['body']])
    check('posting the streak makes a post with the picture and unlocks Shared the flame',len(shared)==1 and any(a['key']=='share-streak' for a in get(op,'me')['achievements']),len(shared))
    check('the timeline opens on the new post',js("return location.hash")=='#/timeline' and js("return !!document.querySelector('.post .post-img')"))
    shot('/tmp/flowa-test/timeline.png')
finally:
    done()
