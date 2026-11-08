# A stand-in for Instagram, TikTok and YouTube, for tests. The server is started with
# FLOWA_PREVIEW_BASE=http://127.0.0.1:3199 and then asks this stub at /<host><path> instead of the
# real sites. Pictures are plain PNGs written here, so no image library is needed.
import threading,json,zlib,struct,http.server,urllib.parse
PORT=3199;BASE=f'http://127.0.0.1:{PORT}'
def png(w,h,rgb):
    raw=b''.join(b'\x00'+bytes(rgb)*w for _ in range(h))
    chunk=lambda t,d: struct.pack('>I',len(d))+t+d+struct.pack('>I',zlib.crc32(t+d)&0xffffffff)
    return b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',w,h,8,2,0,0,0))+chunk(b'IDAT',zlib.compress(raw))+chunk(b'IEND',b'')
WIDE=png(480,270,(40,90,170));TALL=png(270,480,(170,60,90))
hits=[]
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self,*a): pass
    def send(self,body,ctype='application/json',code=200,loc=None):
        self.send_response(code)
        if loc: self.send_header('Location',loc)
        self.send_header('Content-Type',ctype);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
    def do_GET(self):
        u=urllib.parse.urlparse(self.path);q=urllib.parse.parse_qs(u.query);p=u.path;hits.append(p)
        if p=='/www.youtube.com/oembed':
            vid=urllib.parse.parse_qs(urllib.parse.urlparse(q['url'][0]).query).get('v',['x'])[0] if 'watch' in q['url'][0] else q['url'][0].rsplit('/',1)[-1]
            return self.send(json.dumps({'title':'Six directions drill','author_name':'Flowa Spins','thumbnail_url':f'https://i.ytimg.com/vi/{vid}/hqdefault.jpg'}).encode())
        if p.startswith('/i.ytimg.com/'): return self.send(WIDE,'image/png')
        if p=='/www.tiktok.com/oembed': return self.send(json.dumps({'title':'Poi flowers, slow','author_name':'sam','thumbnail_url':'https://p16.tiktokcdn.com/obj/flowers.png'}).encode())
        if p.startswith('/p16.tiktokcdn.com/'): return self.send(TALL,'image/png')
        if p.startswith('/vm.tiktok.com/'): return self.send(b'',code=302,loc=BASE+'/www.tiktok.com/@sam/video/7301234567890123456')
        if p.startswith('/www.tiktok.com/@'): return self.send(b'<html></html>','text/html')
        if p.startswith('/www.instagram.com/'): return self.send(b'<html><head><meta property="og:title" content="Staff spins on the beach"><meta property="og:image" content="https://scontent.cdninstagram.com/v/reel.png"></head></html>','text/html')
        if p.startswith('/scontent.cdninstagram.com/'): return self.send(TALL,'image/png')
        self.send(b'not found','text/plain',404)
def start():
    srv=http.server.ThreadingHTTPServer(('127.0.0.1',PORT),H)
    threading.Thread(target=srv.serve_forever,daemon=True).start()
    return srv
