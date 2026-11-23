package deliver

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// server hands the delivered files of one project over HTTP, on this
// machine only. A file is asked for by its token; the request names no
// path, so nothing that is not in the register can be reached. It reads
// and never writes.
type server struct {
	reg *register
	ln  net.Listener
	srv *http.Server
	// base is where the server answers, as an address begins:
	// "http://127.0.0.1:7777".
	base string
	// asked is the address it was to listen on, and moved says that one
	// was taken and a free port is used in its place.
	asked string
	moved bool
}

// serve starts a server for the register on addr, an address of this
// machine with a port. When that port cannot be had, any free port of the
// same address is taken, and the server says so (moved). An error means
// there is no server.
func serve(reg *register, addr string) (*server, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%q is not an address with a port", addr)
	}
	ln, err := net.Listen("tcp", addr)
	moved := false
	if err != nil {
		other, err2 := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err2 != nil {
			return nil, fmt.Errorf("%s cannot be listened on (%v), and no other port of %s can either (%v)", addr, err, host, err2)
		}
		ln, moved = other, true
	}
	// The option only takes an address of this machine alone; this is the
	// last look, at the address the system really gave.
	if tcp, ok := ln.Addr().(*net.TCPAddr); !ok || !tcp.IP.IsLoopback() {
		ln.Close()
		return nil, fmt.Errorf("%s is not an address of this machine alone, and delivered files are served to this machine only", ln.Addr())
	}
	ln = &capped{Listener: ln, slots: make(chan struct{}, maxConns), done: make(chan struct{})}
	s := &server{reg: reg, ln: ln, base: "http://" + ln.Addr().String(), asked: addr, moved: moved}
	s.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		// A connection kept open with nothing asked of it is closed: each
		// held a goroutine and a descriptor for as long as it was kept.
		IdleTimeout: 2 * time.Minute,
		// What the server has to say of a broken connection goes
		// nowhere: its own log would write into the terminal the
		// interface draws on.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// close stops the server and whatever it is sending.
func (s *server) close() {
	if s != nil {
		_ = s.srv.Close()
	}
}

// address is where an entry of the register is served: the server's own
// address, the token, and the file's name.
func (s *server) address(rec record) string {
	return s.base + "/" + rec.Token + "/" + url.PathEscape(rec.Name)
}

// ServeHTTP answers one request. The path is /<token>/<name>, where the
// name is the one the file was delivered under; anything else, and a
// token that is not in the register, is not found, in the same words, so
// that an answer never says how close a guess was.
//
// The server shares its origin with every service of this machine's
// loopback, and every project's files share it with each other, so what
// it serves is kept from that origin: a page or a picture that can run a
// script runs it sandboxed, with an origin of its own; a service worker
// is never served, which could take every later address on the origin
// (and without the token alone as a path, none could claim all of it);
// and only a request for this machine by name is answered, which a page
// of another site that rebinds its name to 127.0.0.1 is not (the
// deliverables review of 2026-11-19).
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	if !loopbackHost(r.Host) {
		http.Error(w, "gnosh serves delivered files at the addresses of this machine (127.0.0.1, localhost) alone.", http.StatusMisdirectedRequest)
		return
	}
	if r.Header.Get("Service-Worker") != "" {
		http.Error(w, "gnosh does not serve a delivered file as a service worker.", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		http.Error(w, "gnosh serves delivered files to be read: only GET and HEAD are answered here.", http.StatusMethodNotAllowed)
		return
	}
	const none = "There is nothing at this address. gnosh serves each delivered file at an address of its own, which the session that delivered it shows; nothing is listed."
	token, name, ok := splitPath(r.URL.Path)
	if !ok {
		http.Error(w, none, http.StatusNotFound)
		return
	}
	rec, ok := s.reg.byToken(token)
	if !ok || name != rec.Name {
		http.Error(w, none, http.StatusNotFound)
		return
	}
	f, st, err := openRegular(rec.File)
	if err != nil {
		http.Error(w, "The file that was delivered at this address is not there any more, or is no longer a file that can be read.", http.StatusNotFound)
		return
	}
	defer f.Close()
	h.Set("Content-Type", mediaType(rec.Name))
	if scripted(rec.Name) {
		h.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-downloads")
	}
	// ServeContent answers ranges, which a browser needs to play and to
	// seek in a video, and conditional requests; it writes no body for
	// HEAD, and guesses no type, because one is set.
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// splitPath reads a request's path as a token and a name.
func splitPath(p string) (token, name string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if !strings.HasPrefix(p, "/") || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// loopbackHost reports whether a request's Host names this machine: a
// loopback address, or localhost, on any port (a forward from another
// machine may use another).
func loopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// openRegular opens a file to serve it, and returns it with what the
// system says of it. Only a regular file is opened, and never through a
// link: a path that has become a link since the file was delivered leads
// wherever whoever made the link wants, which is not what was delivered.
// That holds for every folder on the way too (path is the file as it was
// delivered, its links followed then); and a file another account has
// put there since, as can happen in /tmp once the one delivered was
// cleaned away, is not served either (the deliverables review of
// 2026-11-19).
func openRegular(path string) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, errors.New("not a regular file")
	}
	if real, err := filepath.EvalSymlinks(path); err != nil || real != path {
		return nil, nil, errors.New("a folder on its way has become a link")
	}
	if strangerOwns(before) {
		return nil, nil, errors.New("another account owns it")
	}
	// (Opened so that a pipe put in its place since cannot hold the open.)
	f, err := openNoWait(path)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(before, st) {
		f.Close()
		return nil, nil, errors.New("not the regular file that was there a moment ago")
	}
	return f, st, nil
}

// maxConns is how many connections the server holds open at once: more
// wait until one closes. Each held a goroutine and a descriptor for as
// long as it was kept (the deliverables review of 2026-11-19).
var maxConns = 64

// capped is a listener that holds at most cap(slots) connections open at
// once.
type capped struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
}

func (c *capped) Accept() (net.Conn, error) {
	select {
	case c.slots <- struct{}{}:
	case <-c.done:
		return nil, net.ErrClosed
	}
	conn, err := c.Listener.Accept()
	if err != nil {
		<-c.slots
		return nil, err
	}
	return &slotConn{Conn: conn, free: sync.OnceFunc(func() { <-c.slots })}, nil
}

func (c *capped) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Listener.Close()
}

// slotConn gives its slot back when it is closed.
type slotConn struct {
	net.Conn
	free func()
}

func (s *slotConn) Close() error {
	err := s.Conn.Close()
	s.free()
	return err
}
