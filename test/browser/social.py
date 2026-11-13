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
SLOW=30  # seconds: a save, the badge sync and the post it makes can be slow on the Pi
try:
    op=admin();sam=user2();start();signin()
    if js("return !!document.querySelector('input[name=prop]')"): js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()");idle()
    # Profile: bio and social links, normalised to handles.
    nav('#/profile')
    js("const f=document.querySelector('#about-form');f.bio.value='Nunchaku since 2024. Working toward a 90 s routine. @sam taught me the wrist roll.';f.instagram.value='https://www.instagram.com/flowa_test/?hl=en';f.tiktok.value='@flowa.tok';f.youtube.value='youtube.com/@FlowaTube';f.requestSubmit()");idle()
    me=get(op,'me')['user']
    check('bio and links save, with URLs reduced to handles',me['bio'].startswith('Nunchaku since') and me['links']=={'instagram':'flowa_test','tiktok':'flowa.tok','youtube':'FlowaTube'},me.get('links'))
    check('a bad link is refused',refused(op,'me',{'links':{'instagram':'not a handle'}},'PUT'))
    nav('#/u/admin')
    hrefs=js("return [...document.querySelectorAll('.prof-head a[target=_blank]')].map(a=>a.href)")
    check('the public profile shows the bio and three social buttons','Nunchaku since' in txt('.prof-bio') and hrefs==['https://www.instagram.com/flowa_test/','https://www.tiktok.com/@flowa.tok','https://www.youtube.com/@FlowaTube'],hrefs)
    check('a mention in the bio links to that profile',js("return document.querySelector('.prof-bio a[href=\"#/u/sam\"]')!==null"))
    # Timeline: post, mention, like, comment.
    nav('#/timeline')
    check('the people list has both users, with a level each',js("return [...document.querySelectorAll('.person b')].map(b=>b.textContent).sort().join()")=='Admin,Sam Spins' and 'Level' in txt('.person .tiny'))
    # Trick chips: the picker searches every prop's skills and the user's combos.
    click('[data-act=pick-open]');idle()
    check('the trick picker opens with a search box and the props of the user',js("return document.activeElement.id")=='pick-q' and 'Nunchaku' in txt('.pick-list'))
    js("const q=document.querySelector('#pick-q');q.value='six dir';q.dispatchEvent(new Event('input',{bubbles:true}))");idle()
    check('typing filters to the matching trick',js("return [...document.querySelectorAll('.pick-i .tag')].map(t=>t.textContent)")==['F5'],js("return [...document.querySelectorAll('.pick-i .tag')].map(t=>t.textContent)"))
    js("document.querySelector('#pick-q').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}))");idle()
    js("const q=document.querySelector('#pick-q');q.value='F7';q.dispatchEvent(new Event('input',{bubbles:true}))");idle();click('.pick-i');idle()
    check('Enter and a click each add a chip, and the picker stays open for more',js("return [...document.querySelectorAll('.draft-chips .chip.trick b')].map(b=>b.textContent)")==['F5','F7'] and js("return !!document.querySelector('#pick-q')"))
    click('[data-act=pick-close]');idle()
    click('[data-act=tag-rm][data-skill=F5]');idle()
    check('a chip can be removed',js("return [...document.querySelectorAll('.draft-chips .chip.trick b')].map(b=>b.textContent)")==['F7'])
    js("const f=document.querySelector('#compose-form');f.body.value='Worked on F7 today. Thanks @sam for the tip!';f.requestSubmit()");idle()
    post=wait_for("return [...document.querySelectorAll('.post')].find(p=>p.textContent.includes('Worked on F7'))?.dataset.post",'the new post to show',SLOW)
    check('a post appears at once, with the trick chip and the mention linked',bool(post) and js("return document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .post-tags .chip[href=\"#/prop/nunchaku/F/F7\"]')!==null&&document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .post-body a[href=\"#/u/sam\"]')!==null",post))
    check('the tags are saved on the post',get(op,f'posts/{post}')['posts'][0]['data']=={'tags':[{'prop':'nunchaku','skill':'F7'}]})
    check('bad tags and combos are refused',refused(op,'posts',{'body':'x','tags':[{'prop':'drums','skill':'F1'}]}) and refused(op,'posts',{'body':'x','combo':{'name':'c','prop':'nunchaku','seq':[]}}))
    # Publish a combo from the Combos page.
    nav('#/combos');js("const f=document.querySelector('#combo-form');f.name.value='Morning loop';f.requestSubmit()");idle()
    for sid in ['F5','F7','R1']: js("const s=document.querySelector('[data-pick=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()",sid);idle()
    click('[data-act=combo-publish]');idle()
    check('Publish opens the composer with the combo attached and a line to edit',js("return location.hash")=='#/timeline' and 'Morning loop' in txt('.draft-chips .combo-chip') and js("return document.querySelector('#compose-form textarea').value").startswith('New combo: Morning loop'))
    js("document.querySelector('#compose-form').requestSubmit()");idle()
    cpost=wait_for("return [...document.querySelectorAll('.post')].find(p=>p.textContent.includes('New combo'))?.dataset.post",'the combo post to show',SLOW)
    check('the post shows the combo with its skills in order, each linking to the skill',js("return [...document.querySelectorAll('.post[data-post=\"'+arguments[0]+'\"] .combo-seq a')].map(a=>a.getAttribute('href'))",cpost)==['#/prop/nunchaku/F/F5','#/prop/nunchaku/F/F7','#/prop/nunchaku/R/R1'] and 'Reliable' in txt(f'.post[data-post="{cpost}"] .post-combo'))
    check('the owner can open the combo from the post',js("return document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .post-combo a.small')?.getAttribute('href')",cpost).startswith('#/combos/'))
    api(op,f'posts/{cpost}',{},'DELETE')
    n2=get(sam,'notifications')
    check('the mentioned user is notified',n2['unread']==1 and n2['items'][0]['kind']=='mention',n2)
    first=get(op,'me')
    check('the first post unlocks a badge',any(a['key']=='first-post' for a in first['achievements']),[a['key'] for a in first['achievements']])
    api(sam,f'posts/{post}/like',{});api(sam,f'posts/{post}/comments',{'body':'Nice one @admin, keep it up'})
    sp=api(sam,'posts',{'body':'Poi flowers all evening','tags':[{'prop':'poi','skill':'F2'}]})['posts'][0]['id']
    api(sam,f'posts/{post}/like',{});api(sam,f'posts/{post}/like',{})  # unlike and like again: still one notification
    # Earlier suites on the same database may already have unlocked badges, so count kinds, not items.
    mine=get(op,'notifications');kinds=[i['kind'] for i in mine['items']];unread=mine['unread']
    check('the author is told once about the like, once about the comment and about the badge, not about their own mention',kinds.count('like')==1 and kinds.count('comment')==1 and kinds.count('achievement')>=1 and 'mention' not in kinds and unread==len(kinds),kinds)
    click('[data-act=bell]');idle()
    check('the bell shows the unread count and opens a panel with every notification',txt('.bell-n')==str(unread) and js("return document.querySelectorAll('.ntf-panel .ntf.unread').length")==unread and 'liked your post' in txt('.ntf-panel') and 'Said hello' in txt('.ntf-panel'),(txt('.bell-n'),unread))
    click('[data-act=ntf-read-all]');idle()
    check('mark all read clears the badge',js("return !document.querySelector('.bell-n')") and get(op,'notifications')['unread']==0)
    js("document.body.click()");nav('#/practice');nav('#/timeline')
    check("the other user's post is in the feed with its prop and skill",js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .chip[href=\"#/prop/poi/F/F2\"]')",str(sp)) and js("return document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-body')?.textContent",post).startswith('Nice one'))
    click(f'.post[data-post="{sp}"] [data-act=like]');idle()
    liked=get(op,f'posts/{sp}')['posts'][0]
    check('liking updates the count and is saved',liked['liked'] and liked['likes']==1 and txt(f'.post[data-post="{sp}"] .act.on .num')=='1',liked.get('likes'))
    js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-form');f.body.value='Which flower?';f.requestSubmit()",str(sp));idle()
    check('a comment from the form shows up under the post',js("return [...document.querySelectorAll('.post[data-post=\"'+arguments[0]+'\"] .cmt-body')].map(c=>c.textContent)",str(sp))==['Which flower?'] and get(sam,'notifications')['items'][0]['kind']=='comment')
    check('only the author or an admin can delete a post',refused(sam,f'posts/{post}',{},'DELETE') and not refused(op,f'posts/{sp}',{},'DELETE'))
    # Achievements: a session unlocks badges, which post a milestone and show in the bell.
    nav('#/practice')
    js("const s=document.querySelector('[data-pick=focus-add]');s.value='F7';s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()");idle()
    js("const f=document.querySelector('#log-form');f.minutes.value='35';f.requestSubmit()");idle()
    ach=wait_for(lambda: [a['key'] for a in get(op,'me')['achievements'] if a['key']=='sessions-1'],'the First steps badge to be saved',SLOW)
    check('logging a session unlocks First steps',ach==['sessions-1'],ach)
    check('an unlock card is shown',lambda: js("return !document.querySelector('#unlock').hidden&&document.querySelector('#unlock').textContent.includes('First steps')"))
    ms=wait_for(lambda: [p for p in get(op,'posts')['posts'] if p['kind']=='milestone' and 'sessions-1' in p['data']['achievements']],'the milestone post',SLOW)
    check('one milestone post is made for the batch',len(ms)==1 and 'first-post' not in ms[0]['data']['achievements'],[p['data'] for p in ms])
    check('the user is notified of the badge',any(i['kind']=='achievement' and i['text']=='sessions-1' for i in get(op,'notifications')['items']))
    stats=wait_for(lambda: get(op,'me')['user'].get('stats') or None,'the stats on the profile',SLOW)
    check('level, XP and streak are reported for the profile',stats and stats['streak']==1 and stats['xp']>=45 and stats['level']>=1 and stats['sessions']==1,stats)
    nav('#/timeline')
    check("today's session suggests its tricks with one tap",'Today' in txt('.sugg') and js("return [...document.querySelectorAll('.sugg .chip')].map(c=>c.textContent)")==['F7'])
    click('.sugg .chip');idle()
    check('a suggested trick becomes a chip',js("return [...document.querySelectorAll('.draft-chips .chip.trick b')].map(b=>b.textContent)")==['F7'])
    js("document.querySelector('[data-act=tag-rm]').click()");idle()
    nav('#/achievements')
    check('the achievements page shows the badge unlocked, the level and a one-day streak',js("return [...document.querySelectorAll('.badge.on b')].map(b=>b.textContent)").count('First steps')==1 and txt('.level-ring b')=='1' and txt('.streak-card .val').startswith('1'))
    check('weekly challenges show progress',js("return document.querySelectorAll('.ch').length")==4 and '1 of 4' in txt('.ch .tiny'))
    # Plan: quests with XP.
    nav('#/plan')
    check('the plan shows quests with skill chips and XP on each gate',js("return document.querySelectorAll('.quest').length")==5 and js("return document.querySelectorAll('.quest.on .chip.on').length")==1 and '+150 XP' in txt('.quest .check .chip') and js("return document.querySelectorAll('.quest .qs').length")>20)
    check('the plan has an XP card, challenges and a focus skill',txt('[data-tour=xp] h2')=='Nunchaku XP' and js("return !!document.querySelector('[data-tour=challenges]')") and js("return !!document.querySelector('[data-tour=focus] .sk-row')"))
    # Streak overlay.
    nav('#/achievements');click('[data-act=share-streak]');idle()
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
    click('[data-act=share-tpl][data-v=bold]');idle()
    check('choosing a template is remembered',js("return document.querySelector('.tpl.on').dataset.v")=='bold' and json.loads(js("return localStorage.getItem('fl-share')"))['tpl']=='bold')
    click('[data-act=share-tpl][data-v=classic]');idle()
    check('the share dialog previews the overlay with download and post buttons',js("return !document.querySelector('#modal').hidden&&document.querySelector('#share-cv').width>0&&!!document.querySelector('[data-act=share-do][data-v=download]')&&!!document.querySelector('[data-act=share-do][data-v=post]')"))
    click('[data-act=share-do][data-v=post]');idle()
    shared=wait_for(lambda: [p for p in get(op,'posts')['posts'] if p['image'].startswith('data:image/jpeg') and 'streak' in p['body']],'the streak post to be saved',SLOW)
    check('posting the streak makes a post with the picture and unlocks Shared the flame',len(shared)==1 and any(a['key']=='share-streak' for a in get(op,'me')['achievements']),len(shared))
    check('the timeline opens on the new post',lambda: js("return location.hash")=='#/timeline' and js("return !!document.querySelector('.post .post-img')"))
    shot('/tmp/flowa-test/timeline.png')
    # Replies and threads.
    rp=api(op,'posts',{'body':'Thread test post'})['posts'][0]['id']
    c1=api(sam,f'posts/{rp}/comments',{'body':'How long did F7 take you?'})['comment']['id']
    nav('#/practice');nav('#/timeline')
    js("document.querySelector('[data-act=cmt-reply][data-id=\"'+arguments[0]+'\"]').click()",str(c1));idle()
    check('Reply opens a box under the thread, prefilled with the name and focused',js("return document.activeElement.closest('.reply-form')!==null&&document.activeElement.value")=='@sam ')
    js("const i=document.activeElement;i.value+='about three weeks';i.form.requestSubmit()");idle()
    th=js("const t=document.getElementById('c-'+arguments[0]).closest('.thr');return [...t.querySelectorAll('.cmt.reply .cmt-body')].map(b=>b.textContent)",str(c1))
    check('the reply appears under the comment it answers, naming the person',th==['@sam about three weeks'],th)
    check('the person answered is told it is a reply',get(sam,'notifications')['items'][0]['kind']=='reply')
    for i in range(5): api(sam,f'posts/{rp}/comments',{'body':f'follow-up {i}','parentId':c1})
    for i in range(3): api(sam,f'posts/{rp}/comments',{'body':f'top-level {i}'})
    nav('#/practice');nav('#/timeline')
    post_sel=f'.post[data-post="{rp}"]'
    check('a busy post shows its two newest comments and a button for the rest',js("return document.querySelectorAll(arguments[0]+' .thr').length",post_sel)==2 and 'View all 10 comments' in txt(post_sel+' [data-act=more-comments]'),txt(post_sel+' [data-act=more-comments]'))
    click(post_sel+' [data-act=more-comments]');idle()
    thr=js("return document.getElementById('c-'+arguments[0]).closest('.thr').querySelectorAll('.cmt.reply').length",str(c1))
    check('View all loads every comment, and a long thread folds to its two newest replies',js("return document.querySelectorAll(arguments[0]+' .thr').length",post_sel)==4 and thr==2 and 'View 4 more replies' in txt(post_sel+' [data-act=more-replies]'),thr)
    click(post_sel+' [data-act=more-replies]');idle()
    check('View more replies shows the whole thread',js("return document.getElementById('c-'+arguments[0]).closest('.thr').querySelectorAll('.cmt.reply').length",str(c1))==6)
    # Likes and edits on comments.
    reps=json.loads(op.open(BASE+f'api/comments/{c1}/replies',timeout=20).read())['comments']
    myrep=[c for c in reps if c['body'].startswith('@sam about')][0]['id'];other=[c for c in reps if c['body']=='follow-up 0'][0]['id']
    js("document.querySelector('[data-act=cmt-like][data-id=\"'+arguments[0]+'\"]').click()",str(other))
    likedOf=lambda: [c for c in json.loads(op.open(BASE+f'api/comments/{c1}/replies',timeout=20).read())['comments'] if c['id']==other][0]
    saved=poll(lambda: likedOf() if likedOf()['liked'] and txt(f'#c-{other} .cmt-a.on .num')=='1' else None)[0] or likedOf()
    check('liking a reply counts it, and the like is saved',txt(f'#c-{other} .cmt-a.on .num')=='1' and saved['liked'],(js("const e=document.getElementById('c-'+arguments[0]);return e&&e.querySelector('.cmt-acts').innerHTML.slice(0,90)",str(other)),saved['liked'],saved['likes'],js("let n=0;eachComment(Number(arguments[0]),c=>n++);return n",str(other))))
    js("document.querySelector('[data-act=cmt-edit][data-id=\"'+arguments[0]+'\"]').click()",str(myrep));idle()
    js("const t=document.querySelector('.cmt-edit textarea');t.value='@sam about three weeks, slowly';t.form.requestSubmit()");idle()
    check('the author can edit a comment, and it is marked edited',txt(f'#c-{myrep} .cmt-body')=='@sam about three weeks, slowly' and 'edited' in txt(f'#c-{myrep} .tiny'))
    # A deleted comment with replies stays as a placeholder.
    api(sam,f'comments/{c1}',{},'DELETE')
    nav('#/practice');nav('#/timeline');click(post_sel+' [data-act=more-comments]');idle()
    check('a deleted comment with replies reads "Comment deleted", with its replies under it',txt(f'#c-{c1} .cmt-gone')=='Comment deleted' and js("return document.getElementById('c-'+arguments[0]).closest('.thr').querySelectorAll('.cmt.reply').length",str(c1))>=2)
    # The bell opens the thread at the reply.
    r2=api(sam,f'posts/{rp}/comments',{'body':'good to know','parentId':myrep})['comment']['id']
    nav('#/practice');js("loadNotifs()");idle();click('[data-act=bell]');idle()
    check('the bell says someone replied to your comment','replied to your comment' in txt('.ntf-panel'))
    js("[...document.querySelectorAll('.ntf')].find(a=>a.textContent.includes('replied to your comment')).click()")
    inview=lambda: js("const e=document.getElementById('c-'+arguments[0]);if(!e)return null;const r=e.getBoundingClientRect();return [location.hash,r.top>=0&&r.bottom<=innerHeight,e.classList.contains('focus')]",str(r2))
    check('the reply notification opens the post with the reply in view and highlighted',lambda: inview()==[f'#/post/{rp}/{r2}',True,True],inview)
    # A post opened at one thread offers more comments only when there are some it is not showing.
    solo=api(sam,'posts',{'body':'One thread only'})['posts'][0]['id']
    k1=api(op,f'posts/{solo}/comments',{'body':'first here'})['comment']['id']
    k2=api(sam,f'posts/{solo}/comments',{'body':'an answer','parentId':k1})['comment']['id']
    nav('#/practice');nav(f'#/post/{solo}/{k2}')
    seen=js("return [!!document.getElementById('c-'+arguments[0]),!!document.getElementById('c-'+arguments[1]),!!document.querySelector('[data-act=more-comments]')]",str(k1),str(k2))
    check('a thread that shows every comment has no "View more comments"',seen==[True,True,False],seen)
    api(sam,f'posts/{solo}/comments',{'body':'a second thread'})
    nav('#/practice');nav(f'#/post/{solo}/{k2}')
    check('with another thread on the post the button is there',js("return (document.querySelector('[data-act=more-comments]')||{}).textContent")=='View more comments')
    # Bookmarks: a post from the timeline, a skill from its panel, a combo from its header, then the page.
    bm=lambda: get(op,'bookmarks')
    check('the navigation has a Bookmarks entry',js("return !!document.querySelector('.side a[href=\"#/bookmarks\"]')"))
    nav('#/practice');nav('#/timeline')
    click(f'.post[data-post="{rp}"] [data-act=bookmark]');idle()
    check('the bookmark button under a post keeps it, and shows as on',bm()['postIds']==[rp] and js("return document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .act.bm').getAttribute('aria-pressed')",str(rp))=='true',bm())
    nav('#/prop/nunchaku/F/F5')
    click('#panel [data-act=bookmark]');idle()
    check('the skill panel bookmarks the skill, and its node in the tree gets a marker',bm()['skills']==['nunchaku:F5'] and js("return !!document.querySelector('.node[href=\"#/prop/nunchaku/F/F5\"] .bm-mark')"),bm()['skills'])
    nav('#/combos')
    click('.cb-head [data-act=bookmark]');idle()
    cid=js("return document.querySelector('.cb-head [data-act=bookmark]').dataset.ref")
    check('the combo header bookmarks the open combo',bm()['combos']==[cid] and bool(cid),(bm()['combos'],cid))
    nav('#/bookmarks')
    rows=js("return [...document.querySelectorAll('.bm-row a')].map(a=>a.getAttribute('href'))")
    check('the Bookmarks page lists the skill, the combo and the post',rows==['#/prop/nunchaku/F/F5',f'#/combos/{cid}'] and 'Morning loop' in js("return [...document.querySelectorAll('.bm-row')].map(r=>r.textContent).join()") and js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"]')",str(rp)),rows)
    click(f'.post[data-post="{rp}"] [data-act=bookmark]');idle()
    check('removing a bookmark on the page takes the post off it and off the server',js("return !document.querySelector('.post[data-post=\"'+arguments[0]+'\"]')",str(rp)) and bm()['postIds']==[],bm()['postIds'])
    check('a bad bookmark is refused',refused(op,'bookmarks',{'kind':'skill','ref':'drums:F1'}))
    # Blocks: from a profile, after a question in plain words. The member's post leaves the timeline at once,
    # the Profile page lists the block, and unblocking brings the post back.
    bp=api(sam,'posts',{'body':'A post by sam, to block'})['posts'][0]['id']
    shown=lambda: js("return !!document.querySelector('.post[data-post=\"'+arguments[0]+'\"]')",str(bp))
    nav('#/practice');nav('#/timeline')
    check("before the block sam's post is in the timeline",shown)
    nav('#/u/sam')
    js("window.__asked='';window.confirm=m=>{window.__asked=m;return true}")
    click('[data-act=block]');idle()
    check('Block asks first, then the profile says sam is blocked, shows no posts and offers Unblock, with the focus on it',lambda: 'no longer see their posts' in js("return window.__asked") and 'If they mention you, you are not told' in js("return window.__asked") and js("return !!document.querySelector('#blocked-note')&&!document.querySelector('.post')&&document.activeElement?.dataset.act==='unblock'"),lambda: (js("return window.__asked"),txt('#blocked-note')))
    check('the block is saved',[b['user']['username'] for b in get(op,'blocks')['blocks']]==['sam'])
    nav('#/timeline')
    check("the timeline no longer shows sam's post, nor sam among the people",lambda: not shown() and js("return document.querySelectorAll('.person').length>0&&![...document.querySelectorAll('.person b')].some(b=>b.textContent==='Sam Spins')"))
    nav('#/profile')
    check('the Profile page lists sam under Blocked accounts',lambda: js("return [...document.querySelectorAll('#blocks-card .bm-row .tiny')].map(t=>t.textContent)")==['@sam'])
    nav('#/u/sam');click('[data-act=unblock]');idle()
    check('Unblock on the profile shows his posts again',lambda: js("return !document.querySelector('#blocked-note')&&document.activeElement?.dataset.act==='block'") and bool(js("return document.querySelector('.post[data-post=\"'+arguments[0]+'\"]')",str(bp))))
    nav('#/timeline')
    check("after the unblock sam's post is back in the timeline",shown)
    api(op,'users/sam/block',{},'PUT');nav('#/profile')
    click('#blocks-card [data-act=unblock]');idle()
    check('Unblock in the list takes him off it',lambda: js("return document.querySelectorAll('#blocks-card .bm-row').length")==0 and get(op,'blocks')['blocks']==[])
    api(sam,f'posts/{bp}',{},'DELETE')
    # Nothing lost and nothing doubled: quick double submits, a lost answer, an empty comment, a post
    # that went away, pictures with see-through parts, the leave warning, and a draft at sign-out.
    bodies=lambda t: [p['body'] for p in get(op,'posts')['posts'] if p['body']==t]
    toastIs=lambda t: js("const e=document.querySelector('#toast');return !e.hidden&&e.textContent")==t
    leaving="const e=new Event('beforeunload',{cancelable:true});window.dispatchEvent(e);return e.defaultPrevented"
    typed="const t=document.querySelector('#compose-form textarea');t.value=arguments[0];t.dispatchEvent(new Event('input',{bubbles:true}))"
    def pick(sel,b64,name,kind):
        # Puts a file on a hidden file input the way a person choosing it would.
        js("const u=Uint8Array.from(atob(arguments[1]),c=>c.charCodeAt(0)),d=new DataTransfer();d.items.add(new File([u],arguments[2],{type:arguments[3]}));const i=document.querySelector(arguments[0]);i.files=d.files;i.dispatchEvent(new Event('change',{bubbles:true}))",sel,b64,name,kind)
    corner="return (async()=>{const i=new Image();i.src=arguments[0];await i.decode();const c=document.createElement('canvas');c.width=i.naturalWidth;c.height=i.naturalHeight;const g=c.getContext('2d');g.drawImage(i,0,0);return [...g.getImageData(1,1,1,1).data]})()"
    import zlib,struct
    chunk=lambda k,d: struct.pack('>I',len(d))+k+d+struct.pack('>I',zlib.crc32(k+d))
    clear_png=base64.b64encode(b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',64,64,8,6,0,0,0))+chunk(b'IDAT',zlib.compress((b'\0'+b'\0'*256)*64))+chunk(b'IEND',b'')).decode()
    one_gif='R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'
    mine=api(op,f'posts/{rp}/comments',{'body':'Mine to edit'})['comment']['id']
    quiet=lambda: js("document.querySelector('#toast').textContent=''")
    nav('#/practice');nav('#/timeline')
    check('with nothing typed, leaving the page is not held up',js(leaving) is False and js("return socialDraft()") is False)
    before=get(op,f'posts/{rp}')['posts'][0]['commentCount']
    js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-form:not(.reply-form)');f.body.value='   ';f.requestSubmit()",str(rp));idle()
    check('a comment of only spaces is not sent, and the page says why',toastIs('Write something first.') and get(op,f'posts/{rp}')['posts'][0]['commentCount']==before,txt('#toast'))
    quiet();js("document.activeElement.blur();document.querySelector('[data-act=cmt-edit][data-id=\"'+arguments[0]+'\"]').click()",str(mine));idle()
    js("const t=document.querySelector('.cmt-edit textarea');t.value='  \\n ';t.form.requestSubmit()");idle()
    said=toastIs('Write something first.');open_still=js("return !!document.querySelector('.cmt-edit')")
    js("document.activeElement.blur();document.querySelector('[data-act=cmt-edit-cancel]').click()");idle()
    check('an edit emptied to spaces is not saved, and the page says why',said and open_still and txt(f'#c-{mine} .cmt-body')=='Mine to edit',(said,open_still,txt(f'#c-{mine} .cmt-body')))
    quiet();js("document.querySelector('[data-act=cmt-reply][data-id=\"'+arguments[0]+'\"]').click()",str(mine));idle()
    js("const i=document.querySelector('.reply-form input');i.value='   ';i.form.requestSubmit()");idle()
    said=toastIs('Write something first.')
    js("document.activeElement.blur();document.querySelector('[data-act=reply-cancel]').click()");idle()
    check('a reply of only spaces is not sent either',said and get(op,f'posts/{rp}')['posts'][0]['commentCount']==before and js("return !document.querySelector('.reply-form')"),said)
    js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-form:not(.reply-form)');f.body.value='half a thought'",str(rp))
    check('a half-written comment holds the page when leaving',js(leaving) is True)
    js("const f=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-form:not(.reply-form)');f.body.value=''",str(rp))
    # With the cursor resting in a comment box the page is not redrawn: a post sent from the composer must still leave it empty.
    js("document.querySelector('.post[data-post=\"'+arguments[0]+'\"] .cmt-form:not(.reply-form)').body.focus();const f=document.querySelector('#compose-form');f.body.value='Posted with the cursor elsewhere';f.requestSubmit()",str(rp))
    wait_for(lambda: bodies('Posted with the cursor elsewhere'),'the post to be saved');idle()
    check('a post sent while the cursor rests in a comment box empties the composer',js("return [document.activeElement.tagName,document.querySelector('#compose-form textarea').value]")==['INPUT',''] and toastIs('Posted.') and len(bodies('Posted with the cursor elsewhere'))==1,js("return [document.activeElement.tagName,document.querySelector('#compose-form textarea').value]"))
    js("document.activeElement.blur()")
    js(typed,'A draft worth keeping')
    check('a draft in the composer holds the page when leaving',js(leaving) is True and js("return socialDraft()") is True)
    js(typed,'')
    check('an emptied composer lets the page go again',js(leaving) is False)
    off=js("const f=document.querySelector('#compose-form');f.body.value='Sent twice by a quick thumb';f.requestSubmit();const off=f.querySelector('button[type=submit]').disabled;f.requestSubmit();return off")
    wait_for(lambda: bodies('Sent twice by a quick thumb'),'the post to be saved');idle()
    check('two quick taps on Post make one post, and the button is off while it sends',off is True and len(bodies('Sent twice by a quick thumb'))==1 and js("return [...document.querySelectorAll('.post-body')].filter(b=>b.textContent==='Sent twice by a quick thumb').length")==1,(off,len(bodies('Sent twice by a quick thumb'))))
    # The server saves the post but the answer never arrives: sending the same draft again must not make a second one.
    js("const real=window.fetch;window.__lost=0;window.fetch=function(u,o){if(o&&o.method==='POST'&&String(u)==='api/posts'&&!window.__lost){window.__lost=1;return real.call(window,u,o).then(()=>{throw new TypeError('lost')})}return real.call(window,u,o)}")
    js("const f=document.querySelector('#compose-form');f.body.value='The answer got lost on the way';f.requestSubmit()")
    wait_for(lambda: bodies('The answer got lost on the way'),'the post to be saved');idle()
    kept=js("return [window.__lost,document.querySelector('#compose-form textarea').value,document.querySelector('#compose-form button[type=submit]').disabled]")
    check('when the answer is lost the draft stays and the button comes back',kept==[1,'The answer got lost on the way',False] and toastIs('Could not reach Flowa.'),kept)
    js("document.querySelector('#compose-form').requestSubmit()");idle()
    check('sending the same draft again does not post it twice',len(bodies('The answer got lost on the way'))==1 and js("return [...document.querySelectorAll('.post-body')].filter(b=>b.textContent==='The answer got lost on the way').length")==1 and js("return document.querySelector('#compose-form textarea').value")=='',len(bodies('The answer got lost on the way')))
    # A like on a post that was deleted elsewhere is taken back.
    gone=api(sam,'posts',{'body':'Here now, gone in a moment'})['posts'][0]['id']
    nav('#/practice');nav('#/timeline')
    api(sam,f'posts/{gone}',{},'DELETE')
    click(f'.post[data-post="{gone}"] [data-act=like]');idle()
    heart=js("const b=document.querySelector('.post[data-post=\"'+arguments[0]+'\"] [data-act=like]');return b&&[b.classList.contains('on'),b.getAttribute('aria-pressed'),b.querySelector('.num').textContent]",str(gone))
    check('liking a post that was just deleted turns the heart back off and says so',heart==[False,'false',''] and toastIs('No such post.'),(heart,txt('#toast')))
    # Pictures: see-through parts become white, not black, and an animated picture says it is kept as a still.
    pick('[data-change=compose-img]',clear_png,'clear.png','image/png')
    src=wait_for("return document.querySelector('.compose-img img')?.src",'the picture in the composer')
    px=js(corner,src) if src else None
    check('a see-through picture lands on white in the composer',bool(px) and min(px[:3])>=245,px)
    check('a still picture carries no note about animation',js("return !document.querySelector('.still-note')"))
    click('[data-act=compose-img-rm]');idle()
    pick('[data-change=compose-img]',one_gif,'spin.gif','image/gif')
    wait_for("return !!document.querySelector('.compose-img img')",'the picture in the composer')
    check('an animated picture is marked as kept as a still',txt('.still-note')=='Animated pictures are kept as a still.',txt('.still-note'))
    click('[data-act=compose-img-rm]');idle()
    check('removing the picture removes the note',js("return !document.querySelector('.still-note')&&!document.querySelector('.compose-img')"))
    nav('#/profile')
    pick('[data-change=avatar]',clear_png,'clear.png','image/png')
    wait_for(lambda: toastIs('Picture updated.'),'the profile picture to be saved');idle()
    av=get(op,'me')['user']['avatar'];px=js(corner,av) if av.startswith('data:image/jpeg') else None
    check('a see-through profile picture lands on white',bool(px) and min(px[:3])>=245,px)
    pick('[data-change=avatar]',one_gif,'spin.gif','image/gif')
    check('an animated profile picture says it is kept as a still',lambda: toastIs('Picture updated. Animated pictures are kept as a still.'),lambda: txt('#toast'))
    # The About form against an edit from another device: neither side's change is lost.
    nav('#/practice');nav('#/profile')
    js("document.querySelector('#about-form').bio.value='Typed here while the phone changed a link.'")
    check('an unsaved About form holds the page when leaving',js("return aboutDirty()") is True and js(leaving) is True)
    api(op,'me',{'links':{'tiktok':'from.the.phone'}},'PUT')
    js("document.querySelector('#about-form').requestSubmit()");idle()
    form=js("const f=document.querySelector('#about-form');return [f.bio.value,f.tiktok.value,f.instagram.value]")
    check('saving over a newer profile is refused with the reason, the typed bio stays and the new link shows',form==['Typed here while the phone changed a link.','from.the.phone','flowa_test'] and 'changed on another device' in (txt('#about-err') or '') and get(op,'me')['user']['bio'].startswith('Nunchaku since'),(form,txt('#about-err')))
    js("document.querySelector('#about-form').requestSubmit()");idle()
    me=get(op,'me')['user']
    check('saving again puts the edit on top and keeps the other device\'s link',me['bio']=='Typed here while the phone changed a link.' and me['links']=={'instagram':'flowa_test','tiktok':'from.the.phone','youtube':'FlowaTube'} and js("return aboutDirty()") is False,(me['bio'],me['links']))
    # A draft does not follow the next person who signs in on this browser.
    nav('#/timeline')
    js(typed,'Private until I post it')
    pick('[data-change=compose-img]',clear_png,'clear.png','image/png')
    wait_for("return !!document.querySelector('.compose-img img')",'the picture in the composer')
    nav('#/profile');click('[data-act=signout]');wait_for(lambda: toastIs('Signed out.'),'the sign-out to finish');idle()
    signin('sam')
    nav('#/timeline')
    left=js("const f=document.querySelector('#compose-form');return f&&[f.body.value,!!document.querySelector('.compose-img'),socialDraft()]")
    check('after sign-out the next person on this browser finds an empty composer',left==['',False,False] and get(sam,'me')['user']['username']=='sam' and 'Private until' not in js("return document.body.textContent"),left)
finally:
    done()
