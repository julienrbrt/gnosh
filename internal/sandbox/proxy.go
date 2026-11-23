package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Proxy is a sandboxed command's one way out to the network: an HTTP proxy
// outside the sandbox that takes its requests (CONNECT, for HTTPS and
// anything else over TCP, and plain HTTP for http:// addresses) to the
// hosts Domains allows, and refuses the rest with a 403 that says why. A
// host whose every address is the machine's own or its network's (a name
// that leads to 127.0.0.1, a cloud's metadata address) is refused unless
// it is named as such: a command may not reach the machine's own servers
// through a name of the internet's.
type Proxy struct {
	domains Domains
	ln      net.Listener
	srv     *http.Server
	// lookup finds a host's addresses; net's own, but in tests.
	lookup func(ctx context.Context, host string) ([]net.IPAddr, error)
	// Told is called with each host refused, for the person; nil is
	// nothing.
	Told func(host, why string)
	// Rules says what the permission rules say of a host
	// (WebFetch(domain:...), as Claude Code takes them): a deny rule
	// refuses it, an ask rule has the person asked, an allow rule lets it
	// through. Nil: none.
	Rules func(host string) HostRule
	// Ask asks about a host nothing lets through or refuses: forced when
	// an ask rule names it. kept says the answer is the person's, kept
	// for the session; one that is the mode's (bypass, dontAsk, nobody
	// there) is not kept, as the mode may change (the sandbox review).
	// Nil: such a host is refused.
	Ask func(ctx context.Context, host string, port int, forced bool) (yes bool, why string, kept bool)

	mu       sync.Mutex
	answered map[string]answer
	pending  map[string]chan struct{}
}

// HostRule is what the permission rules say of a host.
type HostRule int

const (
	HostUnruled HostRule = iota // no rule names it
	HostAllowed                 // a rule lets it through
	HostAsked                   // a rule says to ask the person first
	HostDenied                  // a rule refuses it
)

// maxAsked is how many hosts the person is asked about in a session, at
// most: a command that reaches for one host after another would
// otherwise put question after question (the sandbox review).
const maxAsked = 40

// proxyDialWait bounds the making of one connection outward.
const proxyDialWait = 30 * time.Second

// ListenProxy starts a proxy on network and address ("unix" and a socket's
// path, or "tcp" and "127.0.0.1:0").
func ListenProxy(network, address string, d Domains) (*Proxy, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	p := &Proxy{domains: d, ln: ln, lookup: net.DefaultResolver.LookupIPAddr}
	p.srv = &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// Addr is where the proxy listens.
func (p *Proxy) Addr() net.Addr { return p.ln.Addr() }

// Close stops the proxy and the connections it carries.
func (p *Proxy) Close() error { return p.srv.Close() }

// ServeHTTP is one request of a sandboxed command's.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, port := r.URL.Hostname(), r.URL.Port()
	if r.Method == http.MethodConnect {
		h, pt, err := net.SplitHostPort(r.Host)
		if err != nil {
			p.refuse(w, r.Host, "a CONNECT names a host and a port")
			return
		}
		host, port = h, pt
	}
	if port == "" {
		port = "80"
		if r.URL.Scheme == "https" {
			port = "443"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		p.refuse(w, host, "the port "+port+" is not one")
		return
	}
	h, ok := HostName(host)
	if !ok {
		// (A name with a * in it, or 2130706433 for 127.0.0.1: an answer
		// would be kept as a rule wider, or other, than what was shown:
		// the sandbox review.)
		p.refuse(w, host, "it is no host name, nor an address written in full")
		return
	}
	if ok, why := p.lets(r.Context(), h, n); !ok {
		p.refuse(w, h, why)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), proxyDialWait)
	conn, why := p.dial(ctx, h, port)
	cancel()
	if conn == nil {
		p.refuse(w, host, why)
		return
	}
	if r.Method == http.MethodConnect {
		p.tunnel(w, conn)
		return
	}
	p.forward(w, r, conn)
}

// dial reaches host:port, refusing a host whose every address is local
// unless the host is named as an address or as localhost; or says why
// not.
func (p *Proxy) dial(ctx context.Context, host, port string) (net.Conn, string) {
	h := normalHost(host)
	named := h == "localhost" || net.ParseIP(h) != nil
	addrs, err := p.lookup(ctx, h)
	if err != nil || len(addrs) == 0 {
		return nil, h + " could not be found (" + errText(err) + ")"
	}
	var d net.Dialer
	tried := ""
	for _, a := range addrs {
		if !named && local(a.IP) {
			tried = h + " leads to " + a.IP.String() + ", an address of this machine or its network"
			continue
		}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(a.IP.String(), port))
		if err == nil {
			return conn, ""
		}
		tried = h + " could not be reached (" + err.Error() + ")"
	}
	return nil, tried
}

// local reports whether an address is the machine's own, its network's,
// or no address at all: the loopback, the private ranges, the shared
// address space carrier networks and Tailscale use (100.64.0.0/10), and
// every address of the machine's own interfaces, its public ones among
// them, where a server a firewall keeps from the internet may listen (the
// sandbox review).
func local(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// errText is an error's words, or nothing.
func errText(err error) string {
	if err == nil {
		return "no address"
	}
	return err.Error()
}

// tunnel answers a CONNECT and carries bytes both ways.
func (p *Proxy) tunnel(w http.ResponseWriter, out net.Conn) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		out.Close()
		http.Error(w, "the proxy cannot carry this connection", http.StatusInternalServerError)
		return
	}
	in, buf, err := hj.Hijack()
	if err != nil {
		out.Close()
		return
	}
	_, _ = in.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	carry(in, buf, out)
}

// carry copies between the two until both are done.
func carry(in net.Conn, buf *bufio.ReadWriter, out net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(out, buf)
		closeWrite(out)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(in, out)
		closeWrite(in)
	}()
	wg.Wait()
	in.Close()
	out.Close()
}

// closeWrite ends the writing half of a connection, where it has one.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

// forward sends a plain HTTP request on over conn, and its answer back,
// following no redirect.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, conn net.Conn) {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range []string{"Proxy-Connection", "Proxy-Authorization", "Connection", "Keep-Alive", "Te", "Trailer", "Upgrade"} {
		out.Header.Del(h)
	}
	one := make(chan net.Conn, 1)
	one <- conn
	tr := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			select {
			case c := <-one:
				return c, nil
			default:
				return nil, errors.New("one connection per request")
			}
		},
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	resp, err := tr.RoundTrip(out)
	if err != nil {
		conn.Close()
		http.Error(w, "the proxy could not send the request on: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// refuse answers that the host may not be reached, and why.
func (p *Proxy) refuse(w http.ResponseWriter, host, why string) {
	if p.Told != nil {
		p.Told(host, why)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, "gnosh's sandbox did not let the command reach "+strings.TrimSpace(host)+": "+why+
		". The person may add it to sandbox.network.allowedDomains, or the command may run outside the sandbox (dangerouslyDisableSandbox), after asking.\n")
}

// lets decides whether the host (as HostName gives it) may be reached:
// never one deniedDomains or a deny rule names; one an ask rule names as the person answers; one
// allowedDomains names, or an allow rule; else as the person answers,
// asked once per host for the session (several connections to a new host
// at once ask once, and one that gives up waiting stops waiting).
func (p *Proxy) lets(ctx context.Context, h string, port int) (bool, string) {
	v, why := p.domains.verdict(h, port)
	if v == denied {
		return false, why
	}
	rule := HostUnruled
	if p.Rules != nil {
		rule = p.Rules(h)
	}
	switch {
	case rule == HostDenied:
		return false, h + " is refused by a WebFetch(domain:...) deny rule"
	case rule == HostAsked:
	case v == listed || rule == HostAllowed:
		return true, ""
	}
	if p.Ask == nil {
		return false, why
	}
	for {
		p.mu.Lock()
		if a, done := p.answered[h]; done {
			p.mu.Unlock()
			return a.yes, a.why
		}
		if wait := p.pending[h]; wait != nil {
			p.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return false, h + " was not answered for"
			}
		}
		if len(p.answered) >= maxAsked {
			p.mu.Unlock()
			return false, fmt.Sprintf("%d hosts have been asked about in this session already; one more is refused (sandbox.network.allowedDomains lets it through)", maxAsked)
		}
		if p.pending == nil {
			p.pending, p.answered = map[string]chan struct{}{}, map[string]answer{}
		}
		wait := make(chan struct{})
		p.pending[h] = wait
		p.mu.Unlock()
		yes, why, kept := p.Ask(ctx, h, port, rule == HostAsked)
		p.mu.Lock()
		delete(p.pending, h)
		if kept && ctx.Err() == nil {
			p.answered[h] = answer{yes: yes, why: why}
		}
		p.mu.Unlock()
		close(wait)
		if ctx.Err() != nil {
			// (Taken back before it was answered: nothing kept.)
			return false, h + " was not answered for"
		}
		return yes, why
	}
}

// HostName is a host as the proxy takes it, lower case, without a last
// dot or an IPv6 address's brackets: a name of letters, digits, dashes
// and underscores, in labels of at most 63, the last not all digits, or
// an address written in full (an IPv4 address's four numbers; any form
// of an IPv6 one, given back as Go writes it). ok is false for anything
// else: a name with a * in it, an address written short or as one number
// (127.1, 2130706433), which resolvers take for an address but rules do
// not.
func HostName(host string) (string, bool) {
	h := normalHost(host)
	if h == "" || len(h) > 253 {
		return "", false
	}
	if ip := net.ParseIP(h); ip != nil {
		// (Go takes an IPv4 address only as four numbers, no zero ahead
		// of one.)
		return ip.String(), true
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", false
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return "", false
			}
		}
	}
	last := labels[len(labels)-1]
	if strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x") {
		return "", false
	}
	return h, true
}

// answer is what Ask said of a host, kept for the session.
type answer struct {
	yes bool
	why string
}
