package webtool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMarkdown(t *testing.T) {
	page := `<!DOCTYPE html><html><head><title>ignored head</title><style>p{color:red}</style>
<script>alert("x")</script></head><body>
<nav><a href="/">Home</a></nav>
<h1>The   Title</h1>
<p>A paragraph with <b>bold</b>, <em>slanted</em> and <code>code()</code> in it,
spread over
lines &amp; with an entity.</p>
<ul><li>one</li><li>two <a href="https://example.org/x?a=1&amp;b=2">a link</a></li></ul>
<ol><li>first</li><li>second</li></ol>
<pre>  keep
    this   as it is
</pre>
<table><tr><th>name</th><th>value</th></tr><tr><td>a</td><td>1</td></tr></table>
<p>Line<br>break</p><img src="x.png" alt="a picture"><!-- a comment -->
</body></html>`
	got := Markdown(page)
	for _, want := range []string{
		"[Home](/)",
		"# The Title",
		"A paragraph with **bold**, *slanted* and `code()` in it, spread over lines & with an entity.",
		"- one\n- two [a link](https://example.org/x?a=1&b=2)",
		"1. first\n2. second",
		"```\n  keep\n    this   as it is\n",
		"name | value\na | 1",
		"Line\nbreak",
		"[image: a picture]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the Markdown does not hold %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"alert", "color:red", "ignored head", "a comment", "<", "\n\n\n"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the Markdown holds %q:\n%s", unwanted, got)
		}
	}
}

func TestCheckRefusesWhatIsNotTheOpenWeb(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:8080/x", "https://LOCALHOST/", "https://api.localhost/", "https://intranet/page",
		"https://127.0.0.1/", "https://10.0.0.5/", "https://192.168.1.91:8080/", "https://[::1]/", "https://169.254.169.254/latest",
		"https://100.64.1.2/", "ftp://example.org/x", "file:///etc/passwd", "https://user:pw@example.org/", "not a url",
		"https://example.org/" + strings.Repeat("a", MaxURL),
		"https://*.com/x", "https://pypi*.org/simple", // a wildcard is no host
	} {
		if _, err := Check(raw, false); !errors.Is(err, ErrRefused) {
			t.Errorf("%q: got %v, want a refusal", raw, err)
		}
	}
	u, err := Check("http://example.org/a?b=c", false)
	if err != nil || u.String() != "https://example.org/a?b=c" {
		t.Errorf("an http URL comes back as %v, %v; want it upgraded to https", u, err)
	}
	if _, err := Check("https://8.8.8.8/", false); err != nil {
		t.Errorf("a public address was refused: %v", err)
	}
}

func TestFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "gnosh-test" {
			http.Error(w, "who are you", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html><body><h2>Hello</h2><p>The page.</p></body></html>"))
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/page", http.StatusFound)
	})
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.org/elsewhere", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(strings.Repeat("x", 3000)))
	})
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 'P', 'N', 'G', 0, 0, 0xff, 0xfe})
	})
	// A PDF, all of it ASCII, and a text, each sent as octet-stream; a PDF
	// sent with no type at all; bytes that are not UTF-8, of a type that
	// says nothing.
	mux.HandleFunc("/octet-pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n"))
	})
	mux.HandleFunc("/octet-text", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("just words\n"))
	})
	mux.HandleFunc("/untyped-pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil
		w.Write([]byte("%PDF-1.4\n%%EOF\n"))
	})
	// A shell installer with its archive after the script: it begins as
	// text, and is not.
	mux.HandleFunc("/installer", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("#!/bin/sh\nexec tail -n +3 \"$0\" | tar xz\n\x1f\x8b\x08\x00\xff\xfe\x00\x00"))
	})
	mux.HandleFunc("/latin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-thing")
		w.Write([]byte{'a', 0xe9, 'b'})
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	tlsConf := srv.Client().Transport.(*http.Transport).TLSClientConfig
	o := Options{UserAgent: "gnosh-test", AllowPrivate: true, TLS: tlsConf}
	ctx := context.Background()

	p, err := Fetch(ctx, srv.URL+"/page", o)
	if err != nil || p.Code != 200 || p.ContentType != "text/html" || !strings.Contains(p.Text, "## Hello") || !strings.Contains(p.Text, "The page.") {
		t.Fatalf("the page: %+v, %v", p, err)
	}

	// A redirect on the same host is followed.
	p, err = Fetch(ctx, srv.URL+"/moved", o)
	if err != nil || !strings.HasSuffix(p.URL, "/page") || !strings.Contains(p.Text, "## Hello") {
		t.Errorf("the redirect on the same host: %+v, %v", p, err)
	}
	// One to another host is not, and the page says where it led.
	p, err = Fetch(ctx, srv.URL+"/away", o)
	if err != nil || p.Redirect != "https://example.org/elsewhere" || p.Text != "" {
		t.Errorf("the redirect to another host: %+v, %v", p, err)
	}

	// The size bound.
	small := o
	small.MaxBytes = 1000
	p, err = Fetch(ctx, srv.URL+"/plain", small)
	if err != nil || !p.Cut || p.Bytes != 1000 || len(p.Text) != 1000 {
		t.Errorf("a page over the bound: bytes %d, cut %v, %v", p.Bytes, p.Cut, err)
	}

	// What is not text comes back as its bytes, for the tool to keep, with
	// what it is: read from its first bytes when the server said only
	// octet-stream, or nothing.
	p, err = Fetch(ctx, srv.URL+"/binary", o)
	if err != nil || p.ContentType != "image/png" || len(p.Body) != 8 || p.Text != "" {
		t.Errorf("a picture: %+v, %v", p, err)
	}
	p, err = Fetch(ctx, srv.URL+"/octet-pdf", o)
	if err != nil || p.ContentType != "application/pdf" || !strings.HasPrefix(string(p.Body), "%PDF-1.7") || p.Text != "" {
		t.Errorf("a PDF sent as octet-stream: %+v, %v", p, err)
	}
	p, err = Fetch(ctx, srv.URL+"/octet-text", o)
	if err != nil || p.ContentType != "text/plain" || p.Text != "just words\n" || p.Body != nil {
		t.Errorf("a text sent as octet-stream: %+v, %v", p, err)
	}
	p, err = Fetch(ctx, srv.URL+"/untyped-pdf", o)
	if err != nil || p.ContentType != "application/pdf" || p.Body == nil {
		t.Errorf("a PDF sent with no type: %+v, %v", p, err)
	}
	p, err = Fetch(ctx, srv.URL+"/installer", o)
	if err != nil || p.Body == nil || p.Text != "" || p.ContentType != "application/octet-stream" {
		t.Errorf("an installer that begins as text: %+v, %v", p, err)
	}
	p, err = Fetch(ctx, srv.URL+"/latin", o)
	if err != nil || p.ContentType != "application/x-thing" || string(p.Body) != "a\xe9b" || p.Text != "" {
		t.Errorf("bytes that are not UTF-8: %+v, %v", p, err)
	}

	// Without AllowPrivate the test server, on this machine, is refused
	// before any request.
	if _, err := Fetch(ctx, srv.URL+"/page", Options{TLS: tlsConf}); !errors.Is(err, ErrRefused) {
		t.Errorf("a fetch from this machine: %v, want a refusal", err)
	}
}
