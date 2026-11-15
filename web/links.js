'use strict';
// Which links can become cards: the page's copy of parseLink in main.go. The composer offers a
// card only for a link this accepts, so a card that is offered is one the server will take.
// test/links-fixtures.json holds the cases both must read the same way (go test and
// node test/links.js check it); change the two together.
// Also the page's copy of parseMentions in main.go (mentionsIn): which @names in a text are
// members here and which are handles of other servers, held to test/mentions-fixtures.json by
// TestMentions and node test/mentions.js.
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
  // mentionsIn finds the mentions in a text as the server's parseMentions does: [{ start, end,
  // name, host, local }], start at the @ and end after the name or the host. @name is a member
  // here (a letter or digit, then up to 23 more of letters, digits, dot, dash and underscore; the
  // name in lower case); @name@host, the host an ASCII DNS name of two labels or more whose last
  // label holds a letter (a full stop after it is not part of it) and that ends before anything
  // but another @, an underscore or a letter, digit or mark, is an account elsewhere. Anything
  // shaped @name@... is never a member here: with a host that is no such name it is plain text,
  // whole. own is this server's host ('' when it has none): @name@own is the member here.
  const MENTION = /(^|[^A-Za-z0-9._-])@([A-Za-z0-9._-]+)/g;
  const LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
  const hostOK = h => {
    const labels = h.toLowerCase().split('.');
    return h.length <= 253 && labels.length >= 2 && labels.every(l => LABEL.test(l)) && /[a-z]/.test(labels[labels.length - 1]);
  };
  const LOCAL = /^[a-z0-9][a-z0-9._-]{2,23}/;
  function mentionsIn(text, own = '') {
    const out = [], s = String(text);
    for (const m of s.matchAll(MENTION)) {
      const at = m.index + m[1].length, run = m[2], end = at + 1 + run.length;
      if (s[end] === '@') {
        let j = end + 1;
        while (j < s.length && /[A-Za-z0-9.-]/.test(s[j])) j++;
        const host = s.slice(end + 1, j).replace(/\.+$/, '');
        if ((j < s.length && /[@_\p{L}\p{N}\p{M}]/u.test(String.fromCodePoint(s.codePointAt(j)))) || !hostOK(host)) continue;
        if (own && host.toLowerCase() === own.toLowerCase()) {
          const l = run.toLowerCase();
          if ((LOCAL.exec(l) || [''])[0] === l) out.push({ start: at, end: end + 1 + host.length, name: l, host, local: true });
        } else if (run.length <= 64) out.push({ start: at, end: end + 1 + host.length, name: run, host, local: false });
        continue;
      }
      const l = LOCAL.exec(run.toLowerCase());
      if (l) out.push({ start: at, end: at + 1 + l[0].length, name: l[0], host: '', local: true });
    }
    return out;
  }
  // linkRuns cuts a text into runs and links as the server's textParts reads a post (main.go, after markText): a
  // link is http or https up to a space, a control character, a mark that turns the text's direction, <, > or ",
  // less the punctuation that ends a sentence after it (.,;:!?)'). [{ text, url }], url '' for a run of text. The page
  // links what the server links, and reads mentions only in the runs of text, as the server does.
  const URL_RE = /https?:\/\/[^\x00-\x20\x7f-\x9f<>"\u202a-\u202e\u2066-\u2069]+/g;
  function linkRuns(text) {
    const s = String(text ?? ''), out = [];
    let at = 0;
    for (const m of s.matchAll(URL_RE)) {
      let end = m.index + m[0].length;
      while (end > m.index && ".,;:!?)'".includes(s[end - 1])) end--;
      if (at < m.index) out.push({ text: s.slice(at, m.index), url: '' });
      out.push({ text: s.slice(m.index, end), url: s.slice(m.index, end) });
      at = end;
    }
    if (at < s.length) out.push({ text: s.slice(at), url: '' });
    return out;
  }
  // textMentions is who a whole text tells here, as the server's mentionNames does: the members mentioned in its runs
  // of text, in order, without repeats, at most 10.
  function textMentions(text, own = '') {
    const out = [];
    for (const r of linkRuns(text)) {
      if (r.url) continue;
      for (const m of mentionsIn(r.text, own)) if (m.local && !out.includes(m.name) && out.length < 10) out.push(m.name);
    }
    return out;
  }
  return { parseLink, mentionsIn, linkRuns, textMentions };
})();
if (typeof module !== 'undefined') module.exports = linkCheck;
