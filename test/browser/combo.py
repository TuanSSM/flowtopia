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
    js("const s=document.querySelector('[data-change=combo-add]');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}))",sid);time.sleep(1.1)
try:
    admin();start();signin()
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
    js("const f=document.querySelector('#combo-form');f.name.value='Second';f.requestSubmit()");time.sleep(1.2)
    check('only one combo is open; the other is a single line',js("return document.querySelectorAll('.cb').length")==1 and js("return document.querySelectorAll('.cb-row').length")==1 and 'Test loop' in txt('.cb-row'))
    click('.cb-row');time.sleep(1.2)
    check('clicking a closed combo opens it and plays it',js("return document.querySelector('.cb h2').textContent")=='Test loop' and js("return !!document.querySelector('#combo-cv')"))
    cmd('WebDriver:SetWindowRect',width=480,height=900);time.sleep(.8);shot('/tmp/flowa-test/combo-narrow.png')
    check('no page overflow on a narrow screen',js("return document.documentElement.scrollWidth<=innerWidth"))
finally:
    done()
