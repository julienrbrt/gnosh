package lsp

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Synced is what Sync did: the count of publishes before it and the
// version of the file it sent, for Settle, and whether anything was sent.
type Synced struct {
	Since   uint64
	Version int
	Sent    bool
}

// Sync tells the server what the file at path holds now: it opens it the
// first time, and after that sends its whole text as a change (and a
// save, to a server that asked for saves) when the text is not what the
// server was last told.
func (c *Client) Sync(ctx context.Context, path, languageID, text string) (out Synced, err error) {
	uri := FileURI(path)
	c.mu.Lock()
	out.Since = c.seq
	d := c.docs[uri]
	c.mu.Unlock()
	switch {
	case d == nil:
		err = c.notify(ctx, "textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{"uri": uri, "languageId": languageID, "version": 1, "text": text}})
		if err == nil {
			c.mu.Lock()
			c.docs[uri] = &document{version: 1, text: text}
			c.mu.Unlock()
		}
		out.Sent, out.Version = err == nil, 1
		return out, err
	case d.text == text:
		return out, nil
	}
	c.mu.Lock()
	version := d.version + 1
	c.mu.Unlock()
	err = c.notify(ctx, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []map[string]string{{"text": text}},
	})
	if err != nil {
		return out, err
	}
	c.mu.Lock()
	d.version, d.text = version, text
	c.mu.Unlock()
	out.Sent, out.Version = true, version
	if save, withText := c.saves(); save {
		p := map[string]any{"textDocument": map[string]string{"uri": uri}}
		if withText {
			p["text"] = text
		}
		err = c.notify(ctx, "textDocument/didSave", p)
	}
	return out, err
}

// Open is the paths of the files the server has been told of.
func (c *Client) Open() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.docs))
	for uri := range c.docs {
		if p := URIPath(uri); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// saves reports whether the server asked to be told of saves, and to be
// sent the text with them (textDocumentSync.save).
func (c *Client) saves() (save, withText bool) {
	c.mu.Lock()
	raw := c.caps["textDocumentSync"]
	c.mu.Unlock()
	var sync struct {
		Save json.RawMessage `json:"save"`
	}
	if json.Unmarshal(raw, &sync) != nil || len(sync.Save) == 0 {
		return false, false
	}
	s := strings.TrimSpace(string(sync.Save))
	if s == "true" {
		return true, false
	}
	var opts struct {
		IncludeText bool `json:"includeText"`
	}
	if json.Unmarshal(sync.Save, &opts) != nil {
		return false, false
	}
	return true, opts.IncludeText
}

// Forget forgets a file, telling the server so.
func (c *Client) Forget(ctx context.Context, path string) error {
	uri := FileURI(path)
	c.mu.Lock()
	_, open := c.docs[uri]
	delete(c.docs, uri)
	c.mu.Unlock()
	if !open {
		return nil
	}
	return c.notify(ctx, "textDocument/didClose", map[string]any{"textDocument": map[string]string{"uri": uri}})
}

// Settle waits until the server has published the diagnostics of the
// file at path for what Sync sent (s: a publish after it, and of its
// version where the server says the version), and then for a moment of
// quiet with no more publishes of any file, as a change in one file shows
// up in others; or until ctx ends. It reports whether the file's
// diagnostics came. (A publish of an older version, late, is not taken
// for the new one's.)
func (c *Client) Settle(ctx context.Context, path string, s Synced, quiet time.Duration) bool {
	uri := FileURI(path)
	for {
		c.mu.Lock()
		p := c.diags[uri]
		got := p.seq > s.Since && (p.version < 0 || p.version >= s.Version)
		changed := c.changed
		c.mu.Unlock()
		if got {
			break
		}
		select {
		case <-changed:
		case <-c.done:
			return false
		case <-ctx.Done():
			return false
		}
	}
	for {
		c.mu.Lock()
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-time.After(quiet):
			return true
		case <-c.done:
			return true
		case <-ctx.Done():
			return true
		}
	}
}

// Diagnostics is every file's diagnostics as the server last published
// them, by path; files with none are left out.
func (c *Client) Diagnostics() map[string][]Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string][]Diagnostic{}
	for uri, p := range c.diags {
		if path := URIPath(uri); path != "" && len(p.diags) > 0 {
			out[path] = append([]Diagnostic(nil), p.diags...)
		}
	}
	return out
}

// Text is what the server was last told the file at path holds, and
// whether it was told of it.
func (c *Client) Text(path string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.docs[FileURI(path)]
	if d == nil {
		return "", false
	}
	return d.text, true
}
