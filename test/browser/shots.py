# Contact sheet of chosen demos: shots.py out.png prop,skill,variant,view,phase ...
import sys,os,io
sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
from h import *
from PIL import Image
SZ=int(os.environ.get('SZ','360'));COLS=int(os.environ.get('COLS','3'))
try:
    admin();start();tiles=[]
    nav('#/prop/nunchaku/F/F5');figure()
    print('3D figure:',js("return window.Figure3D?(window.Figure3D.ready?'ready':'failed'):'not loaded'"),flush=True)
    for prop,sid,var,view,ph in [x.split(',') for x in sys.argv[2:]]:
        nav(f'#/prop/{prop}/{sid[0]}/{sid}')
        js("const D=window.Demo;D.act({k:'variant',v:arguments[0]});D.act({k:'view',v:arguments[1]});D.state.playing=false;D.state.phase=Number(arguments[2]);D.mount()",var,view,ph);idle()  # another variant is another clip to fetch
        js("window.Demo.mount()");frames()
        b=js("return document.querySelector('#demo-cv').toDataURL('image/png').split(',')[1]")
        tiles.append(Image.open(io.BytesIO(base64.b64decode(b))).convert('RGB').resize((SZ,SZ)))
    cols=COLS;rows=(len(tiles)+cols-1)//cols;sheet=Image.new('RGB',(cols*SZ,rows*SZ),(30,30,30))
    for i,t in enumerate(tiles): sheet.paste(t,((i%cols)*SZ,(i//cols)*SZ))
    sheet.save(sys.argv[1]);check('sheet saved',True)
finally:
    done()
