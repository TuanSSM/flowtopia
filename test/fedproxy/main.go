// Command fedproxy is the TLS end of one instance in the two-instance test (test/federation.sh): it serves HTTPS on
// :443 with a certificate the test's own authority signed at the start of the run, and passes every request to one
// Flowa over plain HTTP on the test's Docker network, as the reverse proxy in front of a real deployment does
// (README, "Federation: HTTP signatures"): at the root of the origin, the path unchanged (signatures cover it) and
// the Host header too (the name the app answers to), and X-Forwarded-For and X-Forwarded-Proto say who asked and
// over what. Flowa itself never serves TLS, and compose.yml bundles no proxy; this one exists only inside the
// test's network. It logs one line per request
// (method, path, status, time; never a header or a body), which is how the test sees that deliveries came to the
// shared inbox and were answered 202.
//
//	fedproxy -cert server.pem -key server-key.pem -to http://flowa-app:3169
package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	cert := flag.String("cert", "/tls/cert.pem", "the server's certificate, chained to the test's authority")
	key := flag.String("key", "/tls/key.pem", "its private key")
	to := flag.String("to", "", "the Flowa behind it, such as http://flowa-app:3169")
	listen := flag.String("listen", ":443", "where to serve HTTPS")
	flag.Parse()
	target, err := url.Parse(*to)
	if err != nil || target.Scheme != "http" || target.Host == "" {
		log.Fatalf("-to must be http://host:port, not %q", *to)
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host // the name the app answers to
			r.SetXForwarded()      // X-Forwarded-For with the asker's address, X-Forwarded-Proto: https
		},
		ErrorLog: log.Default(),
	}
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, sw := time.Now(), &status{ResponseWriter: w, code: http.StatusOK}
		rp.ServeHTTP(sw, r)
		log.Printf("%s %s %d %dms", r.Method, r.URL.Path, sw.code, time.Since(start).Milliseconds())
	})
	srv := &http.Server{Addr: *listen, Handler: logged, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	log.Printf("fedproxy: https on %s to %s", *listen, target)
	log.Fatal(srv.ListenAndServeTLS(*cert, *key))
}

// status keeps the code an answer was sent with, for the log line.
type status struct {
	http.ResponseWriter
	code int
}

func (s *status) WriteHeader(code int) { s.code = code; s.ResponseWriter.WriteHeader(code) }

// Unwrap lets the proxy flush through the wrapper (http.ResponseController).
func (s *status) Unwrap() http.ResponseWriter { return s.ResponseWriter }
