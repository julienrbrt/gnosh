package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Claude Code's allowedDomains and deniedDomains: a name, *.name for the
// names below it, an address (IPv6 in brackets), each with :port for that
// port alone; denied wins.
func TestDomains(t *testing.T) {
	d := Domains{Allowed: []string{"github.com", "*.npmjs.org", "api.example.com:443", "[::1]:8080", "10.0.0.5"}, Denied: []string{"bad.npmjs.org", "github.com:22"}}
	for _, c := range []struct {
		host string
		port int
		want bool
	}{
		{"github.com", 443, true}, {"GitHub.com.", 80, true}, {"github.com", 22, false}, {"api.github.com", 443, false},
		{"registry.npmjs.org", 443, true}, {"npmjs.org", 443, false}, {"bad.npmjs.org", 443, false}, {"xnpmjs.org", 443, false},
		{"api.example.com", 443, true}, {"api.example.com", 80, false}, {"[::1]", 8080, true}, {"::1", 8080, true}, {"::1", 80, false},
		{"10.0.0.5", 1, true}, {"", 443, false}, {"elsewhere.example", 443, false},
	} {
		if got, why := d.Allows(c.host, c.port); got != c.want {
			t.Errorf("Allows(%q, %d) = %v (%s)", c.host, c.port, got, why)
		}
	}
	if _, why := d.Allows("elsewhere.example", 443); !strings.Contains(why, "not in sandbox.network.allowedDomains") {
		t.Errorf("the reason: %s", why)
	}
}

// The proxy carries a CONNECT and a plain request to an allowed host,
// refuses another with a 403 that says why, and refuses a name of the
// internet's that leads to the machine itself.
func TestProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "hello from "+r.URL.Path) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	var told []string
	p, err := ListenProxy("tcp", "127.0.0.1:0", Domains{Allowed: []string{"127.0.0.1", "rebound.example"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Told = func(host, why string) { told = append(told, host) }
	p.lookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host == "rebound.example" {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}
	proxyURL, _ := url.Parse("http://" + p.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	// A plain request.
	resp, err := client.Get(srv.URL + "/plain")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello from /plain" {
		t.Errorf("a plain request: %d %q", resp.StatusCode, body)
	}

	// A CONNECT, by hand.
	c, err := net.Dial("tcp", p.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "CONNECT "+u.Host+" HTTP/1.1\r\nHost: "+u.Host+"\r\n\r\n")
	br := bufio.NewReader(c)
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "200") {
		t.Fatalf("CONNECT: %q", line)
	}
	_, _ = br.ReadString('\n')
	_, _ = io.WriteString(c, "GET /tunneled HTTP/1.1\r\nHost: "+u.Host+"\r\nConnection: close\r\n\r\n")
	rest, _ := io.ReadAll(br)
	c.Close()
	if !strings.Contains(string(rest), "hello from /tunneled") {
		t.Errorf("through the tunnel: %q", rest)
	}

	// A host not allowed, and a name that leads to the machine itself.
	for _, target := range []string{"http://elsewhere.example/x", "http://rebound.example:" + u.Port() + "/x"} {
		resp, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "gnosh's sandbox did not let the command reach") {
			t.Errorf("%s: %d %q", target, resp.StatusCode, body)
		}
	}
	if len(told) != 2 || told[0] != "elsewhere.example" || told[1] != "rebound.example" {
		t.Errorf("told: %q", told)
	}
}

// A host neither list names is asked about once for the session, however
// many connections reach for it at once, and the answer kept; a denied
// host is never asked about.
func TestProxyAsks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()
	p, err := ListenProxy("tcp", "127.0.0.1:0", Domains{Denied: []string{"denied.example"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var mu sync.Mutex
	asked := map[string]int{}
	p.Ask = func(ctx context.Context, host string, port int, forced bool) (bool, string, bool) {
		mu.Lock()
		asked[host]++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		return host == "127.0.0.1", "the person said no to " + host, true
	}
	proxyURL, _ := url.Parse("http://" + p.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	get := func(target string) (int, string) {
		resp, err := client.Get(target)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, body := get(srv.URL + "/x"); code != 200 || body != "ok" {
				t.Errorf("an approved host: %d %q", code, body)
			}
		}()
	}
	wg.Wait()
	if code, body := get("http://other.example/x"); code != http.StatusForbidden || !strings.Contains(body, "the person said no to other.example") {
		t.Errorf("a refused host: %d %q", code, body)
	}
	get("http://other.example/y")
	get("http://denied.example/")
	mu.Lock()
	defer mu.Unlock()
	if asked["127.0.0.1"] != 1 || asked["other.example"] != 1 || asked["denied.example"] != 0 {
		t.Errorf("asked: %v", asked)
	}
}

// What the proxy takes for a host: a name of letters, digits, dashes and
// underscores whose last label is not all digits, or an address written
// in full; not a name with a * in it, nor an address written short or as
// one number, which a resolver would take for 127.0.0.1 (the sandbox
// review).
func TestHostName(t *testing.T) {
	for in, want := range map[string]string{
		"GitHub.com.": "github.com", "registry.npmjs.org": "registry.npmjs.org", "_dmarc.example.com": "_dmarc.example.com",
		"127.0.0.1": "127.0.0.1", "[::1]": "::1", "::FFFF:127.0.0.1": "127.0.0.1", "x-1.example": "x-1.example",
	} {
		if got, ok := HostName(in); !ok || got != want {
			t.Errorf("HostName(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "*npmjs.org", "*.npmjs.org", "2130706433", "127.1", "0x7f.1", "0x7f000001", "a..b", "-a.com", "a-.com", "exa mple.com", "ex\x1bample.com", "1.2.3.04", strings.Repeat("a", 64) + ".com"} {
		if got, ok := HostName(bad); ok {
			t.Errorf("HostName(%q) took it, as %q", bad, got)
		}
	}
}

// The permission rules and the answers: a deny rule refuses a host,
// asked about or listed; an ask rule has the person asked though a list
// lets the host through; an allow rule lets it through unasked; an answer
// the mode gave is not kept, the person's is; a CONNECT to a host written
// as one number is refused before anybody is asked; after maxAsked hosts,
// no more questions.
func TestProxyRulesAndAnswers(t *testing.T) {
	p, err := ListenProxy("tcp", "127.0.0.1:0", Domains{Allowed: []string{"listed.example"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Rules = func(host string) HostRule {
		switch host {
		case "denied.example":
			return HostDenied
		case "listed.example":
			return HostAsked
		case "allowed.example":
			return HostAllowed
		}
		return HostUnruled
	}
	var mu sync.Mutex
	asked := map[string]int{}
	var forcedFor []string
	p.Ask = func(ctx context.Context, host string, port int, forced bool) (bool, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		asked[host]++
		if forced {
			forcedFor = append(forcedFor, host)
		}
		// (mode.example: an answer the mode gives, not kept.)
		return false, "no to " + host, host != "mode.example"
	}
	ctx := context.Background()
	for host, want := range map[string]bool{"denied.example": false, "listed.example": false, "allowed.example": true, "mode.example": false} {
		if got, why := p.lets(ctx, host, 443); got != want {
			t.Errorf("%s: %v (%s)", host, got, why)
		}
	}
	p.lets(ctx, "mode.example", 443)
	p.lets(ctx, "listed.example", 443)
	mu.Lock()
	if asked["denied.example"] != 0 || asked["allowed.example"] != 0 || asked["listed.example"] != 1 || asked["mode.example"] != 2 {
		t.Errorf("asked: %v", asked)
	}
	if len(forcedFor) != 1 || forcedFor[0] != "listed.example" {
		t.Errorf("forced: %v", forcedFor)
	}
	mu.Unlock()
	proxyURL, _ := url.Parse("http://" + p.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get("http://2130706433/")
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "no host name, nor an address written in full") {
			t.Errorf("a host as one number: %d %s", resp.StatusCode, body)
		}
	}
	for i := 0; i < maxAsked+5; i++ {
		p.lets(ctx, fmt.Sprintf("h%d.example", i), 443)
	}
	mu.Lock()
	n := 0
	for host := range asked {
		if strings.HasPrefix(host, "h") {
			n++
		}
	}
	mu.Unlock()
	if want := maxAsked - 1; n != want {
		t.Errorf("%d hosts asked about up to the cap of %d (one answer kept before)", n, want)
	}
	if ok, why := p.lets(ctx, "one-more.example", 443); ok || !strings.Contains(why, "asked about in this session already") {
		t.Errorf("past the cap: %v %s", ok, why)
	}
}

// One waiting on another connection's question stops waiting when its
// own request is taken back.
func TestProxyWaiterGivesUp(t *testing.T) {
	p, err := ListenProxy("tcp", "127.0.0.1:0", Domains{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	release := make(chan struct{})
	p.Ask = func(ctx context.Context, host string, port int, forced bool) (bool, string, bool) {
		<-release
		return true, "", true
	}
	go p.lets(context.Background(), "slow.example", 443)
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if ok, _ := p.lets(ctx, "slow.example", 443); ok || time.Since(start) > 2*time.Second {
		t.Errorf("the waiter: %v after %s", ok, time.Since(start))
	}
	close(release)
}

// The machine's own and its network's addresses: the shared address space
// (100.64.0.0/10) and each of its interfaces' among them.
func TestLocal(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.1.2.3", "100.64.0.1", "100.127.255.254", "169.254.169.254", "::1", "fd00::1"} {
		if !local(net.ParseIP(a)) {
			t.Errorf("%s is not local", a)
		}
	}
	for _, a := range []string{"100.63.255.255", "100.128.0.1", "8.8.8.8"} {
		if local(net.ParseIP(a)) {
			t.Errorf("%s is local", a)
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !local(n.IP) {
			t.Errorf("an interface's own address %s is not local", n.IP)
		}
	}
}
