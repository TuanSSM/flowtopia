# The combos page: an always-on stage that plays the open combo, a timeline with repetitions, and one-line closed combos.
import sys,os
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
SNAP="const c=document.querySelector('#combo-cv');if(!c)return null;const d=c.getContext('2d').getImageData(0,0,c.width,c.height).data;let h=0;for(let i=0;i<d.length;i+=4*41){h=(h*31+d[i]+d[i+1]*3+d[i+2]*7)>>>0}return h"
ids=lambda: js("return [...document.querySelectorAll('.cb-step .cb-id')].map(c=>c.textContent).join()")
def saved_combo(want):
    # The page saves a moment after each change; wait for it.
    got=None
    for _ in range(30):
        got=js("return fetch('api/progress/nunchaku').then(r=>r.json()).then(d=>{const c=(d.state.combos||[])[0];return c?[c.seq.join(),(c.reps||[]).join()]:null})")
        if got==want: break
        time.sleep(.5)
    return got
def add(sid):
    js("const s=document.querySelector('[data-pick=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}));s.parentElement.querySelector('[data-act=add-chosen]').click()",sid);time.sleep(1.1)
try:
    op=admin();start();signin()
    if js("return !!document.querySelector('input[name=prop]')"): js("for(const i of document.querySelectorAll('input[name=prop]'))i.checked=i.value==='nunchaku';document.querySelector('#props-form').requestSubmit()");time.sleep(3.5)
    nav('#/combos',2.5)
    js("const f=document.querySelector('#combo-form');f.name.value='Test loop';f.requestSubmit()");time.sleep(1.2)
    check('a new combo opens with an empty stage and no Watch button',js("return !!document.querySelector('.cb-empty')&&!document.querySelector('[data-act=combo-watch]')"))
    for sid in ['F5','R10','F1','A1']: add(sid)
    check('the stage appears as soon as there are skills, with one row per skill',js("return !!document.querySelector('#combo-cv')")and ids()=='F5,R10,F1,A1')
    check('a skill with no demo is marked, and a picture has no repetitions',js("return [...document.querySelectorAll('.cb-step.nodemo .cb-id')].map(c=>c.textContent).join()")=='R10' and js("return document.querySelectorAll('.cb-step')[2].querySelector('.cb-reps')===null&&document.querySelectorAll('.cb-step')[0].querySelector('.cb-reps')!==null"))
    a=js(SNAP);time.sleep(.5);b=js(SNAP)
    check('it plays without being asked',js("return window.Demo.combo.playing")and len(txt('#combo-now'))>3 and js("return document.querySelector('#combo-time').textContent.includes('/')"),txt('#combo-now'))
    d=js("return window.Demo.info('nunchaku','F5').d")
    t0=js("return window.Demo.comboSecs('nunchaku',['F5'],[1])")
    check('one repetition is one whole cycle of the move',abs(t0-d)<1e-6 and d>0.4,(t0,d))
    js("document.querySelector('[data-act=combo-rep][data-i=\"0\"][data-d=\"1\"]').click()");time.sleep(1.2)
    js("document.querySelector('[data-act=combo-rep][data-i=\"0\"][data-d=\"1\"]').click()");time.sleep(1.2)
    saved=saved_combo(['F5,R10,F1,A1','3,1,1,1'])
    check('repetitions can be raised and are saved',js("return document.querySelector('.cb-step .cb-reps b').textContent")=='3×' and saved==['F5,R10,F1,A1','3,1,1,1'],saved)
    js("document.querySelector('[data-act=combo-demo][data-k=restart]').click()");time.sleep(.3)
    seenRep=set();t=time.time()
    while time.time()-t<12 and js("return window.Demo.combo.i")==0: seenRep.add(txt('#combo-now'));time.sleep(.15)
    check('the skill plays all three cycles before moving on',any('3 of 3' in x for x in seenRep) and any('1 of 3' in x for x in seenRep),sorted(seenRep)[:4])
    seen=set()
    for _ in range(80):
        seen.add(js("return window.Demo.combo.i"));time.sleep(.4)
        if len(seen)==4 and js("return window.Demo.combo.i")==0: break
    check('it moves through every step and starts again',seen=={0,1,2,3},seen)
    check('the playing row is highlighted and the progress bar has a segment per skill',js("return document.querySelectorAll('.cb-step.playing').length")==1 and js("return document.querySelectorAll('#combo-prog i').length")==4)
    for _ in range(20):  # the rows are drawn again as the stage plays; wait until they are there
        if js("return document.querySelectorAll('.cb-main').length")>=2: break
        time.sleep(.25)
    js("document.querySelectorAll('.cb-main')[1].click()");time.sleep(1.2)
    check('clicking a row plays from there; a name card stands in for a missing demo',txt('#combo-now').startswith('R10') and js("return document.querySelectorAll('.cb-step')[1].classList.contains('playing')"),txt('#combo-now'))
    js("document.querySelector('[data-act=combo-move][data-i=\"1\"]').click()");time.sleep(1.2)
    saved=saved_combo(['R10,F5,F1,A1','1,3,1,1'])
    check('a skill can be moved earlier and takes its repetitions with it',ids()=='R10,F5,F1,A1' and saved==['R10,F5,F1,A1','1,3,1,1'],saved)
    js("document.querySelector('[data-act=combo-demo][data-k=play]').click()");time.sleep(.6)
    i=js("return window.Demo.combo.i");time.sleep(3)
    check('Pause stops the sequence',js("return window.Demo.combo.i")==i and txt('[data-act=combo-demo][data-k=play]')=='Play')
    js("document.querySelector('[data-act=combo-demo][data-k=play]').click()");time.sleep(.5)
    js("document.querySelector('[data-act=combo-rm][data-i=\"0\"]').click()");time.sleep(1.2)
    check('removing a skill updates the stage',ids()=='F5,F1,A1' and js("return document.querySelectorAll('#combo-prog i').length")==3)
    shot('/tmp/flowa-test/combo-open.png')
    # The list of skills is worked with the keyboard: moving through it adds nothing, Enter or the Add button does.
    def keys(*ks): cmd('WebDriver:PerformActions',actions=[{'type':'key','id':'kb','actions':[a for k in ks for a in ({'type':'keyDown','value':k},{'type':'keyUp','value':k})]}]);cmd('WebDriver:ReleaseActions')
    js("document.querySelector('[data-pick=combo-add]').focus()")
    check('the Add button waits for a choice',js("return document.querySelector('[data-pick=combo-add]').parentElement.querySelector('[data-act=add-chosen]').disabled")==True)
    keys('\ue015','\ue015','\ue015');time.sleep(1.2)
    third=js("const s=document.querySelector('[data-pick=combo-add]');return [s.value,s.options[3].value,s.parentElement.querySelector('[data-act=add-chosen]').disabled]")
    check('arrow down three times chooses the third skill and adds nothing',ids()=='F5,F1,A1' and third[0]==third[1]!='' and third[2]==False,(ids(),third))
    keys('\ue007');time.sleep(1.2)
    after=js("const s=document.querySelector('[data-pick=combo-add]');return [document.activeElement===s,s.value]")
    check('Enter adds the chosen skill and leaves the keyboard on the list',ids()=='F5,F1,A1,'+third[1] and after==[True,''],(ids(),after))
    js("document.querySelector('[data-act=combo-rm][data-i=\"3\"]').click()");time.sleep(1.2)
    js("const f=document.querySelector('#combo-form');f.name.value='Second';f.requestSubmit()");time.sleep(1.2)
    check('only one combo is open; the other is a single line',js("return document.querySelectorAll('.cb').length")==1 and js("return document.querySelectorAll('.cb-row').length")==1 and 'Test loop' in txt('.cb-row'))
    click('.cb-row');time.sleep(1.2)
    check('clicking a closed combo opens it and plays it',js("return document.querySelector('.cb h2').textContent")=='Test loop' and js("return !!document.querySelector('#combo-cv')"))
    # Two devices on one account: the other one levels a skill while this page still holds the older copy.
    # Saving here must keep both changes, and the combos made above.
    def doc(): return json.loads(op.open(BASE+'api/progress/nunchaku',timeout=20).read())
    def until(fn,n=40):
        for _ in range(n):
            v=fn()
            if v: return v
            time.sleep(.5)
        return fn()
    nav('#/prop/nunchaku/F/F1',2)
    side=js("return document.querySelector('#panel [data-act=level][data-id=F1]').dataset.side")
    time.sleep(2);d=doc();combos=[c['name'] for c in d['state']['combos']];now=time.strftime('%Y-%m-%dT%H:%M:%S.000Z',time.gmtime())
    d['state']['skills']['F1']={side:3,'updated':now};d['state']['events'].insert(0,{'t':now,'id':'F1','side':'' if side=='O' else side,'from':0,'to':3})
    api(op,'progress/nunchaku',{'rev':d['rev'],'state':d['state']},'PUT')
    nav('#/prop/nunchaku/F/F5',2)
    side5=js("return document.querySelector('#panel [data-act=level][data-id=F5]').dataset.side")
    click('#panel [data-act=level][data-id=F5][data-v="2"]')
    both=until(lambda: (lambda k: k.get('F1',{}).get(side)==3 and k.get('F5',{}).get(side5)==2)(doc()['state']['skills']))
    d=doc()
    check('a skill levelled here and another levelled on a second device both survive',bool(both) and [c['name'] for c in d['state']['combos']]==combos and {'F1','F5'}<={e['id'] for e in d['state']['events']},(d['state']['skills'].get('F1'),d['state']['skills'].get('F5')))
    check('the page says both sets of changes were kept',js("const e=document.querySelector('#toast');return !e.hidden&&e.textContent")=='Changed on another device too. Both sets of changes were kept.',txt('#toast'))
    nav('#/prop/nunchaku/F/F1',2)
    check("the other device's level shows on this page without a reload",js("return document.querySelector('#panel [data-act=level][data-id=F1].on')?.dataset.v")=='3')
    rev=d['rev'];click('#panel [data-act=level][data-id=F1][data-v="1"]')
    again=until(lambda: doc()['state']['skills']['F1'].get(side)==1)
    d=doc()
    check('the next edit saves on top without another clash',bool(again) and d['rev']==rev+1 and d['state']['skills']['F5'].get(side5)==2,(rev,d['rev']))
    cmd('WebDriver:SetWindowRect',width=480,height=900);time.sleep(.8);shot('/tmp/flowa-test/combo-narrow.png')
    check('no page overflow on a narrow screen',js("return document.documentElement.scrollWidth<=innerWidth"))
    cmd('WebDriver:SetWindowRect',width=1280,height=900)
    # ---- Search: it covers every prop the member has, says so, and names the prop of each skill it finds.
    def fresh(tag):
        cmd('WebDriver:Navigate',url=BASE+f'?{tag}#/');until(lambda: js("return !!document.querySelector('.nav-profile .tiny')&&document.querySelector('.nav-profile .tiny').textContent==='@admin'"));time.sleep(1.5)
    def search(q):
        js("if(document.querySelector('#palette').hidden)openPalette();const i=document.querySelector('#pal-q');i.value=arguments[0];i.dispatchEvent(new Event('input',{bubbles:true}))",q);time.sleep(.5)
        return js("return [...document.querySelectorAll('.pal-i')].map(a=>[a.querySelector('.pal-p')?.textContent||'',a.getAttribute('href'),a.querySelector('.grow').textContent])")
    api(op,'me',{'props':['nunchaku']},'PUT');fresh('one')
    rows=search('butterfly')
    check('with one prop the search says so and finds only its skills',txt('#pal-hint')=='Searches the pages and the skills of your props: Nunchaku. Add a prop in your profile to search it too.' and not any(r[0] or '/prop/poi/' in r[1] for r in rows),(txt('#pal-hint'),rows[:4]))
    api(op,'me',{'props':['nunchaku','poi']},'PUT');fresh('two')
    rows=search('butterfly');poi=[r for r in rows if r[0]=='Poi']
    check('with two props the search names both',txt('#pal-hint')=='Searches the pages and the skills of your props: Nunchaku and Poi. Add a prop in your profile to search it too.',txt('#pal-hint'))
    check("a skill of the member's other prop is found and carries the prop's name",bool(poi) and all(r[1].startswith('#/prop/poi/') for r in poi) and all(r[0]=='Nunchaku' for r in rows if r[1].startswith('#/prop/nunchaku/') and r[1].count('/')==4),rows[:6])
    if poi:
        js("document.querySelector('.pal-i[href=\"'+arguments[0]+'\"]').click()",poi[0][1]);time.sleep(3)
        check('choosing it opens that skill on its own prop',js("return location.hash")==poi[0][1] and js("return !document.querySelector('#panel').hidden&&document.querySelector('#panel').dataset.id")==poi[0][1].split('/')[-1] and 'Poi' in txt('#crumb'),(js("return location.hash"),txt('#crumb')))
    api(op,'me',{'props':['nunchaku']},'PUT')
    # ---- A tour belongs to its section: leaving the section closes it for good, and the next page is free.
    api(op,'me',{'seen':[]},'PUT');api(op,'flags',{'flags':{'tour':True}},'PUT');fresh('tour')
    tip=lambda: js("const t=document.querySelector('.react-joyride__tooltip');return t?t.innerText:''")
    seen=lambda: js("return fetch('api/me').then(r=>r.json()).then(d=>d.user.seen||[])")
    up=until(lambda: 'Welcome to Flowa' in tip())
    check('the dashboard tour opens for a member who has not seen it',bool(up),tip()[:80])
    nav('#/settings',1.5)
    gone=until(lambda: not tip() and 'dashboard' in seen())
    check('leaving the section closes the tour and remembers it as seen',bool(gone) and txt('#main h1')=='Settings',(tip()[:60],seen()))
    check('the page behind is not blocked',js("return !document.querySelector('.react-joyride__overlay')&&!document.body.classList.contains('nav-open')"))
    nav('#/',3)
    check('going back does not bring the tour back',not tip() and js("return location.hash")=='#/',tip()[:60])
    nav('#/practice',1)
    check('the next section gets its own tour',bool(until(lambda: 'Log a session' in tip())),tip()[:80])
    js("history.back()");time.sleep(1)
    check('Back closes that one too',bool(until(lambda: not tip() and 'practice' in seen())),(tip()[:60],seen()))
    api(op,'flags',{'flags':{'tour':False}},'PUT')
finally:
    done()
