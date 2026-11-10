'use strict';
// Which links can become cards: the page's copy of parseLink in main.go. The composer offers a
// card only for a link this accepts, so a card that is offered is one the server will take.
// test/links-fixtures.json holds the cases both must read the same way (go test and
// node test/links.js check it); change the two together.
const linkCheck = (() => {
  const YT = /^[A-Za-z0-9_-]{11}$/, IG = /^[A-Za-z0-9_-]{5,40}$/, TT = /^[0-9]{8,25}$/,
    TT_USER = /^@[A-Za-z0-9._]{1,40}$/, TT_SHORT = /^[A-Za-z0-9]{5,20}$/;

  // Percent escapes read one byte at a time, as Go reads them; null when one is broken. Every
  // name and id that matters is plain ASCII, so bytes above it never need joining into letters.
  const unesc = (s, plus) => {
    if (/%(?![0-9A-Fa-f]{2})/.test(s)) return null;
    if (plus) s = s.replace(/\+/g, ' ');
    return s.replace(/%([0-9A-Fa-f]{2})/g, (_, h) => String.fromCharCode(parseInt(h, 16)));
  };

  // The first value of a query key. A pair with a semicolon or a broken escape is skipped.
  const queryGet = (q, name) => {
    for (const pair of q.split('&')) {
      if (!pair || pair.includes(';')) continue;
      const i = pair.indexOf('='), k = unesc(i < 0 ? pair : pair.slice(0, i), true),
        v = unesc(i < 0 ? '' : pair.slice(i + 1), true);
      if (k === null || v === null) continue;
      if (k === name) return v;
    }
    return '';
  };

  // parseLink returns { url, site, kind, id } with the link in canonical form, or null.
  function parseLink(raw) {
    raw = String(raw).trim();
    if (!raw.includes('://')) raw = 'https://' + raw;
    if (/[\x00-\x1f\x7f]/.test(raw)) return null;
    // scheme://name/path?query#fragment. The name takes letters, digits, dots and dashes only
    // (no account before an @, no port, no escapes) and ends at a slash, a ? or a #.
    const m = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([A-Za-z0-9.-]+)((?:\/[^?#]*)?)(?:\?([^#]*))?(?:#(.*))?$/.exec(raw);
    if (!m) return null;
    const scheme = m[1].toLowerCase();
    if (scheme !== 'https' && scheme !== 'http') return null;
    const path = unesc(m[3]);
    if (path === null || unesc(m[5] || '') === null) return null;
    let host = m[2].toLowerCase();
    if (host.startsWith('www.')) host = host.slice(4);
    if (host.startsWith('m.')) host = host.slice(2);
    let parts = path.split('/').filter(Boolean);
    const out = (url, site, kind, id) => ({ url, site, kind, id });
    switch (host) {
      case 'youtube.com': case 'music.youtube.com': {
        let id = '', kind = 'video';
        if (parts.length === 1 && parts[0] === 'watch') id = queryGet(m[4] || '', 'v');
        else if (parts.length === 2 && ['shorts', 'live', 'embed'].includes(parts[0])) {
          id = parts[1];
          if (parts[0] === 'shorts') kind = 'short';
        }
        if (!YT.test(id)) return null;
        return kind === 'short' ? out('https://www.youtube.com/shorts/' + id, 'youtube', kind, id)
          : out('https://www.youtube.com/watch?v=' + id, 'youtube', kind, id);
      }
      case 'youtu.be':
        if (parts.length === 1 && YT.test(parts[0])) return out('https://www.youtube.com/watch?v=' + parts[0], 'youtube', 'video', parts[0]);
        break;
      case 'instagram.com':
        // /p/CODE, /reel/CODE, /reels/CODE, /tv/CODE, optionally after the account name.
        if (parts.length >= 3 && ['p', 'reel', 'reels', 'tv'].includes(parts[1])) parts = parts.slice(1);
        if (parts.length >= 2 && IG.test(parts[1])) {
          if (parts[0] === 'p' || parts[0] === 'tv') return out('https://www.instagram.com/p/' + parts[1] + '/', 'instagram', 'post', parts[1]);
          if (parts[0] === 'reel' || parts[0] === 'reels') return out('https://www.instagram.com/reel/' + parts[1] + '/', 'instagram', 'reel', parts[1]);
        }
        break;
      case 'tiktok.com': {
        if (parts.length === 3 && TT_USER.test(parts[0]) && parts[1] === 'video' && TT.test(parts[2])) return out('https://www.tiktok.com/' + parts[0] + '/video/' + parts[2], 'tiktok', 'video', parts[2]);
        const id = parts.length === 2 ? parts[1].replace(/\.html$/, '') : '';
        if (parts.length === 2 && parts[0] === 'v' && TT.test(id)) return out('https://m.tiktok.com/v/' + id + '.html', 'tiktok', 'video', id);
        if (parts.length === 2 && parts[0] === 't' && TT_SHORT.test(parts[1])) return out('https://www.tiktok.com/t/' + parts[1] + '/', 'tiktok', 'shortlink', parts[1]);
        break;
      }
      case 'vm.tiktok.com': case 'vt.tiktok.com':
        if (parts.length === 1 && TT_SHORT.test(parts[0])) return out('https://' + host + '/' + parts[0] + '/', 'tiktok', 'shortlink', parts[0]);
        break;
    }
    return null;
  }
  return { parseLink };
})();
if (typeof module !== 'undefined') module.exports = linkCheck;
