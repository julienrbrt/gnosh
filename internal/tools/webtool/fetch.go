// Package webtool fetches a web page for the WebFetch tool and turns it
// into text a model can read: HTML into Markdown, other text as it is.
// What is not text (a picture, a PDF, an archive) comes back as its bytes,
// for the tool to keep in a file.
//
// It fetches from the open web only. A URL is refused before any request
// when its host is localhost, a name without a dot, or an address of this
// machine or of a private network, and the addresses a name resolves to are
// held to the same rule when the connection is made, so that a name cannot
// lead to such a place either: what runs on this machine or the local
// network is for curl through the shell tool, under its own rules. http is
// upgraded to https. A redirect is followed only on the same host; one to
// another host is not, and the page says where it led.
package webtool

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Options are how Fetch behaves. The zero value is usable.
type Options struct {
	// UserAgent is the User-Agent header. Default "gnosh".
	UserAgent string
	// Timeout bounds the whole download, redirects included. Default five
	// minutes; a negative value means no bound.
	Timeout time.Duration
	// MaxBytes is the most of a response that is read. Default 10 MiB.
	MaxBytes int64
	// AllowPrivate lets addresses of this machine and of private networks
	// be fetched. It is for tests.
	AllowPrivate bool
	// TLS is the TLS configuration, for tests that serve their own
	// certificate. Nil means the system's.
	TLS *tls.Config
}

// Page is what a fetch came to.
type Page struct {
	// URL is the address that answered, after the redirects that were
	// followed.
	URL string
	// Code and Status are the response's status.
	Code   int
	Status string
	// ContentType is the response's media type, without its parameters.
	ContentType string
	// Bytes is how much of the response was read, and Cut says the
	// response was longer than that.
	Bytes int
	Cut   bool
	// Text is the page as text: Markdown made of HTML, or the response's
	// own text.
	Text string
	// Body is the response itself when it is not text (a picture, a PDF,
	// an archive, or bytes that are not UTF-8), and Text is then empty.
	// ContentType says what it is, read from its first bytes when the
	// server named no type, or only application/octet-stream.
	Body []byte
	// Redirect is set when the page redirects to another host, which was
	// not followed: it is the address it led to, and Text is empty.
	Redirect string
	// Took is how long the fetch took.
	Took time.Duration
}

// ErrRefused wraps every refusal made before a request goes out.
var ErrRefused = errors.New("refused")

// MaxURL is the most characters of a URL Fetch takes, as Claude Code's
// WebFetch: a longer one is refused before anybody is asked about it.
const MaxURL = 2000

// Check reads a URL as Fetch would and says why it would be refused, or
// returns the URL Fetch would ask for (http upgraded to https).
func Check(raw string, allowPrivate bool) (*url.URL, error) {
	if n := utf8.RuneCountInString(strings.TrimSpace(raw)); n > MaxURL {
		return nil, fmt.Errorf("%w: the URL is %d characters long, and one of at most %d is fetched", ErrRefused, n, MaxURL)
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: %q is not a URL with a host", ErrRefused, raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "https"
	case "https":
	default:
		return nil, fmt.Errorf("%w: only http and https URLs are fetched, and this one is %s", ErrRefused, u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: the URL carries a user name or a password, which are not sent", ErrRefused)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if allowPrivate {
		return u, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if !public(ip) {
			return nil, fmt.Errorf("%w: %s is an address of this machine or of a private network; use curl through the shell for those", ErrRefused, host)
		}
		return u, nil
	}
	if strings.ContainsAny(host, "*?[]{}") {
		// No name on the web holds one, and a rule made from such a host
		// would stand for many.
		return nil, fmt.Errorf("%w: %s is not a host name: it holds a wildcard", ErrRefused, host)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") {
		return nil, fmt.Errorf("%w: %s is not a host on the open web (it is localhost, or a name without a dot); use curl through the shell for those", ErrRefused, host)
	}
	return u, nil
}

// public reports whether an address is one of the open internet's.
func public(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() ||
		// 100.64.0.0/10, the carrier-grade NAT range, is private in all
		// but name.
		ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xC0 == 64)
}

// Fetch gets a page and returns it as text.
func Fetch(ctx context.Context, raw string, o Options) (Page, error) {
	start := time.Now()
	if o.UserAgent == "" {
		o.UserAgent = "gnosh"
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Minute
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 10 << 20
	}
	u, err := Check(raw, o.AllowPrivate)
	if err != nil {
		return Page{}, err
	}
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	if !o.AllowPrivate {
		// The address a name resolves to is held to the rule too, at the
		// moment of connecting: a name cannot lead to this machine.
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !public(ip) {
				return fmt.Errorf("%w: the name resolves to %s, an address of this machine or of a private network", ErrRefused, host)
			}
			return nil
		}
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       o.TLS,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	defer transport.CloseIdleConnections()
	var other string
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("it redirects more than ten times")
			}
			if !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
				other = req.URL.String()
				return http.ErrUseLastResponse
			}
			if strings.EqualFold(req.URL.Scheme, "http") {
				req.URL.Scheme = "https"
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("User-Agent", o.UserAgent)
	req.Header.Set("Accept", "text/markdown, text/html;q=0.9, text/plain;q=0.8, */*;q=0.5")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrRefused) {
			return Page{}, err
		}
		return Page{}, fmt.Errorf("the page could not be fetched: %w", err)
	}
	defer resp.Body.Close()
	p := Page{URL: resp.Request.URL.String(), Code: resp.StatusCode, Status: http.StatusText(resp.StatusCode)}
	if other != "" {
		p.Redirect, p.Took = other, time.Since(start)
		return p, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, o.MaxBytes+1))
	if err != nil {
		return p, fmt.Errorf("the page could not be read whole: %w", err)
	}
	if int64(len(body)) > o.MaxBytes {
		body, p.Cut = body[:o.MaxBytes], true
	}
	p.Bytes = len(body)
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media == "" || media == "application/octet-stream" || media == "binary/octet-stream" {
		// No type, or bytes of no type named: the first bytes say what
		// they are, so that a PDF or a picture sent as octet-stream is
		// known as one (Claude Code 2.1.329 fixed the same in its own).
		// Text it is only when all of it is: a shell installer with its
		// archive after the script begins as text (the review of the
		// day's code, 2026-11-20).
		sniffed, _, _ := mime.ParseMediaType(http.DetectContentType(body))
		if strings.HasPrefix(sniffed, "text/") && (!utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0) {
			sniffed = "application/octet-stream"
		}
		if media == "" || sniffed != "application/octet-stream" {
			media = sniffed
		}
	}
	p.ContentType = media
	text := strings.ToValidUTF8(string(body), "�")
	switch {
	case media == "text/html" || media == "application/xhtml+xml":
		p.Text = Markdown(text)
	case strings.HasPrefix(media, "text/") || media == "application/json" || media == "application/xml" ||
		strings.HasSuffix(media, "+json") || strings.HasSuffix(media, "+xml") || media == "application/javascript":
		p.Text = text
	case binaryType(media) || !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0:
		p.Body = body
	default:
		p.Text = text
	}
	p.Took = time.Since(start)
	return p, nil
}

// binaryType reports whether a media type names bytes that are never read
// as text: pictures, sound, video, fonts, PDFs and archives. (A picture in
// SVG is XML, which is text.)
func binaryType(media string) bool {
	if strings.HasSuffix(media, "+xml") {
		return false
	}
	for _, kind := range []string{"image/", "audio/", "video/", "font/"} {
		if strings.HasPrefix(media, kind) {
			return true
		}
	}
	switch media {
	case "application/pdf", "application/zip", "application/gzip", "application/x-gzip", "application/x-tar",
		"application/x-bzip2", "application/x-xz", "application/zstd", "application/x-7z-compressed",
		"application/vnd.rar", "application/x-rar-compressed", "application/wasm", "application/java-archive",
		"application/ogg", "application/vnd.ms-fontobject":
		return true
	}
	return false
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
