package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/tools/webtool"
)

// This file is the WebFetch tool (spec/cc/tools.md, section 10): a page
// from the web, read by a separate request to the small model with the
// model's request about it, so that the model reads an answer and not the
// raw page. What is not text (a picture, a PDF, an archive) is kept in a
// file of the session's, as Claude Code keeps it, and the model is told
// where. The fetching itself, and what is refused, is webtool's.

const webFetchName = "WebFetch"

// webFetchMaxChars is how much of a page one call reads; the rest is read
// with offset.
const webFetchMaxChars = 100_000

const webFetchDescription = `Fetches a page from the web and answers a request about it.

The page is turned into Markdown, and a separate, smaller model reads it with your request and answers; you get that answer, not the raw page, so say exactly what you need from it (a command, a function's signature, the steps of a procedure, whether it says something). The answer may leave out what you did not ask for.

- url: http or https, on the open web. http is upgraded to https. A host on this machine or on a private network (localhost, a name without a dot, 192.168.x.x and the like) is refused: use curl through Bash for those.
- A redirect to another host is not followed: the result says where it leads, and you can fetch that address yourself.
- At most 100,000 characters of a page are read in one call; the result says how much is left, and offset reads on from there.
- A page is kept for a while, so asking again about the same page soon fetches nothing new.
- What is not text (a picture, a PDF, an archive) is saved to a file, and the result says where: Read shows a picture.
- Each fetch asks the user first, unless they have allowed the site.`

var webFetchSchema = json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"The page's address: http or https, on the open web."},"prompt":{"type":"string","description":"What you need from the page."},"offset":{"type":"integer","minimum":0,"description":"How many characters of the page to skip, to read on in a long page."}},"required":["url","prompt"],"additionalProperties":false}`)

// webFetchSystem is what the small model is told when it reads a page.
const webFetchSystem = `You read one web page for another model and answer its request from what the page says, and from nothing else. Where exact text matters (code, commands, names, numbers, quotations), give it exactly as the page has it. If the page does not say what was asked, say so plainly, and say what it is about instead. Be brief: the answer goes to a model that will act on it.`

// fetchPage fetches a page. Tests put a page of their own in its place.
var fetchPage = webtool.Fetch

type webFetchInput struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt"`
	Offset int    `json:"offset"`
}

// webFetchTool is the tool of one session. cacheFor is how long a page is
// kept (CLAUDE_CODE_WEBFETCH_CACHE_TTL_MS, 15 minutes) and deadline how
// long one may take to download (CLAUDE_CODE_WEBFETCH_DEADLINE_MS; 0 is
// no limit).
type webFetchTool struct {
	l                  *live
	cacheFor, deadline time.Duration

	mu    sync.Mutex
	cache map[string]cachedPage
}

type cachedPage struct {
	page webtool.Page
	at   time.Time
	// binary says the page is not text; saved is the file it was kept in,
	// or "" when it was too long to keep whole. Its bytes are not held
	// here.
	binary bool
	saved  string
}

func (t *webFetchTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: webFetchName, Description: webFetchDescription, InputSchema: webFetchSchema}
}

// read reads a call's arguments and says, in words for the model, what is
// wrong with them when something is.
func (t *webFetchTool) read(input json.RawMessage) (webFetchInput, string) {
	var in webFetchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return in, fmt.Sprintf("the arguments cannot be read (%v): url and prompt are strings, offset a number", err)
	}
	in.URL, in.Prompt = strings.TrimSpace(in.URL), strings.TrimSpace(in.Prompt)
	switch {
	case in.URL == "":
		return in, "url is empty"
	case in.Prompt == "":
		return in, "prompt is empty: say what you need from the page"
	case in.Offset < 0:
		return in, "offset is below zero"
	}
	return in, ""
}

// Describe says what a call would do: fetch from a host. A URL that would
// be refused is said to be one now, before anybody is asked about it.
func (t *webFetchTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	in, wrong := t.read(call.Input)
	act := core.Action{Kind: core.ActionNetwork, URL: in.URL, Summary: "Fetch a web page"}
	if wrong != "" {
		return act, errors.New(wrong)
	}
	u, err := webtool.Check(in.URL, false)
	if err != nil {
		return act, err
	}
	act.URL, act.Summary = u.String(), "Fetch "+u.Hostname()
	return act, nil
}

// Run fetches the page, or takes it from the cache, and has the small model
// answer the request about it.
func (t *webFetchTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	in, wrong := t.read(call.Input)
	if wrong != "" {
		return core.ErrorResult("Nothing was fetched: " + wrong + "."), nil
	}
	u, err := webtool.Check(in.URL, false)
	if err != nil {
		return core.ErrorResult("Nothing was fetched: " + err.Error() + "."), nil
	}
	key := u.String()
	c, cached := t.cached(key)
	if !cached {
		timeout := t.deadline
		if timeout <= 0 {
			timeout = -1 // (no bound)
		}
		page, err := fetchPage(ctx, key, webtool.Options{UserAgent: "gnosh/" + firstOf(t.l.p.o.BuildID, "devel"), Timeout: timeout})
		if err != nil {
			return core.ErrorResult(fmt.Sprintf("%s was not fetched: %v.", key, err)), nil
		}
		c = cachedPage{page: page, binary: page.Body != nil}
		if c.binary && page.Code < 400 && page.Redirect == "" && !page.Cut {
			if c.saved, err = t.save(page); err != nil {
				return core.ErrorResult(fmt.Sprintf("%s is %s, not text, and the file to keep it in could not be written: %v.", page.URL, firstOf(page.ContentType, "of no type it names"), err)), nil
			}
		}
		c.page.Body = nil
		t.keep(key, c)
	}
	page := c.page
	meta := map[string]any{"bytes": page.Bytes, "code": page.Code, "codeText": page.Status, "durationMs": page.Took.Milliseconds(), "url": page.URL}
	if page.Redirect != "" {
		res := core.TextResult(fmt.Sprintf("%s redirects to %s, which is on another host, and WebFetch does not follow that. Fetch %s itself if it is the page you want.", key, page.Redirect, page.Redirect))
		meta["redirect"] = page.Redirect
		res.Meta = meta
		return res, nil
	}
	if page.Code >= 400 {
		res := core.ErrorResult(fmt.Sprintf("%s answered %d %s. What it sent begins:\n%s", page.URL, page.Code, page.Status, clipChars(page.Text, 0, 2000)))
		res.Meta = meta
		return res, nil
	}
	if c.binary {
		meta["contentType"] = page.ContentType
		if c.saved == "" {
			res := core.ErrorResult(fmt.Sprintf("%s is %s, not text, and longer than the %s WebFetch keeps, so nothing was saved. Download it with curl through Bash if you need it.", page.URL, firstOf(page.ContentType, "of no type it names"), ByteCount(int64(page.Bytes))))
			res.Meta = meta
			return res, nil
		}
		meta["savedTo"] = c.saved
		res := core.TextResult(savedNote(page, c.saved))
		res.Meta = meta
		return res, nil
	}
	total := utf8.RuneCountInString(page.Text)
	if in.Offset >= total && total > 0 {
		return core.ErrorResult(fmt.Sprintf("The page has %d characters, so there is nothing from offset %d on.", total, in.Offset)), nil
	}
	window := clipChars(page.Text, in.Offset, webFetchMaxChars)
	read := utf8.RuneCountInString(window)
	from := ""
	if in.Offset > 0 {
		from = fmt.Sprintf(" (from character %d of %d)", in.Offset, total)
	}
	answer, err := t.l.agent.Side(ctx, agent.SideRequest{
		Purpose: "fetch",
		System:  webFetchSystem,
		Prompt:  fmt.Sprintf("The page %s%s:\n<page>\n%s\n</page>\n\nThe request: %s", page.URL, from, window, in.Prompt),
		Timeout: 5 * time.Minute,
	})
	var text string
	if err != nil || strings.TrimSpace(answer.Text) == "" {
		// The page itself, then, as far as it fits: an answer the model can
		// still use.
		why := "it answered nothing"
		if err != nil {
			why = err.Error()
		}
		text = fmt.Sprintf("The page could not be read for you by the smaller model (%s), so here is the beginning of its text%s:\n%s", why, from, clipChars(window, 0, 20_000))
	} else {
		text = strings.TrimSpace(answer.Text)
	}
	if left := total - in.Offset - read; left > 0 {
		text += fmt.Sprintf("\n[%d of the page's %d characters were read; %d are left, and WebFetch with offset %d reads on.]", read, total, left, in.Offset+read)
	}
	if page.Cut {
		text += "\n[The page was longer than what is fetched, and what is past that is not here.]"
	}
	res := core.TextResult(text)
	res.Meta = meta
	return res, nil
}

// cached is the page kept for key, if it was fetched less than cacheFor
// ago and, when it was saved to a file, the file is still there.
func (t *webFetchTool) cached(key string) (cachedPage, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.cache[key]
	if !ok || t.l.p.now().Sub(c.at) >= t.cacheFor {
		return cachedPage{}, false
	}
	if c.saved != "" {
		if _, err := os.Stat(c.saved); err != nil {
			return cachedPage{}, false
		}
	}
	return c, true
}

func (t *webFetchTool) keep(key string, c cachedPage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cache == nil {
		t.cache = map[string]cachedPage{}
	}
	now := t.l.p.now()
	for k, old := range t.cache {
		if now.Sub(old.at) >= t.cacheFor {
			delete(t.cache, k)
		}
	}
	c.at = now
	t.cache[key] = c
}

// save keeps a fetched file that is not text in the session's folder for
// tool output, where the model may read without a question (allowSpool),
// named for what it is.
func (t *webFetchTool) save(p webtool.Page) (string, error) {
	dir := t.l.spoolDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "WebFetch-*"+extensionOf(p.ContentType, p.URL))
	if err != nil {
		return "", err
	}
	_, err = f.Write(p.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// fetchedExtensions name what WebFetch saves, by its type.
var fetchedExtensions = map[string]string{
	"application/pdf": ".pdf",
	"image/png":       ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/bmp": ".bmp",
	"image/x-icon": ".ico", "image/vnd.microsoft.icon": ".ico", "image/avif": ".avif", "image/heic": ".heic", "image/tiff": ".tiff",
	"application/zip": ".zip", "application/gzip": ".gz", "application/x-gzip": ".gz", "application/x-tar": ".tar",
	"application/x-bzip2": ".bz2", "application/x-xz": ".xz", "application/zstd": ".zst", "application/x-7z-compressed": ".7z",
	"application/vnd.rar": ".rar", "application/x-rar-compressed": ".rar", "application/wasm": ".wasm", "application/java-archive": ".jar",
	"audio/mpeg": ".mp3", "audio/wave": ".wav", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/ogg": ".ogg", "application/ogg": ".ogg",
	"audio/flac": ".flac", "audio/aac": ".aac", "audio/mp4": ".m4a", "audio/midi": ".mid", "audio/aiff": ".aiff", "audio/basic": ".au",
	"video/mp4": ".mp4", "video/webm": ".webm", "video/avi": ".avi", "video/x-msvideo": ".avi", "video/quicktime": ".mov", "video/ogg": ".ogv",
	"font/woff": ".woff", "font/woff2": ".woff2", "font/ttf": ".ttf", "font/otf": ".otf", "font/collection": ".ttc",
	"application/vnd.ms-fontobject": ".eot",
}

// extensionOf is the file name ending for what was fetched: by its type,
// else the address's own ending when it is a plain one (.deb, .iso), else
// .bin.
func extensionOf(media, address string) string {
	if ext, ok := fetchedExtensions[media]; ok {
		return ext
	}
	if u, err := url.Parse(address); err == nil {
		ext := strings.ToLower(path.Ext(u.Path))
		plain := len(ext) >= 2 && len(ext) <= 8
		for _, r := range strings.TrimPrefix(ext, ".") {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				plain = false
			}
		}
		if plain {
			return ext
		}
	}
	return ".bin"
}

// savedNote tells the model what a fetched file that is not text is, where
// it was kept, and how to look into it.
func savedNote(p webtool.Page, file string) string {
	what := ByteCount(int64(p.Bytes))
	if p.ContentType != "" {
		what = p.ContentType + ", " + what
	}
	switch p.ContentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return fmt.Sprintf("%s is a picture (%s), not text, so it was saved to %s: Read that file to see it.", p.URL, what, file)
	case "application/pdf":
		return fmt.Sprintf("%s is a PDF (%s), not text, so it was saved to %s: Read that file for its text.", p.URL, what, file)
	}
	return fmt.Sprintf("%s is not text (%s), so it was saved to %s, for shell commands to work with.", p.URL, what, file)
}

// clipChars returns at most n characters of s from character from on.
func clipChars(s string, from, n int) string {
	start, i := -1, 0
	for at := range s {
		if i == from {
			start = at
		}
		if start >= 0 && i == from+n {
			return s[start:at]
		}
		i++
	}
	if start < 0 {
		return ""
	}
	return s[start:]
}
