package main

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// FLOWA_FEDERATION_ALLOW_PRIVATE, the two-instance test's setting (test/federation.sh, federation.md 5.1): off unless
// it says on, read only in a mode that federates, refused with an origin that is not under .test, and anything but on
// or off stops the start with a short message naming it.
func TestAllowPrivateSetting(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		all := map[string]string{"FLOWA_ORIGIN": "https://one.flowa.test", "FLOWA_KEY_SECRET": testSecret}
		for k, v := range m {
			all[k] = v
		}
		return func(k string) string { return all[k] }
	}
	for _, v := range []string{"", " ", "off", "OFF", " Off\n"} {
		if s, err := parseFederation(modeFederated, env(map[string]string{"FLOWA_FEDERATION_ALLOW_PRIVATE": v})); err != nil || s.allowPrivate {
			t.Errorf("FLOWA_FEDERATION_ALLOW_PRIVATE=%q: %v, %v; want off", v, s.allowPrivate, err)
		}
	}
	for _, v := range []string{"on", "ON", "\tOn "} {
		if s, err := parseFederation(modeFederated, env(map[string]string{"FLOWA_FEDERATION_ALLOW_PRIVATE": v})); err != nil || !s.allowPrivate {
			t.Errorf("FLOWA_FEDERATION_ALLOW_PRIVATE=%q with a .test origin: %v, %v; want on", v, s.allowPrivate, err)
		}
	}
	for _, v := range []string{"yes", "true", "1", "onn", strings.Repeat("x", 5000)} {
		_, err := parseFederation(modeFederated, env(map[string]string{"FLOWA_FEDERATION_ALLOW_PRIVATE": v}))
		if err == nil || !strings.Contains(err.Error(), "FLOWA_FEDERATION_ALLOW_PRIVATE must be on or off") || len(err.Error()) > 250 {
			t.Errorf("FLOWA_FEDERATION_ALLOW_PRIVATE=%.20q: %v; want a short error naming the setting", v, err)
		}
	}
	// A production-looking origin is refused: the setting is for names under .test, which no public server has.
	for _, o := range []string{"https://flowa.example.org", "https://flowa.testing.org", "https://test.example.org", "https://flowa.test.example"} {
		_, err := parseFederation(modeFederated, env(map[string]string{"FLOWA_ORIGIN": o, "FLOWA_FEDERATION_ALLOW_PRIVATE": "on"}))
		if err == nil || !strings.Contains(err.Error(), "not a name under .test") {
			t.Errorf("on with FLOWA_ORIGIN=%s: %v; want refused", o, err)
		}
	}
	// A mode that does not federate never reads it, as it never reads the origin: a deployment that sets nothing new,
	// or a stray value, behaves exactly as before.
	for _, m := range []instanceMode{modeLocal, modeSingle} {
		if s, err := parseFederation(m, env(map[string]string{"FLOWA_FEDERATION_ALLOW_PRIVATE": "garbage", "FLOWA_ORIGIN": "https://flowa.example.org"})); err != nil || s.allowPrivate || s.on {
			t.Errorf("%s mode with a stray value: %+v, %v", m, s, err)
		}
	}
}

// privateDialGuard lets private ranges through beside public ones, and nothing else dialGuard refuses: loopback,
// link-local (the metadata address), the reserved ranges, another port, and the fetcher's deny list.
func TestPrivateDialGuard(t *testing.T) {
	for addr, ok := range map[string]bool{
		"172.18.0.3:443":               true,
		"10.1.2.3:443":                 true,
		"192.168.1.20:443":             true,
		"[fd00::1]:443":                false, // unique local IPv6: the test network is IPv4
		"[fd00:ec2::254]:443":          false, // a cloud's metadata address over IPv6
		"[::ffff:10.1.2.3]:443":        true,  // the IPv4 address it carries
		"93.184.216.34:443":            true,
		"172.18.0.3:3169":              false,
		"172.18.0.3:80":                false,
		"127.0.0.1:443":                false,
		"[::1]:443":                    false,
		"169.254.169.254:443":          false,
		"[::ffff:169.254.169.254]:443": false,
		"[fe80::1]:443":                false,
		"0.0.0.0:443":                  false,
		"100.64.0.1:443":               false,
		"224.0.0.1:443":                false,
		"not-an-address:443":           false,
	} {
		err := privateDialGuard(addr, nil)
		var ar *addrRefused
		if ok && err != nil || !ok && !errors.As(err, &ar) {
			t.Errorf("%s: %v, want allowed %v", addr, err, ok)
		}
	}
	deny := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/24")}
	if privateDialGuard("172.18.0.3:443", deny) == nil || privateDialGuard("172.19.0.3:443", deny) != nil {
		t.Error("the deny list does not hold under FLOWA_FEDERATION_ALLOW_PRIVATE")
	}
	// The fetcher a build makes never has it on: only start sets it, from the setting.
	f := newFetcher("", false)
	if f.fedClient != nil {
		t.Error("a new fetcher lets private addresses through")
	}
	// With the setting on, a link preview still may not dial a private address, and a federation request may: the
	// preview's Transport refuses 10.255.255.1 before any connection, federation's tries to connect (and runs out of
	// the short time given, nothing answering there).
	f.allowFedPrivate()
	dial := func(tr *http.Transport) error {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		c, err := tr.DialContext(ctx, "tcp", "10.255.255.1:443")
		if c != nil {
			c.Close()
		}
		return err
	}
	var ar *addrRefused
	if err := dial(f.tr); !errors.As(err, &ar) {
		t.Errorf("a link preview to a private address with the setting on: %v, want refused", err)
	}
	if err := dial(f.fedTr); errors.As(err, &ar) {
		t.Errorf("a federation request to a private address with the setting on: %v, want let through", err)
	}
	// And the kinds go through their own client: a preview never through federation's.
	if f.client == f.fedClient || f.client.Transport != f.tr || f.fedClient.Transport != f.fedTr {
		t.Error("previews and federation share a client under the setting")
	}
}
