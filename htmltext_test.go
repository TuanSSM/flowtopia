package main

// Remote HTML as text (docs/federation.md 6.6): htmlToText over hostile inputs, over every post and actor
// under testdata/federation (none recorded yet: section 13), and a fuzz target for what must hold of any
// input.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

func TestRemoteHTMLToText(t *testing.T) {
	bo := []apMention{{Href: "https://social.example/users/bo", Name: "@bo@social.example"}}
	cats := []apHashtag{{Name: "#cats", Href: "https://example.com/tagged/cats"}}
	for _, c := range []struct {
		name, in string
		cut      bool
		want     string
	}{
		{"plain text", "I love cats", false, "I love cats"},
		{"paragraphs and breaks", "<p>One<br>two</p><p>Three</p>", false, "One\ntwo\n\nThree"},
		{"a list", "<ul><li>a</li><li>b</li></ul>", false, "a\nb"},
		{"entities decoded once", "<p>&lt;b&gt;bold&lt;/b&gt; &amp;amp; &#x27;q&#39; &copy;</p>", false, "<b>bold</b> &amp; 'q' ©"},
		{"a link with other text keeps its address", `see <a href="https://example.com/drill">the drill</a>.`, false, "see the drill (https://example.com/drill)."},
		{"a link whose text is its address", `<a href="https://example.com/drill" rel="nofollow">https://example.com/drill</a>`, false, "https://example.com/drill"},
		{"Mastodon's hidden spans", `<a href="https://example.com/a/very/long/path"><span class="invisible">https://</span><span class="ellipsis">example.com/a/very</span><span class="invisible">/long/path</span></a>`, false, "https://example.com/a/very/long/path"},
		{"a link without scheme in its text", `<a href="https://example.com/">example.com</a>`, false, "example.com"},
		{"an empty link shows its address", `<a href="https://example.com/x"></a>`, false, "https://example.com/x"},
		{"a javascript href is not kept", `<a href="javascript:alert(1)">click</a>`, false, "click"},
		{"an entity in an href", `<a href="https://example.com/?a=1&amp;b=2">q</a>`, false, "q (https://example.com/?a=1&b=2)"},
		{"a mention by its profile link", `<p>Thanks <span class="h-card"><a href="https://social.example/@bo" class="u-url mention">@<span>bo</span></a></span>!</p>`, false, "Thanks @bo@social.example!"},
		{"a mention by its actor id", `<a href="https://social.example/users/bo">@bo</a>`, false, "@bo@social.example"},
		{"a short @name of another host is no mention", `<a href="https://evil.example/@bo">@bo</a>`, false, "@bo (https://evil.example/@bo)"},
		{"a hashtag keeps its text", `I love <a href="https://example.com/tagged/cats" rel="tag">#<span>cats</span></a>`, false, "I love #cats"},
		{"script and style dropped", "a<script>alert('<p>x</p>')</script>b<STYLE>p{}</Style>c", false, "abc"},
		{"a script never closed", "a<script>alert(1)", false, "a"},
		{"comments and CDATA dropped", "a<!-- <p>no</p> -->b<![CDATA[ <x> ]]>c<!DOCTYPE html>d<?xml x?>e", false, "abcde"},
		{"a comment never closed", "a<!-- b", false, "a"},
		{"> inside a quoted attribute", `<a title="1 > 0" href='https://example.com/q'>x</a>y`, false, "x (https://example.com/q)y"},
		{"an unquoted attribute", `<a href=https://example.com/u>u</a>`, false, "u (https://example.com/u)"},
		{"a < that starts no tag", "1 < 2 and 3 <= 4", false, "1 < 2 and 3 <= 4"},
		{"an unclosed tag at the end", "text <a href=\"https://x", false, "text"},
		{"an unclosed attribute at the end, cut", `text <span class="h-car`, true, "text"},
		{"an entity cut at the end", "fish &amp chips &am", true, "fish & chips"},
		{"an entity cut at the end, not cut", "fish &am", false, "fish &am"},
		{"a numeric entity cut", "x &#12", true, "x"},
		{"a whole entity at the end of a cut text", "x &amp;", true, "x &"},
		{"invalid UTF-8", "a\xffb", false, "a\uFFFDb"},
		{"control characters and direction marks", "a\x00b\x07c\u202ed\u2066e\tf", false, "abcde f"},
		{"spaces and blank lines tidied", "  <p> a  b </p>\n\n\n<p>c</p>  ", false, "a b\n\nc"},
		{"nested links: the first ends at the second", `<a href="https://a.example/1">one<a href="https://a.example/2">two</a>`, false, "one (https://a.example/1)two (https://a.example/2)"},
		{"tags in another case", "<P>a<BR/>b</P>", false, "a\nb"},
		// FWR 5's hostile corpus: what a remote post may carry to make a page run or show markup.
		{"event attributes", `<img src=x onerror="alert(1)">a<b onclick='steal()'>b</b><svg onload=alert(1)>c`, false, "abc"},
		{"a data: link", `<a href="data:text/html,<script>alert(1)</script>">d</a>`, false, "d"},
		{"a javascript: link hidden by an entity", `<a href="jav&#x61;script:alert(1)">j</a>`, false, "j"},
		{"a javascript: link in capitals", `<a href="JAVASCRIPT:alert(1)">j</a>`, false, "j"},
		{"an http link in capitals is not taken either", `<a href="HTTPS://example.com/">e</a>`, false, "e"},
		{"numeric entities that make markup are text", "&#60;b&#62;bold&#x3C;/b&#x3e;", false, "<b>bold</b>"},
		{"an overlong numeric entity", "a&#x110000;b&#99999999999999999999;c", false, "a�b�c"},
		{"a NUL by entity", "a&#0;b", false, "a�b"},
		{"nested tags never closed", "<div><p><b>bold<i>it<u>", false, "boldit"},
		{"script in capitals with attributes", `<SCRIPT type="module">bad()</SCRIPT >ok`, false, "ok"},
		{"a style with another closing tag in a string", `<style>p::after{content:"</p>"}</style>x`, false, "x"},
		{"a tag split around another", "<scr<script>ipt>alert(1)</script>ok", false, "ipt>alert(1)ok"},
		{"a CDATA never closed", "a<![CDATA[ <b>", false, "a"},
		{"an iframe and an object", `<iframe src="https://evil.example/"></iframe><object data="x"></object>ok`, false, "ok"},
		{"a remote picture is no picture", `<img src="https://evil.example/track.gif" alt="tracker">seen`, false, "seen"},
		{"a heading and a quote end their lines", "<h2>Title</h2><blockquote><p>q</p></blockquote>after", false, "Title\nq\n\nafter"},
		{"bidi isolates and overrides", "a\u2067b\u2069c\u202dd\u200fe", false, "abcde"},
	} {
		if got := htmlToText(c.in, bo, cats, c.cut); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}

	// 10,000 nested tags, a body of tags with no end, and a run of comment openers: each in time linear in
	// its length (the fuzz target covers the rest), and nothing of the markup left.
	deep := strings.Repeat("<div><span>", 10000) + "deep" + strings.Repeat("</span></div>", 10000)
	if got := htmlToText(deep, nil, nil, false); got != "deep" {
		t.Errorf("nested: %q", got)
	}
	if got := htmlToText(strings.Repeat("<a href='https://x.example/'>", 5000), nil, nil, false); strings.Contains(got, "<") {
		t.Errorf("open links: %q", got[:min(len(got), 80)])
	}
	if got := htmlToText(strings.Repeat("<!--", 20000)+"x", nil, nil, false); got != "" {
		t.Errorf("comment openers: %q", got)
	}
	// A megabyte of nesting: 100,000 opened tags and as many closed, in one pass with no stack.
	if got := htmlToText(strings.Repeat("<div>", 100000)+"core"+strings.Repeat("</div>", 100000), nil, nil, false); got != "core" {
		t.Errorf("a megabyte of nesting: %q", got[:min(len(got), 80)])
	}
	// A text the decoder cut at 64 KB (apNote.Cut) may end inside a tag, an attribute, a comment or an entity: the
	// unterminated tail goes, and the post still ends with the link to the original (remotePost).
	for what, tail := range map[string]string{"a tag": `<a href="https://x.example/`, "an attribute": `<span class="h-car`, "a comment": "<!-- a note",
		"an entity": "&am", "a numeric entity": "&#12", "a script": "<script>alert(1)"} {
		n := &apNote{ID: "https://social.example/users/bo/statuses/1", Kind: "Note", Content: "<p>Hello there</p>" + tail, Cut: true}
		body, _, _ := remotePost(n, boID, time.Now())
		if want := "Hello there\n\n" + remoteReadRest + " https://social.example/users/bo/statuses/1"; body != want {
			t.Errorf("cut inside %s: %q, want %q", what, body, want)
		}
	}
	if got, cut := cutText(strings.Repeat("é", 2001), remoteTextMax); !cut || utf8.RuneCountInString(got) != remoteTextMax {
		t.Errorf("cut at %d characters: %d, %v", remoteTextMax, utf8.RuneCountInString(got), cut)
	}
	if got, cut := cutText("short", remoteTextMax); cut || got != "short" {
		t.Errorf("a short text was cut: %q", got)
	}
}

// Every post and actor this repository holds as a document from elsewhere (the shapes copied from the
// specifications and Mastodon's page, the actors written from them, Flowa's own golden files: none recorded,
// section 13) converts to text with no markup left.
func TestRemoteHTMLFixtures(t *testing.T) {
	n := 0
	for _, dir := range []string{"testdata/federation", "testdata/federation/shapes", "testdata/federation/actors", "testdata/federation/notes"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, p := range files {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			check := func(what, html string, m []apMention, h []apHashtag) {
				if html == "" {
					return
				}
				n++
				got := htmlToText(html, m, h, false)
				if strings.ContainsAny(got, "<>") && !strings.Contains(html, "&lt;") && !strings.Contains(html, "&gt;") {
					t.Errorf("%s %s: markup left in %q", p, what, got)
				}
			}
			if note, err := decodeNote(b, apMax); err == nil {
				check("content", note.text(), note.Mentions, note.Hashtags)
				check("summary", note.Summary, nil, nil)
			}
			if act, err := decodeActivity(b, apMax); err == nil && len(act.Object) == 1 {
				if note, err := act.Object[0].note(); err == nil {
					check("object content", note.text(), note.Mentions, note.Hashtags)
				}
			}
			if a, err := decodeActor(b, apMax); err == nil {
				check("summary", a.Summary, nil, nil)
			}
		}
	}
	if n < 10 {
		t.Errorf("only %d texts converted: the fixtures moved?", n)
	}
	// The post of 6.5 as Flowa sends it: the mention becomes the full handle, the hashtag its text.
	b, _ := os.ReadFile("testdata/federation/shapes/flowa-6.5-create.json")
	act, err := decodeActivity(b, apMax)
	if err != nil {
		t.Fatal(err)
	}
	note, err := act.Object[0].note()
	if err != nil {
		t.Fatal(err)
	}
	want := "Finally clean! Thanks @bo@social.example for the drill\n\nTricks: Six basic figure-8s (nunchaku) #nunchaku"
	if got := htmlToText(note.text(), note.Mentions, note.Hashtags, note.Cut); got != want {
		t.Errorf("the post of 6.5: %q, want %q", got, want)
	}
}

// What holds of any input: the text is valid UTF-8 with no control character but a line break and no
// direction mark, has no blank line twice in a row, never starts or ends with space, and is no longer than a
// bound linear in the input (each byte once, each href once more, and each mention's handle).
func FuzzHTMLToText(f *testing.F) {
	for _, s := range []string{"<p>a</p>", `<a href="https://e.example/">x</a>`, "<script>", "<!--", "&amp", "<a href='", "a\xffb", "<p>Thanks <a href=\"https://social.example/@bo\">@<span>bo</span></a></p>"} {
		f.Add(s, false)
		f.Add(s, true)
	}
	bo := []apMention{{Href: "https://social.example/users/bo", Name: "@bo@social.example"}}
	f.Fuzz(func(t *testing.T, in string, cut bool) {
		got := htmlToText(in, bo, nil, cut)
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8 from %q", in)
		}
		for _, r := range got {
			if r != '\n' && (unicode.IsControl(r) || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069') {
				t.Fatalf("%U left in %q from %q", r, got, in)
			}
		}
		if strings.Contains(got, "\n\n\n") || strings.TrimSpace(got) != got {
			t.Fatalf("untidy %q from %q", got, in)
		}
		if bound := 3*len(in) + 3*strings.Count(in, "<")*len("@bo@social.example") + 16; len(got) > bound {
			t.Fatalf("%d bytes from %d", len(got), len(in))
		}
	})
}

// The cost of a 64 KB text, the most the decoder hands on (7.5), of each kind of markup.

func BenchmarkHTMLToText(b *testing.B) {
	for _, c := range []struct{ name, unit string }{
		{"paragraphs", "<p>Some text with a <a href=\"https://e.example/x\">link</a> &amp; more.</p>"},
		{"tags", "<span>"},
		{"styles", "<style>"},
		{"links", "<a href=\"https://e.example/x\">"},
		{"declarations", "<!x"},
		{"amps", "&amp"},
		{"lt", "< "},
	} {
		in := strings.Repeat(c.unit, (64<<10)/len(c.unit))
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				htmlToText(in, nil, nil, true)
			}
		})
	}
}

// The fixtures of testdata/federation/notes (written from documentation, their README says from where): each
// remote post as Flowa keeps it, the text, what is shown beside it, and the attachments to fetch or link.
func TestRemotePostFixtures(t *testing.T) {
	read := func(name string) *apNote {
		t.Helper()
		b, err := os.ReadFile("testdata/federation/notes/" + name)
		if err != nil {
			t.Fatal(err)
		}
		act, err := decodeActivity(b, apMax)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		n, err := act.Object[0].note()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !noteShaped(act) {
			t.Fatalf("%s: not taken by the inbox", name)
		}
		return n
	}
	for _, c := range []struct {
		file, body, cw, kind string
		sensitive            bool
		media                []remoteAttachment
	}{
		{"cw.json", "The last move is a reverse figure-8 into a catch.\n\nTook me a month.", "Spoilers: the ending of the routine", "Note", true,
			[]remoteAttachment{{"picture", "https://files.social.example/media/101.jpg", "A nunchaku caught behind the back", true}}},
		{"poll.json", "What should I practise today?\n• apple\n• orange\n• banana\n\n" + remotePollSays + " https://social.example/@bo/102", "", "Question", false, nil},
		{"attachments.json", "Four kinds of media.", "", "Note", false, []remoteAttachment{
			{"picture", "https://files.social.example/media/a.png", "Two poi in a fire circle", true},
			{"picture", "https://files.social.example/media/b.webp", "A webp picture", false}, // WebP: a link, the standard library cannot decode it
			{"video", "https://files.social.example/media/c.mp4", "A video of the flow", false},
			{"audio", "https://files.social.example/media/d.mp3", "The music", false},
			{"picture", "https://files.social.example/media/e.gif", "A spinning staff, as a GIF", true}}},
		{"quote.json", "RE: https://social.example/@cat/90\n\nSo true.", "", "Note", false, nil},
		{"hashtags-emoji.json", "Practice day :blobcat: #poi", "", "Note", false, nil},
		{"followers-only.json", "Only for my followers, and @ana@here.example.", "", "Note", false, nil},
		{"mention.json", "Thanks @anna@social.example and @cyd@here.example! Also @anna, written plainly.", "", "Note", false, nil},
	} {
		n := read(c.file)
		body, v, media := remotePost(n, boID, time.Now())
		if body != c.body || v.CW != c.cw || v.Kind != c.kind || v.Sensitive != c.sensitive || !slices.Equal(media, c.media) {
			t.Errorf("%s:\n got %q %q %s %v %v\nwant %q %q %s %v %v", c.file, body, v.CW, v.Kind, v.Sensitive, media, c.body, c.cw, c.kind, c.sensitive, c.media)
		}
		if _, num, _ := strings.Cut(n.ID, "/statuses/"); v.Link != "https://social.example/@bo/"+num || v.Published == nil {
			t.Errorf("%s: link %q, published %v", c.file, v.Link, v.Published)
		}
	}
	// The long Article: its title, its first 500 characters, then the link; its heading on a line of its own.
	n := read("article.json")
	body, v, _ := remotePost(n, boID, time.Now())
	text, _, _ := strings.Cut(body, "\n\n"+remoteReadRest)
	if v.Title != "A long note on learning the figure-8" || v.Kind != "Article" || !strings.HasPrefix(body, "Why the figure-8\nFlow arts are") ||
		!strings.HasSuffix(body, remoteReadRest+" https://social.example/@bo/106") || utf8.RuneCountInString(text) != remoteLongMax+1 || !strings.HasSuffix(text, "…") {
		t.Errorf("the article: %q %+v (%d characters before the link)", body, v, utf8.RuneCountInString(text))
	}
	// The direct message reads too; its addressing is what drops it (TestDBRemoteNotes).
	if aud, _ := noteAudience(read("direct.json"), &apActivity{}, boID+"/followers"); aud != "" {
		t.Errorf("the direct message is read as %q", aud)
	}
	if aud, _ := noteAudience(read("followers-only.json"), &apActivity{}, boID+"/followers"); aud != "followers" {
		t.Errorf("the followers-only post is read as %q", aud)
	}
	// A url on another host than the author's is not offered as the original: the post's id is.
	n = &apNote{ID: boID + "/statuses/9", URL: []apLink{{Href: "https://phish.example/login"}}}
	if l := remoteLink(n, boID); l != n.ID {
		t.Errorf("a url on another host is offered: %s", l)
	}
}

// What holds of any remote post: no "<" in its text that markup made (each is a "<" the input had as text, since
// the input holds no entity), and a text the decoder cut ends with the link to the original.
func FuzzRemotePost(f *testing.F) {
	for _, s := range []string{"<p>a</p>", "<<b>x", "a < b", "<a href='", "&lt;b&gt;", "<!--", "<script>x", strings.Repeat("é", 2100)} {
		f.Add(s, false)
		f.Add(s, true)
	}
	const id = "https://social.example/users/bo/statuses/1"
	f.Fuzz(func(t *testing.T, in string, cut bool) {
		for _, kind := range []string{"Note", "Article", "Question"} {
			n := &apNote{ID: id, Kind: kind, Content: in, Summary: in, Name: "A title", Cut: cut, Options: []string{"yes", "no"}}
			body, v, _ := remotePost(n, boID, time.Now())
			if cut && !strings.HasSuffix(body, remoteReadRest+" "+id) && !strings.HasSuffix(body, remotePollSays+" "+id) {
				t.Fatalf("%s cut, without the link: %q from %q", kind, body, in)
			}
			if strings.Contains(in, "&") {
				continue
			}
			textLt := 0
			for i := 0; i < len(in); i++ {
				// A "<" that starts no tag, comment or declaration (as htmlToText reads one) is text.
				if in[i] == '<' && !strings.HasPrefix(in[i:], "<!") && !strings.HasPrefix(in[i:], "<?") && !(i+1 < len(in) && isAlpha(in[i+1])) && !(i+2 < len(in) && in[i+1] == '/' && isAlpha(in[i+2])) {
					textLt++
				}
			}
			// What is shown is made from the content and the summary, each the input once (a name and a choice are plain text,
			// AS2 Core 4.7: their "<" are characters the page escapes, so they are held fixed here).
			if got := strings.Count(body, "<") + strings.Count(v.CW, "<") + strings.Count(v.Title, "<"); got > 2*textLt {
				t.Fatalf("%s: %d \"<\" from %d in the input's text: %q", kind, got, textLt, body)
			}
		}
	})
}
