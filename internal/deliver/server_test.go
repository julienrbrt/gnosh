package deliver

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// started is a server for a test: a register with one video and one
// image in it, served on a port the system picked.
type started struct {
	reg   *register
	srv   *server
	video record
	image record
}

// pngHead is how a PNG file begins.
const pngHead = "\x89PNG\r\n\x1a\n"

// startServer makes a register with two files in it and serves it.
func startServer(t *testing.T) *started {
	t.Helper()
	s := &started{reg: newTestRegister(t)}
	video := file(t, "ad.mp4", "0123456789 the bytes of a video")
	image := file(t, "shot.png", pngHead+"the bytes of an image")
	var err error
	if s.video, err = s.reg.add(t.Context(), "", video, video, "the ad"); err != nil {
		t.Fatal(err)
	}
	if s.image, err = s.reg.add(t.Context(), "", image, image, "a screenshot"); err != nil {
		t.Fatal(err)
	}
	if s.srv, err = serve(s.reg, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.srv.close)
	return s
}

// A delivered file is served at its own address, with the type gnosh's
// table gives it, so that a video plays in the browser and an image
// shows; and a browser is told not to guess.
func TestServerServesDeliveredFiles(t *testing.T) {
	s := startServer(t)
	address := s.srv.address(s.video)
	if want := s.srv.base + "/" + s.video.Token + "/ad.mp4"; address != want {
		t.Fatalf("the video's address is %s, want %s", address, want)
	}
	status, header, body := fetch(t, http.MethodGet, address)
	if status != http.StatusOK || body != "0123456789 the bytes of a video" {
		t.Fatalf("GET %s: %d %q", address, status, body)
	}
	if got := header.Get("Content-Type"); got != "video/mp4" {
		t.Errorf("the video is served as %q", got)
	}
	for name, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store", "Accept-Ranges": "bytes"} {
		if got := header.Get(name); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
	if header.Get("Last-Modified") == "" {
		t.Errorf("the answer does not say when the file last changed")
	}

	status, header, body = fetch(t, http.MethodGet, s.srv.address(s.image))
	if status != http.StatusOK || header.Get("Content-Type") != "image/png" || body != pngHead+"the bytes of an image" {
		t.Errorf("the image: %d, %q, %q", status, header.Get("Content-Type"), body)
	}
	// The token alone is no address: a service worker there could claim
	// every address on the origin.
	if status, _, _ := fetch(t, http.MethodGet, s.srv.base+"/"+s.image.Token); status != http.StatusNotFound {
		t.Errorf("by its token alone: %d", status)
	}

	// HEAD: the same headers, and no body.
	status, header, body = fetch(t, http.MethodHead, address)
	if status != http.StatusOK || body != "" || header.Get("Content-Type") != "video/mp4" || header.Get("Content-Length") != "31" || header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("HEAD: %d, %q, %v", status, body, header)
	}

	// A part of the file, which is what a browser asks for when a video
	// is played or sought in.
	status, header, body = fetch(t, http.MethodGet, address, "Range", "bytes=11-13")
	if status != http.StatusPartialContent || body != "the" || header.Get("Content-Range") != "bytes 11-13/31" {
		t.Errorf("a range: %d, %q, %q", status, body, header.Get("Content-Range"))
	}
}

// The types of the kinds of file an agent hands over, by the end of the
// name; anything else is offered as a download and never guessed at.
func TestMediaTypes(t *testing.T) {
	for name, want := range map[string]string{
		"ad.mp4":       "video/mp4",
		"AD.MP4":       "video/mp4",
		"clip.webm":    "video/webm",
		"clip.mov":     "video/quicktime",
		"shot.png":     "image/png",
		"photo.JPG":    "image/jpeg",
		"photo.jpeg":   "image/jpeg",
		"anim.gif":     "image/gif",
		"logo.svg":     "image/svg+xml",
		"voice.mp3":    "audio/mpeg",
		"report.pdf":   "application/pdf",
		"index.html":   "text/html; charset=utf-8",
		"notes.md":     "text/plain; charset=utf-8",
		"data.csv":     "text/plain; charset=utf-8",
		"data.json":    "application/json",
		"bundle.zip":   "application/zip",
		"program":      "application/octet-stream",
		"archive.xyz":  "application/octet-stream",
		"script.ts":    "application/octet-stream",
		".mp4":         "video/mp4",
		"a.tar.gz":     "application/gzip",
		"with space.m": "application/octet-stream",
	} {
		if got := mediaType(name); got != want {
			t.Errorf("mediaType(%q) = %q, want %q", name, got, want)
		}
	}
}

// Nothing is served that was not delivered: a token that is not in the
// register is not found, a path in the request reaches nothing, and there
// is no listing. Every answer says the same and tells a browser not to
// guess.
func TestServerServesNothingElse(t *testing.T) {
	s := startServer(t)
	// A file that lies beside a delivered one, and one above it.
	secret := filepath.Join(filepath.Dir(s.video.File), "secret.txt")
	if err := os.WriteFile(secret, []byte("not delivered"), 0o644); err != nil {
		t.Fatal(err)
	}
	token := s.video.Token
	for _, path := range []string{
		"/",
		"",
		"/nope",
		"/" + strings.Repeat("A", 43),
		"/" + token[:42],
		"/" + token + "x",
		"/" + strings.ToUpper(token),
		"/" + token + "/secret.txt",
		"/" + token + "/../secret.txt",
		"/" + token + "/ad.mp4/..",
		"/" + token + "/ad.mp4/extra",
		"/" + token + "/..%2Fsecret.txt",
		"/" + token + "/%2e%2e/secret.txt",
		"/" + token + "//ad.mp4",
		"//" + token + "/ad.mp4",
		"/../" + token + "/ad.mp4",
		"/" + token + "/shot.png",
		secret,
		"/etc/passwd",
		"/" + token + "/etc/passwd",
		"/" + token + "/" + s.video.File,
		"/.gnosh/deliverables/register.json",
		"/files/",
	} {
		status, header, body := fetch(t, http.MethodGet, s.srv.base+path)
		if status != http.StatusNotFound {
			t.Errorf("GET %q: %d %q, want it not found", path, status, body)
		}
		if strings.Contains(body, "not delivered") || strings.Contains(body, "the bytes of") || strings.Contains(body, token) {
			t.Errorf("GET %q answered with %q", path, body)
		}
		if header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %q: the answer does not say nosniff", path)
		}
	}

	// Only reading is answered.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions, "PROPFIND"} {
		status, header, body := fetch(t, method, s.srv.address(s.video))
		if status != http.StatusMethodNotAllowed || header.Get("Allow") != "GET, HEAD" || strings.Contains(body, "the bytes of") {
			t.Errorf("%s: %d, Allow %q, %q", method, status, header.Get("Allow"), body)
		}
	}
	if data, err := os.ReadFile(s.video.File); err != nil || string(data) != "0123456789 the bytes of a video" {
		t.Errorf("the delivered file was changed: %q, %v", data, err)
	}
}

// A file that is gone, that has become a directory, or whose path has
// become a link since it was delivered is not served.
func TestServerServesTheFileThatWasDelivered(t *testing.T) {
	s := startServer(t)
	address := s.srv.address(s.video)
	other := file(t, "other.txt", "something else on this machine")

	if err := os.Remove(s.video.File); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := fetch(t, http.MethodGet, address); status != http.StatusNotFound {
		t.Errorf("a file that is gone: %d", status)
	}
	if err := os.Symlink(other, s.video.File); err != nil {
		t.Fatal(err)
	}
	if status, _, body := fetch(t, http.MethodGet, address); status != http.StatusNotFound || strings.Contains(body, "something else") {
		t.Errorf("a path that has become a link: %d %q", status, body)
	}
	if err := os.Remove(s.video.File); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.video.File, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.video.File, "inside.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status, _, body := fetch(t, http.MethodGet, address); status != http.StatusNotFound || strings.Contains(body, "inside") {
		t.Errorf("a path that has become a directory: %d %q", status, body)
	}

	// A newer version written to the same path is what the address
	// serves from then on.
	if err := os.RemoveAll(s.video.File); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.video.File, []byte("the second cut"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status, _, body := fetch(t, http.MethodGet, address); status != http.StatusOK || body != "the second cut" {
		t.Errorf("a newer version at the same path: %d %q", status, body)
	}
}

// The server listens on this machine only, whatever it is asked for.
func TestServerListensOnThisMachineOnly(t *testing.T) {
	s := startServer(t)
	addr, ok := s.srv.ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		t.Fatalf("the server listens on %v", s.srv.ln.Addr())
	}
	if !strings.HasPrefix(s.srv.base, "http://127.0.0.1:") {
		t.Errorf("the server's address is %s", s.srv.base)
	}
	for _, addr := range []string{"0.0.0.0:0", ":0"} {
		srv, err := serve(s.reg, addr)
		if err == nil {
			srv.close()
			t.Errorf("a server was started on %s, which other machines can reach", addr)
			continue
		}
		if !strings.Contains(err.Error(), "served to this machine only") {
			t.Errorf("serving on %s: %v", addr, err)
		}
	}
	if _, err := serve(s.reg, "no-port"); err == nil {
		t.Errorf("an address with no port was served on")
	}
}

// When the port is taken, a free one is used, and the server says so.
func TestServerTakesAFreePortWhenItsOwnIsTaken(t *testing.T) {
	s := startServer(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	other, err := serve(s.reg, taken.Addr().String())
	if err != nil {
		t.Fatalf("with its port taken the server did not start: %v", err)
	}
	defer other.close()
	if !other.moved || other.asked != taken.Addr().String() || other.base == "http://"+taken.Addr().String() {
		t.Errorf("the server is at %s (moved %v), and was asked for %s", other.base, other.moved, other.asked)
	}
	if status, _, body := fetch(t, http.MethodGet, other.address(s.video)); status != http.StatusOK || !strings.Contains(body, "the bytes of a video") {
		t.Errorf("on the port it took: %d %q", status, body)
	}
	if s.srv.moved {
		t.Errorf("a server that got the port it asked for says it moved")
	}
}

// A second server, which is what a later session of the project starts,
// reads the register back and serves the addresses the first one handed
// out; and a file delivered by another session while it runs is served
// without a restart.
func TestServerReadsTheRegisterBack(t *testing.T) {
	s := startServer(t)
	later, err := serve(newRegister(s.reg.dir, s.reg.key, nil, nil), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer later.close()
	for _, rec := range []record{s.video, s.image} {
		address := later.base + "/" + rec.Token + "/" + rec.Name
		if status, _, _ := fetch(t, http.MethodGet, address); status != http.StatusOK {
			t.Errorf("the later server does not serve %s: %d", address, status)
		}
	}
	page := file(t, "index.html", "<p>hello</p>")
	added, err := s.reg.add(t.Context(), "", page, page, "page")
	if err != nil {
		t.Fatal(err)
	}
	status, header, body := fetch(t, http.MethodGet, later.address(added))
	if status != http.StatusOK || body != "<p>hello</p>" || header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("a file delivered while the later server runs: %d %q %q", status, body, header.Get("Content-Type"))
	}

	// A server whose account holds another key serves none of it.
	stranger, err := serve(newRegister(s.reg.dir, []byte(strings.Repeat("k", 32)), nil, nil), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stranger.close()
	if status, _, _ := fetch(t, http.MethodGet, stranger.base+"/"+s.video.Token+"/ad.mp4"); status != http.StatusNotFound {
		t.Errorf("a server with another key served the file: %d", status)
	}
}

// The server shares its origin with every service of this machine's
// loopback: a page or a picture that can run a script is served
// sandboxed, a service worker is never served, and a request that names
// another host (a page of another site that rebinds its name to this
// machine) is not answered (the deliverables review of 2026-11-19).
func TestServerKeepsItsOriginSafe(t *testing.T) {
	s := startServer(t)
	page := file(t, "report.html", "<script>fetch('http://127.0.0.1:9/')</script>")
	rec, err := s.reg.add(t.Context(), "", page, page, "the report")
	if err != nil {
		t.Fatal(err)
	}
	_, header, _ := fetch(t, http.MethodGet, s.srv.address(rec))
	if got := header.Get("Content-Security-Policy"); !strings.HasPrefix(got, "sandbox allow-scripts") || strings.Contains(got, "allow-same-origin") {
		t.Errorf("the page's policy: %q", got)
	}
	if _, header, _ := fetch(t, http.MethodGet, s.srv.address(s.image)); header.Get("Content-Security-Policy") != "" {
		t.Errorf("a PNG was given a policy: %q", header.Get("Content-Security-Policy"))
	}
	if status, _, _ := fetch(t, http.MethodGet, s.srv.address(rec), "Service-Worker", "script"); status != http.StatusForbidden {
		t.Errorf("a service worker's fetch: %d", status)
	}
	host := func(h string) int {
		req, err := http.NewRequest(http.MethodGet, s.srv.address(s.image), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = h
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for h, want := range map[string]int{"evil.example": http.StatusMisdirectedRequest, "evil.example:7777": http.StatusMisdirectedRequest,
		"localhost:9999": http.StatusOK, "127.0.0.1:7777": http.StatusOK, "[::1]:7777": http.StatusOK, "127.0.0.2": http.StatusOK} {
		if got := host(h); got != want {
			t.Errorf("Host %s: %d, want %d", h, got, want)
		}
	}
}

// A delivered file whose folder has since become a link is not served:
// what was delivered is the file as its links were then (the deliverables
// review of 2026-11-19).
func TestServerRefusesAPathThatLeadsElsewhereNow(t *testing.T) {
	s := startServer(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(page, []byte("the report"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := s.reg.add(t.Context(), "", page, page, "the report")
	if err != nil {
		t.Fatal(err)
	}
	if status, _, body := fetch(t, http.MethodGet, s.srv.address(rec)); status != http.StatusOK || body != "the report" {
		t.Fatalf("before: %d %q", status, body)
	}
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "report.txt"), []byte("not the report"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, dir); err != nil {
		t.Skip("no links here:", err)
	}
	if status, _, body := fetch(t, http.MethodGet, s.srv.address(rec)); status != http.StatusNotFound || strings.Contains(body, "not the report") {
		t.Errorf("after its folder became a link: %d %q", status, body)
	}
}

// The server holds a fixed number of connections at once: one more waits
// until one closes, and is then answered.
func TestServerHoldsSoManyConnections(t *testing.T) {
	defer func(n int) { maxConns = n }(maxConns)
	maxConns = 2
	s := startServer(t)
	addr := strings.TrimPrefix(s.srv.base, "http://")
	var held []net.Conn
	for range 2 {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	got := make(chan int, 1)
	go func() {
		status, _, _ := fetch(t, http.MethodGet, s.srv.address(s.image))
		got <- status
	}()
	select {
	case status := <-got:
		t.Fatalf("a third connection was answered (%d) while two were held", status)
	case <-time.After(300 * time.Millisecond):
	}
	held[0].Close()
	select {
	case status := <-got:
		if status != http.StatusOK {
			t.Errorf("once a slot was free: %d", status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting connection was never answered")
	}
	held[1].Close()
}
