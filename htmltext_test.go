package main

// Remote HTML as text (docs/federation.md 6.6): htmlToText over hostile inputs, over every post and actor
// under testdata/federation (none recorded yet: section 13), and a fuzz target for what must hold of any
// input.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, dir := range []string{"testdata/federation", "testdata/federation/shapes", "testdata/federation/actors"} {
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
