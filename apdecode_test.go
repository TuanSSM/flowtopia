package main

// The decoder for the shapes activities arrive in (docs/federation.md 6.3, 7.3, 7.5): every shape
// a property may take, the readers, every refusal, the fuzz targets and the cost. The documents in
// testdata/federation/shapes are written from documentation, never recorded (their README says
// from where); the rest are written here, each case saying what it follows.

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const apMax = 1 << 20 // a fetched document's cap (federation.md 7.5)

func shapeDoc(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata/federation/shapes", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// apOut is a case's result in one line: the value, or the refusal's reason and path.
func apOut(v any, err error) string {
	var e *apError
	switch {
	case errors.As(err, &e):
		return "refused: " + e.Reason.String() + " @" + e.Field
	case err != nil:
		return "untyped error: " + err.Error()
	}
	return fmt.Sprint(v)
}

func apReasonOf(err error) apReason {
	var e *apError
	if errors.As(err, &e) {
		return e.Reason
	}
	return 0
}

// TestDecodeShapes reads each property Flowa reads in each shape the texts allow, and the shapes
// they do not, from a one-object document. A document that the scan or the root refuses gives that
// refusal.
func TestDecodeShapes(t *testing.T) {
	type read func(apFields) (any, error)
	ref := func(n string) read {
		return func(f apFields) (any, error) {
			rs, err := f.refs(n)
			var out []string
			for _, r := range rs {
				s := r.ID
				if r.Obj != nil {
					s = "{" + r.ID + " " + r.Kind + "}"
				}
				out = append(out, s)
			}
			return out, err
		}
	}
	one := func(n string) read { return func(f apFields) (any, error) { return f.id(n) } }
	text := func(n string) read { return func(f apFields) (any, error) { return f.text(n, apTextMax) } }
	flag := func(n string) read { return func(f apFields) (any, error) { return f.flag(n) } }
	links := func(n string) read { return func(f apFields) (any, error) { return f.links(n) } }
	kind := func(f apFields) (any, error) {
		ts, k, err := f.types()
		return fmt.Sprint(ts, " ", k), err
	}
	date := func(f apFields) (any, error) {
		d, err := f.date("published")
		return d.UTC().Format(time.RFC3339), err
	}
	aud := func(f apFields) (any, error) {
		a, err := f.audience()
		return fmt.Sprint(a.To, a.Cc, a.Bto, a.Bcc, a.Audience), err
	}
	keys := func(f apFields) (any, error) {
		ks, err := f.keys()
		var out []string
		for _, k := range ks {
			out = append(out, fmt.Sprintf("%s owner=%s pem=%q byid=%v", k.ID, k.Owner, k.PEM, k.ByID))
		}
		return out, err
	}
	note := func(get func(*apNote) any) read {
		return func(f apFields) (any, error) {
			n, err := noteFrom(f)
			if err != nil {
				return nil, err
			}
			return get(n), nil
		}
	}
	file := func(name string) string { return string(shapeDoc(t, name)) }
	const n = `"id":"https://b.example/n/1","type":"Note",`

	cases := []struct {
		name, doc string
		read      read
		want      string
	}{
		// type: one name or many; the vocabulary's names short, compact or full (AS2 Core 5.1).
		{"type one", `{"type":"Note"}`, kind, "[Note] Note"},
		{"type list of one", `{"type":["Note"]}`, kind, "[Note] Note"},
		{"type with an extension", `{"type":["Note","toot:Thing"]}`, kind, "[Note toot:Thing] Note"},
		{"type compact", `{"type":"as:Note"}`, kind, "[Note] Note"},
		{"type full IRI", `{"type":"https://www.w3.org/ns/activitystreams#Note"}`, kind, "[Note] Note"},
		{"type extension only", `{"type":"EmojiReact"}`, kind, "[EmojiReact] "},
		{"type two kinds", `{"type":["Person","Service"]}`, kind, "refused: a type list naming two kinds @type"},
		{"type a number", `{"type":5}`, kind, "refused: a value of the wrong kind @type"},
		{"type an object", `{"type":{"id":"x"}}`, kind, "refused: a value of the wrong kind @type"},
		{"type nested", `{"type":[["Note"]]}`, kind, "refused: a list inside a list @type"},
		{"type null", `{"type":null}`, kind, "refused: an object without a type @type"},
		{"type empty list", `{"type":[]}`, kind, "refused: an object without a type @type"},
		{"type absent", `{}`, kind, "refused: an object without a type @type"},

		// An id as a string, an embedded object, a list of one, a list of both (AS2 Core 4.2).
		{"object string", `{"object":"https://a.example/1"}`, ref("object"), "[https://a.example/1]"},
		{"object embedded", `{"object":{"id":"https://a.example/1","type":"Note"}}`, ref("object"), "[{https://a.example/1 Note}]"},
		{"object list of one", `{"object":["https://a.example/1"]}`, ref("object"), "[https://a.example/1]"},
		{"object list of both", `{"object":["https://a.example/1",{"id":"https://a.example/2","type":"Person"}]}`, ref("object"), "[https://a.example/1 {https://a.example/2 Person}]"},
		{"object null", `{"object":null}`, ref("object"), "[]"},
		{"object absent", `{}`, ref("object"), "[]"},
		{"object null in a list", `{"object":[null,"https://a.example/1"]}`, ref("object"), "[https://a.example/1]"},
		{"object a number", `{"object":7}`, ref("object"), "refused: a value of the wrong kind @object"},
		{"object nested", `{"object":[["https://a.example/1"]]}`, ref("object"), "refused: a list inside a list @object"},
		{"object empty string", `{"object":""}`, ref("object"), "refused: an id that is not an https URL that may be fetched @object"},
		{"object with @id", `{"object":{"@id":"https://a.example/1"}}`, ref("object"), "refused: @id or @type in place of id or type @object"},
		{"object with a number id", `{"object":{"id":1}}`, ref("object"), "refused: a value of the wrong kind @object.id"},
		{"actor one of a list", `{"actor":["https://a.example/u"]}`, one("actor"), "https://a.example/u"},
		{"actor embedded", `{"actor":{"type":"Person","id":"https://a.example/u"}}`, one("actor"), "https://a.example/u"},
		{"actor two", `{"actor":["https://a.example/u","https://a.example/v"]}`, one("actor"), "refused: more than one value where one is read @actor"},
		{"actor embedded without id", `{"actor":{"type":"Person","name":"Sally"}}`, one("actor"), "refused: an object without an id @actor"},
		{"actor full IRI", `{"https://www.w3.org/ns/activitystreams#actor":"https://a.example/u"}`, one("actor"), "https://a.example/u"},
		{"actor compact", `{"as:actor":"https://a.example/u"}`, one("actor"), "https://a.example/u"},
		{"inbox ldp IRI", `{"http://www.w3.org/ns/ldp#inbox":"https://a.example/inbox"}`, one("inbox"), "https://a.example/inbox"},
		{"inReplyTo embedded", `{"inReplyTo":{"id":"https://a.example/n/0","type":"Note"}}`, one("inReplyTo"), "https://a.example/n/0"},
		{"attributedTo list", `{"attributedTo":["https://a.example/u",{"type":"Group","id":"https://a.example/g"}]}`, func(f apFields) (any, error) { return f.ids("attributedTo") }, "[https://a.example/u https://a.example/g]"},
		{"Move target (Mastodon)", file("mastodon-move.json"), one("target"), "https://alice.com/users/109835986274379"},

		// Addressing (federation.md 6.7; ActivityPub 5.6: the public collection's three forms).
		{"to one string (Mastodon Block)", file("mastodon-block.json"), aud, "[https://example.com/~mallory] [] [] [] []"},
		{"to and cc lists (ActivityPub Example 16)", file("ap-ex16-create.json"), aud, "[https://example.org/~john/] [https://example.com/~erik/followers " + asPublic + "] [] [] []"},
		{"Public compact and bare", `{"to":"as:Public","cc":["Public"]}`, aud, "[" + asPublic + "] [" + asPublic + "] [] [] []"},
		{"to embedded Person (Vocabulary Example 157's shape)", `{"to":{"name":"Sally","type":"Person","id":"https://sally.example.org"}}`, aud, "[https://sally.example.org] [] [] [] []"},
		{"bto, bcc, audience", `{"bto":"https://a.example/u","bcc":["https://a.example/v"],"audience":"https://a.example/g"}`, aud, "[] [] [https://a.example/u] [https://a.example/v] [https://a.example/g]"},
		{"to full IRI", `{"https://www.w3.org/ns/activitystreams#to":"https://a.example/u"}`, aud, "[https://a.example/u] [] [] [] []"},
		{"to an http address", `{"to":"http://a.example/u"}`, aud, "refused: an id that is not an https URL that may be fetched @to"},

		// Text, flags, dates, counts.
		{"content", `{"content":"<p>hi</p>"}`, text("content"), "<p>hi</p>"},
		{"content full IRI", `{"https://www.w3.org/ns/activitystreams#content":"hi"}`, text("content"), "hi"},
		{"content twice in two spellings", `{"content":"a","as:content":"b"}`, text("content"), "refused: a member named twice in one object @content"},
		{"content a number", `{"content":5}`, text("content"), "refused: a value of the wrong kind @content"},
		{"content null", `{"content":null}`, text("content"), ""},
		{"escaped pair", "{\"content\":\"\\ud83d\\ude00\"}", text("content"), "😀"},
		{"preferredUsername compact", `{"as:preferredUsername":"ana"}`, text("preferredUsername"), "ana"},
		{"publicKeyPem sec IRI", `{"https://w3id.org/security#publicKeyPem":"P"}`, text("publicKeyPem"), "P"},
		{"sensitive true", `{"sensitive":true}`, flag("sensitive"), "true"},
		{"sensitive as: IRI", `{"https://www.w3.org/ns/activitystreams#sensitive":true}`, flag("sensitive"), "true"},
		{"sensitive null", `{"sensitive":null}`, flag("sensitive"), "false"},
		{"sensitive a string", `{"sensitive":"true"}`, flag("sensitive"), "refused: a value of the wrong kind @sensitive"},
		{"suspended toot IRI", `{"http://joinmastodon.org/ns#suspended":true}`, flag("suspended"), "true"},
		{"suspended toot compact", `{"toot:suspended":true}`, flag("suspended"), "true"},
		{"date", `{"published":"2015-02-10T15:04:55Z"}`, date, "2015-02-10T15:04:55Z"},
		{"date with a fraction", `{"published":"2015-02-10T15:04:55.25Z"}`, date, "2015-02-10T15:04:55Z"},
		{"date without seconds (AS2 Core 2.3)", `{"published":"2015-02-10T15:04Z"}`, date, "2015-02-10T15:04:00Z"},
		{"date with an offset", `{"published":"2015-02-10T17:04:55+02:00"}`, date, "2015-02-10T15:04:55Z"},
		{"date lower case t", `{"published":"2015-02-10t15:04:55Z"}`, date, "refused: a date that is not an RFC 3339 date-time @published"},
		{"date lower case z", `{"published":"2015-02-10T15:04:55z"}`, date, "refused: a date that is not an RFC 3339 date-time @published"},
		{"date without a zone", `{"published":"2015-02-10T15:04:55"}`, date, "refused: a date that is not an RFC 3339 date-time @published"},
		{"date a number", `{"published":1423580695}`, date, "refused: a value of the wrong kind @published"},
		{"totalItems", `{"totalItems":3}`, func(f apFields) (any, error) { v, _, err := f.count("totalItems"); return v, err }, "3"},
		{"totalItems a string", `{"totalItems":"3"}`, func(f apFields) (any, error) { v, _, err := f.count("totalItems"); return v, err }, "refused: a count that is not a whole number @totalItems"},
		{"totalItems a fraction", `{"totalItems":3.0}`, func(f apFields) (any, error) { v, _, err := f.count("totalItems"); return v, err }, "refused: a count that is not a whole number @totalItems"},
		{"totalItems negative", `{"totalItems":-1}`, func(f apFields) (any, error) { v, _, err := f.count("totalItems"); return v, err }, "refused: a count that is not a whole number @totalItems"},

		// url and icon: a URL, a Link, an Image with a url, lists of these (AS2 Core 4.2).
		{"url string", `{"url":"https://a.example/p"}`, links("url"), "[{https://a.example/p }]"},
		{"url Link (Vocabulary Example 50's shape)", `{"url":{"type":"Link","href":"https://a.example/p.mp3","mediaType":"audio/mp3"}}`, links("url"), "[{https://a.example/p.mp3 audio/mp3}]"},
		{"url list of both (AS2 Core Example 13's shape)", `{"url":["https://a.example/abc.gif",{"type":"Link","href":"https://a.example/123.png","mediaType":"image/png"}]}`, links("url"), "[{https://a.example/abc.gif } {https://a.example/123.png image/png}]"},
		{"url http left out", `{"url":["http://a.example/p","https://a.example/q"]}`, links("url"), "[{https://a.example/q }]"},
		{"icon list of one (ActivityPub Example 9)", file("ap-ex9-actor.json"), links("icon"), "[{https://kenzoishii.example.com/image/165987aklre4 }]"},
		{"icon Image (federation.md 5.5)", file("flowa-5.5-actor.json"), links("icon"), "[{https://flowa.example.org/users/ana/avatar.jpg?v=3f9a1c image/jpeg}]"},
		{"icon Images (Vocabulary Example 80's shape)", `{"icon":[{"type":"Image","url":"https://a.example/1.png","width":16},{"type":"Image","url":"https://a.example/2.png"}]}`, links("icon"), "[{https://a.example/1.png } {https://a.example/2.png }]"},
		{"icon Image with a Link url", `{"icon":{"type":"Image","mediaType":"image/png","url":{"type":"Link","href":"https://a.example/i.png"}}}`, links("icon"), "[{https://a.example/i.png image/png}]"},
		{"icon Image too deep", `{"icon":{"type":"Image","url":{"type":"Image","url":{"type":"Image","url":"https://a.example/i.png"}}}}`, links("icon"), "[]"},
		{"icon number url", `{"icon":{"type":"Image","url":5}}`, links("icon"), "refused: a value of the wrong kind @icon.url"},

		// publicKey: one, many, by id; the security vocabulary's IRIs.
		{"publicKey one (Mastodon)", `{"publicKey":{"id":"https://a.example/u#main-key","owner":"https://a.example/u","publicKeyPem":"P"}}`, keys, `[https://a.example/u#main-key owner=https://a.example/u pem="P" byid=false]`},
		{"publicKey by id", `{"publicKey":"https://a.example/u#main-key"}`, keys, `[https://a.example/u#main-key owner= pem="" byid=true]`},
		{"publicKey list", `{"publicKey":["https://a.example/k1",{"id":"https://a.example/u#k2","controller":"https://a.example/u","publicKeyPem":"Q"}]}`, keys, `[https://a.example/k1 owner= pem="" byid=true https://a.example/u#k2 owner=https://a.example/u pem="Q" byid=false]`},
		{"publicKey sec IRIs", `{"https://w3id.org/security#publicKey":{"id":"https://a.example/u#k","https://w3id.org/security#owner":"https://a.example/u","sec:publicKeyPem":"R"}}`, keys, `[https://a.example/u#k owner=https://a.example/u pem="R" byid=false]`},
		{"publicKey twice with one id", `{"publicKey":[{"id":"https://a.example/u#k","publicKeyPem":"A"},{"id":"https://a.example/u#k","publicKeyPem":"B"}]}`, keys, "refused: two public keys with the same id @publicKey"},
		{"publicKey without id", `{"publicKey":{"publicKeyPem":"A"}}`, keys, "refused: an object without an id @publicKey"},
		{"endpoints", `{"endpoints":{"sharedInbox":"https://a.example/inbox"}}`, func(f apFields) (any, error) {
			r, err := f.ref("endpoints")
			if err != nil {
				return nil, err
			}
			return r.Obj.id("sharedInbox")
		}, "https://a.example/inbox"},

		// A Note's tag and attachment, as 6.5 and 6.6 read them.
		{"tag one object, Hashtag (Mastodon)", file("mastodon-hashtag.json"), note(func(n *apNote) any { return fmt.Sprint(n.Hashtags, n.Mentions) }), "[{#cats https://example.com/tagged/cats}] []"},
		{"tag list, Mention and Hashtag (federation.md 6.5)", `{` + n + `"tag":[{"type":"Mention","href":"https://s.example/users/bo","name":"@bo@s.example"},{"type":"as:Hashtag","href":"https://b.example/t/poi","name":"#poi"},"https://b.example/t/x",{"type":"Emoji","name":":k:"},{"name":"#untyped"}]}`, note(func(n *apNote) any { return fmt.Sprint(n.Mentions, n.Hashtags) }), "[{https://s.example/users/bo @bo@s.example}] [{#poi https://b.example/t/poi}]"},
		{"tag one Mention, not a list", `{` + n + `"tag":{"type":"Mention","href":"https://s.example/users/bo","name":"@bo"}}`, note(func(n *apNote) any { return n.Mentions }), "[{https://s.example/users/bo @bo}]"},
		{"tag Mention with an http href", `{` + n + `"tag":[{"type":"Mention","href":"http://s.example/users/bo"}]}`, note(func(n *apNote) any { return n.Mentions }), "refused: an id that is not an https URL that may be fetched @tag.href"},
		{"tag Hashtag with an http href", `{` + n + `"tag":{"type":"Hashtag","href":"http://b.example/t/poi","name":"#poi"}}`, note(func(n *apNote) any { return n.Hashtags }), "[{#poi }]"},
		{"attachment one object, alt in summary", `{` + n + `"attachment":{"type":"Image","mediaType":"image/png","url":"https://b.example/1.png","summary":"a cat","name":"cat.png"}}`, note(func(n *apNote) any {
			return fmt.Sprintf("%v %v %v %v", n.Attachments[0].Kind, n.Attachments[0].MediaType, n.Attachments[0].URL, n.Attachments[0].alt())
		}), "Image image/png [{https://b.example/1.png }] a cat"},
		{"attachment, alt in name", `{` + n + `"attachment":[{"type":"Document","url":{"type":"Link","href":"https://b.example/1.jpg","mediaType":"image/jpeg"},"name":"a dog"}]}`, note(func(n *apNote) any {
			return fmt.Sprintf("%v %v", n.Attachments[0].URL, n.Attachments[0].alt())
		}), "[{https://b.example/1.jpg image/jpeg}] a dog"},
		{"attachment focalPoint skipped (Mastodon)", file("mastodon-focal-point.json"), note(func(n *apNote) any { return n.Attachments }), "[{Image image/png [{https://example.com/files/cats.png }]  }]"},
		{"contentMap (Vocabulary Example 115's shape)", `{` + n + `"contentMap":{"en":"A <em>simple</em> note","zh-Hans":"一段<em>简单的</em>笔记"}}`, note(func(n *apNote) any { return n.ContentMap }), "map[en:A <em>simple</em> note zh-Hans:一段<em>简单的</em>笔记]"},
		{"contentMap a list", `{` + n + `"contentMap":["a"]}`, note(func(n *apNote) any { return n.ContentMap }), "refused: a value of the wrong kind @contentMap"},

		// @context in every shape, never read.
		{"@context string (AS2 Core Example 1)", file("as-core-ex1-context-string.json"), text("content"), "My dog has fleas."},
		{"@context object (AS2 Core Example 2)", file("as-core-ex2-context-object.json"), text("content"), "My dog has fleas."},
		{"@context list (AS2 Core Example 3)", file("as-core-ex3-context-array.json"), text("content"), "My dog has fleas."},
		{"@context list of both (Mastodon Hashtags)", file("mastodon-hashtag.json"), text("content"), "I love #cats"},
		{"@context in an embedded object (ActivityPub Example 7)", file("ap-ex7-like-embedded.json"), ref("object"), "[{https://example.org/~alice/note/23 Note}]"},
		{"@context a number", `{"@context":5,"content":"x"}`, text("content"), "x"},
		{"@id inside @context and extensions (AS2 Core Appendix C's shape)", `{"@context":{"x":{"@id":"ex:x","@type":"@id"}},"dcterms:creator":{"@id":"https://a.example/e"},"content":"x"}`, text("content"), "x"},
		{"@id at the root", `{"@id":"https://a.example/1","content":"x"}`, text("content"), "refused: @id or @type in place of id or type @"},
		{"whitespace around the document", " \r\n\t{\"content\":\"x\"}\n ", text("content"), "x"},
		{"unknown nested lists skipped", `{"coordinates":[[1,2],[3,[4]]],"content":"x"}`, text("content"), "x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := apParse([]byte(c.doc), apMax)
			got := apOut(f, err)
			if err == nil {
				got = apOut(c.read(f))
			}
			if got != c.want {
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

// TestDecodeFixtures reads every file in testdata/federation/shapes as its README says, and fails
// if a file is not in the table.
func TestDecodeFixtures(t *testing.T) {
	actor := func(b []byte) error { _, err := decodeActor(b, apMax); return err }
	activity := func(b []byte) error {
		a, err := decodeActivity(b, apMax)
		if err == nil && len(a.Object) == 1 && a.Object[0].Obj != nil && a.Object[0].Kind == "Note" {
			_, err = a.Object[0].note()
		}
		return err
	}
	note := func(b []byte) error { _, err := decodeNote(b, apMax); return err }
	tomb := func(b []byte) error { _, err := decodeTombstone(b, apMax); return err }
	coll := func(b []byte) error { _, err := decodeCollection(b, apMax); return err }
	cases := map[string]struct {
		read func([]byte) error
		want apReason
	}{
		"ap-ex1-actor.json":                 {actor, 0},
		"ap-ex3-create.json":                {activity, 0},
		"ap-ex4-create-reply.json":          {activity, apMalformed},
		"ap-ex5-like.json":                  {activity, 0},
		"ap-ex6-create-public.json":         {activity, 0},
		"ap-ex7-like-embedded.json":         {activity, apNoID}, // a Like with no id of its own
		"ap-ex9-actor.json":                 {actor, 0},
		"ap-ex15-note-no-id.json":           {note, apNoID},
		"ap-ex16-create.json":               {activity, 0},
		"ap-ex17-tombstone.json":            {tomb, 0},
		"mastodon-block.json":               {activity, 0},
		"mastodon-flag.json":                {activity, 0},
		"mastodon-move.json":                {activity, 0},
		"mastodon-question.json":            {note, 0},
		"mastodon-vote.json":                {activity, 0},
		"mastodon-public-key.json":          {actor, apNoInbox},
		"mastodon-suspended.json":           {actor, 0},
		"mastodon-memorial.json":            {actor, apNoInbox},
		"mastodon-hashtag.json":             {note, 0},
		"mastodon-emoji.json":               {note, apMalformed},
		"mastodon-focal-point.json":         {note, 0},
		"as-core-ex1-context-string.json":   {note, apNoID},
		"as-core-ex2-context-object.json":   {note, apNoID},
		"as-core-ex3-context-array.json":    {note, apNoID},
		"as-core-ex16-like-http.json":       {activity, apBadID},
		"as-core-ex20-collection-http.json": {coll, apBadID},
		"flowa-5.5-actor.json":              {actor, 0},
		"flowa-6.5-create.json":             {activity, 0},
	}
	files, err := filepath.Glob("testdata/federation/shapes/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		name := filepath.Base(p)
		c, ok := cases[name]
		if !ok {
			t.Errorf("%s: not in the table", name)
			continue
		}
		delete(cases, name)
		if got := apReasonOf(c.read(shapeDoc(t, name))); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	for name := range cases {
		t.Errorf("%s: in the table, not on disk", name)
	}
}

func TestDecodeActor(t *testing.T) {
	a, err := decodeActor(shapeDoc(t, "flowa-5.5-actor.json"), apMax)
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%s %s %v %s|%s|%s %v %v %s %s %s %v %v %q %v", a.ID, a.Kind, a.Types, a.PreferredUsername, a.Name, a.Summary,
		a.URL, a.Icon, a.Inbox, a.SharedInbox, a.Followers, a.ManuallyApprovesFollowers, a.Suspended, a.MovedTo, a.AlsoKnownAs)
	want := `https://flowa.example.org/users/ana Person [Person] ana|Ana|<p>Poi and staff, mostly in the park.</p> [{https://flowa.example.org/users/ana }] [{https://flowa.example.org/users/ana/avatar.jpg?v=3f9a1c image/jpeg}] https://flowa.example.org/users/ana/inbox https://flowa.example.org/inbox https://flowa.example.org/users/ana/followers false false "" []`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	k, err := a.key("https://flowa.example.org/users/ana#main-key")
	if err != nil || k.Owner != a.ID || !strings.HasPrefix(k.PEM, "-----BEGIN PUBLIC KEY-----\n") {
		t.Errorf("key: %+v %v", k, err)
	}

	// The key the key id names, not the first, and the PEM handed on exactly as the JSON string
	// holds it, which parsePublicKeyPEM then reads.
	priv, other := sigTestKeys()
	spki := func(pub any) string {
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	}
	pemA, pemB := spki(&other.PublicKey), spki(&priv.PublicKey)
	doc, _ := json.Marshal(map[string]any{
		"@context": []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id":       "https://r.example/users/bo", "type": []string{"Person", "toot:Thing"},
		"inbox": "https://r.example/users/bo/inbox",
		"publicKey": []any{
			map[string]any{"id": "https://r.example/users/bo#old", "owner": "https://r.example/users/bo", "publicKeyPem": pemA},
			map[string]any{"id": "https://r.example/users/bo#main-key", "owner": "https://r.example/users/bo", "publicKeyPem": pemB},
			"https://r.example/keys/3",
		},
		"manuallyApprovesFollowers": true, "movedTo": "https://n.example/users/bo",
		"alsoKnownAs": "https://o.example/users/bo",
	})
	b, err := decodeActor(doc, apMax)
	if err != nil {
		t.Fatal(err)
	}
	k, err = b.key("https://r.example/users/bo#main-key")
	if err != nil || k.PEM != pemB {
		t.Fatalf("key by id: %+v %v", k, err)
	}
	pub, err := parsePublicKeyPEM(k.PEM, sigKeyMin)
	if err != nil || !pub.Equal(&priv.PublicKey) {
		t.Errorf("the PEM did not come through: %v", err)
	}
	if k, err := b.key("https://r.example/keys/3"); err != nil || !k.ByID || k.PEM != "" {
		t.Errorf("a key by id: %+v %v", k, err)
	}
	for _, id := range []string{"https://r.example/users/bo", "https://r.example/users/bo#MAIN-KEY", "https://r.example/users/bo#main-key ", ""} {
		if _, err := b.key(id); apReasonOf(err) != apNoKey {
			t.Errorf("key %q: %v", id, err)
		}
	}
	if !b.ManuallyApprovesFollowers || b.MovedTo != "https://n.example/users/bo" || !slices.Equal(b.AlsoKnownAs, []string{"https://o.example/users/bo"}) {
		t.Errorf("flags and moves: %+v", b)
	}

	// Mastodon's own example of a key: the PEM read alone comes through byte for byte.
	f, err := apParse(shapeDoc(t, "mastodon-public-key.json"), apMax)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := f.keys()
	if err != nil || len(ks) != 1 || ks[0].ID != "https://mastodon.social/users/Gargron#main-key" {
		t.Fatalf("Mastodon's key: %+v %v", ks, err)
	}
	var raw struct {
		PublicKey struct {
			PEM string `json:"publicKeyPem"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(shapeDoc(t, "mastodon-public-key.json"), &raw); err != nil || raw.PublicKey.PEM != ks[0].PEM {
		t.Errorf("the PEM changed on the way: %v", err)
	}
	if _, err := parsePublicKeyPEM(ks[0].PEM, sigKeyMin); err != nil {
		t.Errorf("Mastodon's example key: %v", err)
	}

	// Mastodon's suspended account: empty data, no inbox, read to be marked gone.
	s, err := decodeActor(shapeDoc(t, "mastodon-suspended.json"), apMax)
	if err != nil || !s.Suspended || s.Inbox != "" {
		t.Errorf("suspended: %+v %v", s, err)
	}

	// The five actor types and nothing else (Activity Vocabulary 3.2).
	for typ, want := range map[string]apReason{
		`"Person"`: 0, `"Service"`: 0, `"Application"`: 0, `"Group"`: 0, `"Organization"`: 0,
		`"as:Service"`: 0, `["Person","https://example.org/ns#Bot"]`: 0,
		`"Note"`: apWrongKind, `"Bot"`: apWrongKind, `["Person","Application"]`: apTypeConflict,
	} {
		doc := `{"id":"https://r.example/u","inbox":"https://r.example/u/inbox","type":` + typ + `}`
		if _, err := decodeActor([]byte(doc), apMax); apReasonOf(err) != want {
			t.Errorf("type %s: %v, want %q", typ, err, want)
		}
	}
	// endpoints given as a link to another document: no shared inbox, not refused.
	e, err := decodeActor([]byte(`{"id":"https://r.example/u","type":"Person","inbox":"https://r.example/u/inbox","endpoints":"https://r.example/u/endpoints"}`), apMax)
	if err != nil || e.SharedInbox != "" {
		t.Errorf("endpoints by link: %+v %v", e, err)
	}
	// An actor embedded in an activity is read the same way.
	act, err := decodeActivity([]byte(`{"id":"https://r.example/a/1","type":"Update","actor":"https://r.example/u","object":{"id":"https://r.example/u","type":"Person","inbox":"https://r.example/u/inbox"}}`), apMax)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := act.Object[0].actor(); err != nil || u.ID != act.Actor {
		t.Errorf("embedded actor: %+v %v", u, err)
	}
}

func TestDecodeNote(t *testing.T) {
	a, err := decodeActivity(shapeDoc(t, "flowa-6.5-create.json"), apMax)
	if err != nil {
		t.Fatal(err)
	}
	n, err := a.Object[0].note()
	if err != nil {
		t.Fatal(err)
	}
	if !n.isNote() || !n.by(a.Actor) || n.by("https://flowa.example.org/users/bo") || n.Sensitive || n.Cut {
		t.Errorf("kind, author, flags: %+v", n)
	}
	got := fmt.Sprint(n.ID, " ", n.URL, " ", n.Mentions, " ", n.Hashtags, " ", n.Attachments, " ", n.Audience.public(), " ", n.Published.Format(time.RFC3339))
	want := `https://flowa.example.org/users/ana/posts/812 [{https://flowa.example.org/users/ana/posts/812 }] [{https://social.example/users/bo @bo@social.example}] [{#nunchaku https://flowa.example.org/#/prop/nunchaku}] [{Image image/jpeg [{https://flowa.example.org/users/ana/posts/812/picture.jpg }]  }] true 2026-11-12T18:04:05Z`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if !strings.HasPrefix(n.Content, "<p>Finally clean! Thanks <span class=\"h-card\">") {
		t.Errorf("content: %q", n.Content)
	}

	// Mastodon's poll is read; its kind is Question, which 6.6 keeps as text.
	q, err := decodeNote(shapeDoc(t, "mastodon-question.json"), apMax)
	if err != nil || q.isNote() || q.Kind != "Question" || q.Content != "What should I eat for breakfast today?" {
		t.Errorf("Question: %+v %v", q, err)
	}
	// A vote is a Note with only a name, in reply to the poll, ids with fragments.
	v, err := decodeActivity(shapeDoc(t, "mastodon-vote.json"), apMax)
	if err != nil {
		t.Fatal(err)
	}
	vn, err := v.Object[0].note()
	if err != nil || vn.Name != "orange" || vn.InReplyTo != "https://mastodon.example/users/alice/statuses/1009947848598745" || !vn.by(v.Actor) {
		t.Errorf("vote: %+v %v", vn, err)
	}

	// Several authors cannot all be the signer.
	two, err := decodeNote([]byte(`{"id":"https://p.example/v/1","type":"Video","attributedTo":[{"type":"Person","id":"https://p.example/a/u"},{"type":"Group","id":"https://p.example/c/g"}]}`), apMax)
	if err != nil || two.by("https://p.example/a/u") || two.Kind != "Video" {
		t.Errorf("two authors: %+v %v", two, err)
	}
	// attributedTo compared as written: another spelling of the same URL is another id.
	if one, _ := decodeNote([]byte(`{"id":"https://p.example/n/1","type":"Note","attributedTo":"https://P.example/u"}`), apMax); one.by("https://p.example/u") {
		t.Error("attributedTo matched in another spelling")
	}

	// content is cut at 64 KB on a character boundary, contentMap entries too, and only them.
	long := strings.Repeat("é", apTextMax) // two bytes each
	doc, _ := json.Marshal(map[string]any{"id": "https://p.example/n/2", "type": "Note", "content": "x" + long, "contentMap": map[string]string{"fr": long}, "summary": "cw", "sensitive": true})
	c, err := decodeNote(doc, apMax)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Cut || len(c.Content) > apTextMax || len(c.Content) < apTextMax-3 || !utf8.ValidString(c.Content) || !utf8.ValidString(c.ContentMap["fr"]) || len(c.ContentMap["fr"]) != apTextMax {
		t.Errorf("cut: %d %d %v", len(c.Content), len(c.ContentMap["fr"]), c.Cut)
	}
	if c.Summary != "cw" || !c.Sensitive {
		t.Errorf("summary and sensitive: %q %v", c.Summary, c.Sensitive)
	}
	doc, _ = json.Marshal(map[string]any{"id": "https://p.example/n/3", "type": "Note", "summary": long})
	if _, err := decodeNote(doc, apMax); apReasonOf(err) != apTooLong {
		t.Errorf("a summary over 64 KB: %v", err)
	}
}

func TestDecodeActivity(t *testing.T) {
	read := func(name string) *apActivity {
		t.Helper()
		a, err := decodeActivity(shapeDoc(t, name), apMax)
		if err != nil {
			t.Fatal(name, err)
		}
		return a
	}
	flag := read("mastodon-flag.json")
	if flag.Kind != "Flag" || len(flag.Object) != 3 || flag.Object[2].ID != "https://example.com/posts/380591" || flag.Actor != "https://mastodon.example/actor" {
		t.Errorf("Flag: %+v", flag)
	}
	if _, err := flag.Object[0].note(); apReasonOf(err) != apByID {
		t.Errorf("an object by id read as embedded: %v", err)
	}
	move := read("mastodon-move.json")
	if move.Kind != "Move" || move.Target.ID != "https://alice.com/users/109835986274379" || move.Object[0].ID != move.Actor {
		t.Errorf("Move: %+v", move)
	}

	// Origins (federation.md 7.3): ActivityPub Example 16's Note is on example.com and its actor on
	// example.net; Example 3's are on one origin.
	ex16 := read("ap-ex16-create.json")
	if sameOrigin(ex16.Object[0].ID, ex16.Actor) {
		t.Error("Example 16: the Note's id taken for the actor's origin")
	}
	ex3 := read("ap-ex3-create.json")
	if !sameOrigin(ex3.Object[0].ID, ex3.Actor) || !sameOrigin(ex3.ID, ex3.Actor) {
		t.Error("Example 3: one origin not seen as one")
	}
	if !read("ap-ex6-create-public.json").Audience.public() || ex3.Audience.public() {
		t.Error("public addressing")
	}
	if !ex16.Published.Equal(time.Date(2015, 2, 10, 15, 4, 55, 0, time.UTC)) {
		t.Errorf("published %v", ex16.Published)
	}

	// Undo with the Follow embedded, Delete with a Tombstone embedded, an extension type.
	undo, err := decodeActivity([]byte(`{"@context":"https://www.w3.org/ns/activitystreams","id":"https://r.example/u#undo/1","type":"Undo","actor":"https://r.example/u","object":{"id":"https://r.example/u#follow/1","type":"Follow","actor":"https://r.example/u","object":"https://b.example/users/ana"}}`), apMax)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := undo.Object[0].activity()
	if err != nil || inner.Kind != "Follow" || inner.Actor != undo.Actor || inner.Object[0].ID != "https://b.example/users/ana" {
		t.Errorf("Undo(Follow): %+v %v", inner, err)
	}
	del, err := decodeActivity([]byte(`{"id":"https://r.example/n/1#delete","type":"Delete","actor":"https://r.example/u","object":{"id":"https://r.example/n/1","type":"Tombstone","formerType":"as:Note","deleted":"2026-11-13T10:00:00Z"},"updated":"2026-11-13T10:00:00Z"}`), apMax)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := del.Object[0].tombstone()
	if err != nil || ts.ID != "https://r.example/n/1" || !slices.Equal(ts.FormerType, []string{"Note"}) || ts.Deleted.IsZero() || del.Updated.IsZero() {
		t.Errorf("Delete(Tombstone): %+v %v", ts, err)
	}
	if _, err := del.Object[0].note(); err != nil {
		t.Errorf("a Tombstone read as a post is kind Tombstone, not refused: %v", err)
	}
	if _, err := undo.Object[0].tombstone(); apReasonOf(err) != apWrongKind {
		t.Errorf("a Follow read as a Tombstone: %v", err)
	}
	tomb, err := decodeTombstone(shapeDoc(t, "ap-ex17-tombstone.json"), apMax)
	if err != nil || tomb.ID != "https://example.com/~alice/note/72" || tomb.Deleted.IsZero() {
		t.Errorf("ActivityPub Example 17: %+v %v", tomb, err)
	}
	react, err := decodeActivity([]byte(`{"id":"https://m.example/r/1","type":"EmojiReact","actor":"https://m.example/u","object":"https://b.example/p/1","content":"🍣"}`), apMax)
	if err != nil || react.Kind != "" || !slices.Equal(react.Types, []string{"EmojiReact"}) {
		t.Errorf("an extension type: %+v %v", react, err)
	}

	// Recipients: 1,000 in all is read, 1,001 is refused, whatever lists they are in.
	addr := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "https://r.example/u/" + strconv.Itoa(i)
		}
		return out
	}
	for _, c := range []struct {
		to, cc int
		want   apReason
	}{{1000, 0, 0}, {500, 500, 0}, {501, 500, apTooManyRecipients}, {1001, 0, apTooMany}} {
		doc, _ := json.Marshal(map[string]any{"id": "https://r.example/a/1", "type": "Create", "actor": "https://r.example/u", "to": addr(c.to), "cc": addr(c.cc)})
		if _, err := decodeActivity(doc, apMax); apReasonOf(err) != c.want {
			t.Errorf("%d and %d recipients: %v", c.to, c.cc, err)
		}
	}
}

func TestDecodeCollection(t *testing.T) {
	// An outbox as Mastodon serves it: the collection, then its pages (ActivityPub 5.1; AS2 Core
	// 4.6.1 Example 20's shape, with https ids).
	c, err := decodeCollection([]byte(`{"@context":"https://www.w3.org/ns/activitystreams","id":"https://m.example/users/a/outbox","type":"OrderedCollection","totalItems":7,"first":"https://m.example/users/a/outbox?page=true","last":"https://m.example/users/a/outbox?min_id=0&page=true"}`), apMax)
	if err != nil || c.Kind != "OrderedCollection" || c.TotalItems != 7 || !c.HasTotal || c.First.ID != "https://m.example/users/a/outbox?page=true" || len(c.Items) != 0 {
		t.Errorf("outbox: %+v %v", c, err)
	}
	p, err := decodeCollection([]byte(`{"id":"https://m.example/users/a/outbox?page=true","type":"OrderedCollectionPage","next":"https://m.example/users/a/outbox?max_id=1&page=true","partOf":"https://m.example/users/a/outbox","orderedItems":[{"id":"https://m.example/users/a/statuses/2/activity","type":"Create","actor":"https://m.example/users/a","object":{"id":"https://m.example/users/a/statuses/2","type":"Note","attributedTo":"https://m.example/users/a","content":"hi"}},"https://m.example/users/a/statuses/1/activity"]}`), apMax)
	if err != nil || p.Kind != "OrderedCollectionPage" || p.HasTotal || p.Next.ID == "" || len(p.Items) != 2 || p.Items[1].Obj != nil {
		t.Fatalf("page: %+v %v", p, err)
	}
	act, err := p.Items[0].activity()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := act.Object[0].note(); err != nil || n.Content != "hi" {
		t.Errorf("an item's Note: %+v %v", n, err)
	}
	// A post's replies with the first page embedded, unordered items.
	r, err := decodeCollection([]byte(`{"id":"https://m.example/s/1/replies","type":"Collection","first":{"type":"CollectionPage","next":"https://m.example/s/1/replies?page=2","partOf":"https://m.example/s/1/replies","items":["https://m.example/s/2"]}}`), apMax)
	if err != nil || r.First.Obj == nil {
		t.Fatalf("replies: %+v %v", r, err)
	}
	first, err := r.First.collection()
	if err != nil || first.Kind != "CollectionPage" || first.ID != "" || len(first.Items) != 1 || first.Next.ID != "https://m.example/s/1/replies?page=2" {
		t.Errorf("an embedded first page: %+v %v", first, err)
	}
	for doc, want := range map[string]apReason{
		`{"type":"OrderedCollection","items":["https://a.example/1"],"orderedItems":["https://a.example/1"]}`: apDuplicate,
		`{"type":"OrderedCollection","orderedItems":[{"type":"Note","content":"no id"}]}`:                     apNoID,
		`{"type":"OrderedCollection","orderedItems":[["https://a.example/1"]]}`:                               apNested,
		`{"type":"OrderedCollection","totalItems":1e3}`:                                                       apBadCount,
		`{"type":"OrderedCollection","first":["https://a.example/1","https://a.example/2"]}`:                  apMoreThanOne,
		`{"type":"OrderedCollection","next":"http://a.example/2"}`:                                            apBadID,
		`{"type":"Note","id":"https://a.example/1"}`:                                                          apWrongKind,
		`{"type":["OrderedCollectionPage","OrderedCollection"]}`:                                              apTypeConflict,
	} {
		if _, err := decodeCollection([]byte(doc), apMax); apReasonOf(err) != want {
			t.Errorf("%s: %v, want %q", doc, err, want)
		}
	}
}

// TestDecodeRefusals holds each limit of federation.md 7.5 the decoder enforces and each malformed
// shape, with its reason. Every refusal's text is one of the fixed phrases.
func TestDecodeRefusals(t *testing.T) {
	nest := func(n int) string { return `{"x":` + strings.Repeat("[", n-1) + strings.Repeat("]", n-1) + `}` }
	list := func(n int) string { return `{"x":[` + strings.TrimSuffix(strings.Repeat("0,", n), ",") + `]}` }
	members := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"x":{`)
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`"k` + strconv.Itoa(i) + `":0`)
		}
		b.WriteString(`}}`)
		return b.String()
	}
	note := `"id":"https://a.example/n/1","type":"Note"`
	cases := []struct {
		name, doc string
		max       int
		want      apReason
		field     string
	}{
		{"over the caller's size", `{"id":"https://a.example/n/1","type":"Note"}`, 10, apTooLarge, ""},
		{"empty", ``, apMax, apNotObject, ""},
		{"a list", `[]`, apMax, apNotObject, ""},
		{"a string", `"x"`, apMax, apNotObject, ""},
		{"a byte order mark", "\xef\xbb\xbf{}", apMax, apNotObject, ""},
		{"unclosed", `{"a":1`, apMax, apMalformed, ""},
		{"trailing comma", `{"a":1,}`, apMax, apMalformed, ""},
		{"trailing data", `{} {}`, apMax, apMalformed, ""},
		{"single quotes", `{'a':1}`, apMax, apMalformed, ""},
		{"a raw line break in a string", "{\"a\":\"x\ny\"}", apMax, apMalformed, ""},
		{"an unknown escape", `{"a":"\x"}`, apMax, apMalformed, ""},
		{"a bad number", `{"a":01}`, apMax, apMalformed, ""},
		{"a bare word", `{"a":nope}`, apMax, apMalformed, ""},
		{"invalid UTF-8", "{\"a\":\"\xff\"}", apMax, apBadText, ""},
		{"invalid UTF-8 in a name", "{\"\xc3\":1}", apMax, apBadText, ""},
		{"an escaped high surrogate alone", `{"a":"\ud800"}`, apMax, apBadText, ""},
		{"an escaped low surrogate alone", `{"a":"\udc00x"}`, apMax, apBadText, ""},
		{"a high surrogate before a letter", `{"a":"\ud800A"}`, apMax, apBadText, ""},
		{"24 levels", nest(24), apMax, apNoType, "type"},
		{"25 levels", nest(25), apMax, apTooDeep, ""},
		{"1,000 entries", list(1000), apMax, apNoType, "type"},
		{"1,001 entries", list(1001), apMax, apTooMany, ""},
		{"1,000 members", members(1000), apMax, apNoType, "type"},
		{"1,001 members", members(1001), apMax, apTooMany, ""},
		{"id twice", `{"id":"https://a.example/1","id":"https://a.example/2","type":"Note"}`, apMax, apDuplicate, ""},
		{"id twice, once escaped", "{\"id\":\"https://a.example/1\",\"\\u0069d\":\"https://a.example/2\",\"type\":\"Note\"}", apMax, apDuplicate, ""},
		{"a high surrogate before an escaped letter", "{\"a\":\"\\ud800\\u0041\"}", apMax, apBadText, ""},
		{"actor twice", `{"actor":"https://a.example/u","actor":"https://e.example/u"}`, apMax, apDuplicate, ""},
		{"a name twice in an extension", `{"x":{"a":1,"a":2}}`, apMax, apDuplicate, ""},
		{"a name twice in @context", `{"@context":{"a":"x","a":"y"}}`, apMax, apDuplicate, ""},
		{"actor in two spellings", `{` + note + `,"attributedTo":"https://a.example/u","as:attributedTo":"https://e.example/u"}`, apMax, apDuplicate, "attributedTo"},
		{"@id for id", `{"@id":"https://a.example/n/1","type":"Note"}`, apMax, apKeyword, ""},
		{"@type for type", `{"id":"https://a.example/n/1","@type":"Note"}`, apMax, apKeyword, ""},
		{"an id that is a number", `{"id":12,"type":"Note"}`, apMax, apWrongShape, "id"},
		{"content a number", `{` + note + `,"content":12}`, apMax, apWrongShape, "content"},
		{"content an object", `{` + note + `,"content":{"@value":"x"}}`, apMax, apWrongShape, "content"},
		{"name a list", `{` + note + `,"name":["x"]}`, apMax, apWrongShape, "name"},
		{"sensitive a number", `{` + note + `,"sensitive":1}`, apMax, apWrongShape, "sensitive"},
		{"to a number", `{` + note + `,"to":5}`, apMax, apWrongShape, "to"},
		{"to nested", `{` + note + `,"to":[["https://a.example/u"]]}`, apMax, apNested, "to"},
		{"an id over 2,048 bytes", `{"id":"https://a.example/` + strings.Repeat("a", 2048) + `","type":"Note"}`, apMax, apTooLong, "id"},
		{"a name over 64 KB", `{` + note + `,"name":"` + strings.Repeat("a", apTextMax+1) + `"}`, apMax, apTooLong, "name"},
		{"a media type over 255 bytes", `{` + note + `,"attachment":{"type":"Image","mediaType":"` + strings.Repeat("a", 256) + `"}}`, apMax, apTooLong, "attachment.mediaType"},
		{"an http id", `{"id":"http://a.example/n/1","type":"Note"}`, apMax, apBadID, "id"},
		{"a user name in the id", `{"id":"https://u@a.example/n/1","type":"Note"}`, apMax, apBadID, "id"},
		{"another port", `{"id":"https://a.example:8443/n/1","type":"Note"}`, apMax, apBadID, "id"},
		{"an IP address", `{"id":"https://203.0.113.5/n/1","type":"Note"}`, apMax, apBadID, "id"},
		{"localhost", `{"id":"https://localhost/n/1","type":"Note"}`, apMax, apBadID, "id"},
		{"a name with no dot", `{"id":"https://raspi/n/1","type":"Note"}`, apMax, apBadID, "id"},
		{"a relative id", `{"id":"/n/1","type":"Note"}`, apMax, apBadID, "id"},
		{"a space in the id", `{"id":"https://a.example/n 1","type":"Note"}`, apMax, apBadID, "id"},
		{"no id", `{"type":"Note"}`, apMax, apNoID, "id"},
		{"an attributedTo without id", `{` + note + `,"attributedTo":{"type":"Person"}}`, apMax, apNoID, "attributedTo"},
		{"no type", `{"id":"https://a.example/n/1"}`, apMax, apNoType, "type"},
		{"two kinds", `{"id":"https://a.example/n/1","type":["Note","Article"]}`, apMax, apTypeConflict, "type"},
		{"two inReplyTo", `{` + note + `,"inReplyTo":["https://a.example/n/0","https://e.example/n/0"]}`, apMax, apMoreThanOne, "inReplyTo"},
		{"a bad date", `{` + note + `,"updated":"yesterday"}`, apMax, apBadDate, "updated"},
		{"a Mention without href", `{` + note + `,"tag":{"type":"Mention","name":"@x"}}`, apMax, apNoID, "tag.href"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeNote([]byte(c.doc), c.max)
			var e *apError
			if !errors.As(err, &e) || e.Reason != c.want || e.Field != c.field {
				t.Fatalf("got %v, want %q at %q", err, c.want, c.field)
			}
			if e.Reason.String() == "refused" || strings.Contains(err.Error(), "a.example") {
				t.Errorf("the reason is not a fixed phrase: %q", err)
			}
		})
	}
	// Refusals only the actor and activity readers make.
	for doc, want := range map[string]apReason{
		`{"id":"https://a.example/u","type":"Person"}`:                                                                                                                  apNoInbox,
		`{"id":"https://a.example/u","type":"Person","inbox":"https://a.example/u/inbox","suspended":0}`:                                                                apWrongShape,
		`{"id":"https://a.example/u","type":"Person","inbox":"http://a.example/u/inbox"}`:                                                                               apBadID,
		`{"id":"https://a.example/u","type":"Person","inbox":"https://a.example/u/inbox","endpoints":{"sharedInbox":"https://10.0.0.1/inbox"}}`:                         apBadID,
		`{"id":"https://a.example/u","type":"Person","inbox":"https://a.example/u/inbox","publicKey":{"id":"https://a.example/u#k","owner":"https://a.example:444/u"}}`: apBadID,
	} {
		if _, err := decodeActor([]byte(doc), apMax); apReasonOf(err) != want {
			t.Errorf("actor %s: %v, want %q", doc, err, want)
		}
	}
	for doc, want := range map[string]apReason{
		`{"type":"Like","actor":"https://a.example/u","object":"https://b.example/n/1"}`:                                         apNoID,
		`{"id":"https://a.example/l/1","type":"Like","object":"https://b.example/n/1"}`:                                          apNoID,
		`{"id":"https://a.example/l/1","type":"Like","actor":["https://a.example/u","https://e.example/u"]}`:                     apMoreThanOne,
		`{"id":"https://a.example/l/1","type":"Like","actor":"https://a.example/u","object":{"type":"Note","content":"forged"}}`: apNoID,
		`{"id":"https://a.example/l/1","type":"Move","actor":"https://a.example/u","target":"http://e.example/u"}`:               apBadID,
	} {
		if _, err := decodeActivity([]byte(doc), apMax); apReasonOf(err) != want {
			t.Errorf("activity %s: %v, want %q", doc, err, want)
		}
	}
	for r := apReason(1); int(r) < len(apReasons); r++ {
		if r.String() == "refused" {
			t.Errorf("reason %d has no phrase", r)
		}
	}
}

// apAdversarial builds the costly inputs of a given size, outside any timed section: nesting to the
// limit and past it, a list far over the limit, a run of one name, as many distinct names as fit
// (some escaped), and a post that is one long text.
func apAdversarial(size int) map[string][]byte {
	out := map[string][]byte{}
	// Deep: 24 levels repeated side by side until the size, inside lists of 1,000 at most.
	unit := strings.Repeat("[", 21) + strings.Repeat("]", 21) // 3 levels around it: 24
	var b strings.Builder
	b.WriteString(`{"id":"https://a.example/n/1","type":"Note","x":[`)
	for i := 0; b.Len() < size-100; i++ {
		if i%999 == 0 {
			if i > 0 {
				b.WriteString(`],`)
			}
			b.WriteString(`[`)
		} else {
			b.WriteByte(',')
		}
		b.WriteString(unit)
	}
	b.WriteString(`]]}`)
	out["deep, accepted"] = []byte(b.String())
	out["deep, refused"] = []byte(`{"x":` + strings.Repeat("[", size/2-4) + strings.Repeat("]", size/2-4) + `}`)
	out["list, refused"] = []byte(`{"x":[` + strings.Repeat("0,", size/2-6) + `0]}`)
	out["one name, refused"] = []byte(`{` + strings.Repeat(`"a":0,`, size/6-1) + `"a":0}`)
	b.Reset()
	b.WriteString(`{"id":"https://a.example/n/1","type":"Note","x":[`)
	for o := 0; b.Len() < size-200; o++ {
		if o > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		for j := 0; j < 1000 && b.Len() < size-100; j++ {
			if j > 0 {
				b.WriteByte(',')
			}
			// Every name starts with an escaped "a", so each is unescaped to be compared.
			b.WriteString("\"\\u0061" + strconv.Itoa(j) + "\":0")
		}
		b.WriteByte('}')
	}
	b.WriteString(`]}`)
	out["many names, accepted"] = []byte(b.String())
	doc, _ := json.Marshal(map[string]any{"id": "https://a.example/n/1", "type": "Note", "content": strings.Repeat("é", size/2-50)})
	out["long content, cut"] = doc
	return out
}

// TestDecodeAdversarial runs the costly inputs at 64 KB and 1 MB once: each finishes with the
// outcome it should have. How long they take is BenchmarkDecodeAdversarial's.
func TestDecodeAdversarial(t *testing.T) {
	want := map[string]apReason{"deep, accepted": 0, "deep, refused": apTooDeep, "list, refused": apTooMany, "one name, refused": apDuplicate, "many names, accepted": 0, "long content, cut": 0}
	for _, size := range []int{64 << 10, 1 << 20} {
		for name, doc := range apAdversarial(size) {
			if len(doc) > size+100 {
				t.Errorf("%s at %d: %d bytes", name, size, len(doc))
			}
			n, err := decodeNote(doc, 2<<20)
			if apReasonOf(err) != want[name] {
				t.Errorf("%s at %d: %v", name, size, err)
			}
			if name == "long content, cut" && err == nil && n.Cut != (size > 64<<10) { // a 64 KB document fits whole
				t.Errorf("long content at %d: cut %v", size, n.Cut)
			}
		}
	}
}

func BenchmarkDecodeAdversarial(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20} {
		docs := apAdversarial(size)
		names := make([]string, 0, len(docs))
		for name := range docs {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			doc := docs[name]
			b.Run(fmt.Sprintf("%s/%dKB", name, size>>10), func(b *testing.B) {
				b.SetBytes(int64(len(doc)))
				b.ReportAllocs()
				for b.Loop() {
					_, _ = decodeNote(doc, 2<<20)
				}
			})
		}
	}
}

func BenchmarkDecodeNote(b *testing.B) {
	doc := shapeDoc(b, "flowa-6.5-create.json")
	b.ReportAllocs()
	for b.Loop() {
		a, err := decodeActivity(doc, 256<<10)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := a.Object[0].note(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeActor(b *testing.B) {
	doc := shapeDoc(b, "flowa-5.5-actor.json")
	b.ReportAllocs()
	for b.Loop() {
		a, err := decodeActor(doc, apMax)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := a.key("https://flowa.example.org/users/ana#main-key"); err != nil {
			b.Fatal(err)
		}
	}
}

// apFuzzSeeds are the fixtures and a few hostile shapes.
func apFuzzSeeds(f *testing.F) {
	files, _ := filepath.Glob("testdata/federation/shapes/*.json")
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	for _, s := range []string{`{}`, `{"a":"\ud800"}`, "{\"id\":1,\"\\u0069d\":2}", `{"x":[[[[]]]]}`, `{"type":["Note",["x"]]}`, "{\"a\":\"\xff\"}", `{"@id":"x"}`, `{"a":-0.0e+1}`} {
		f.Add([]byte(s))
	}
}

// FuzzDecodeDocument runs every reader over the input: no panic, every refusal typed with a fixed
// phrase, and whatever is read keeps the limits.
func FuzzDecodeDocument(f *testing.F) {
	apFuzzSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		check := func(err error) {
			if err != nil && (apReasonOf(err) == 0 || apReasonOf(err).String() == "refused") {
				t.Fatalf("an untyped refusal: %v", err)
			}
		}
		a, err := decodeActor(b, apMax)
		check(err)
		if err == nil {
			if !apActorKinds[a.Kind] || !apIDOK(a.ID) || (a.Inbox == "" && !a.Suspended) {
				t.Fatalf("actor %+v", a)
			}
			for _, k := range a.Keys {
				_, err := a.key(k.ID)
				check(err)
			}
		}
		act, err := decodeActivity(b, apMax)
		check(err)
		if err == nil {
			n := len(act.Audience.To) + len(act.Audience.Cc) + len(act.Audience.Bto) + len(act.Audience.Bcc) + len(act.Audience.Audience)
			if !apIDOK(act.ID) || !apIDOK(act.Actor) || n > apRecipientsMax {
				t.Fatalf("activity %+v", act)
			}
			for _, o := range act.Object {
				_, err := o.note()
				check(err)
				_, err = o.activity()
				check(err)
				_, err = o.actor()
				check(err)
				_, err = o.tombstone()
				check(err)
				_, err = o.collection()
				check(err)
			}
		}
		n, err := decodeNote(b, apMax)
		check(err)
		if err == nil && (len(n.Content) > apTextMax || !apIDOK(n.ID)) {
			t.Fatalf("note %+v", n)
		}
		_, err = decodeTombstone(b, apMax)
		check(err)
		_, err = decodeCollection(b, apMax)
		check(err)
	})
}

// FuzzDecodeScan holds the scan against encoding/json: what the scan accepts, encoding/json
// accepts; what encoding/json accepts as an object of valid UTF-8 the scan refuses only for a
// limit, a duplicate name or an escaped lone surrogate, never as malformed.
func FuzzDecodeScan(f *testing.F) {
	apFuzzSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		r := apScan(b)
		valid := json.Valid(b)
		switch {
		case r == 0 && !valid:
			t.Fatalf("the scan accepted what encoding/json refuses: %q", b)
		case r == apMalformed && valid:
			t.Fatalf("the scan called well-formed JSON malformed: %q", b)
		case r == 0:
			if _, err := apFieldsOf(b); err != nil && apReasonOf(err) != apKeyword && apReasonOf(err) != apDuplicate {
				t.Fatalf("a scanned document did not decode: %v", err)
			}
		}
	})
}
