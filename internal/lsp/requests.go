package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// The read-only questions a server is asked. Each names a file by its
// path and a place in it by a protocol Position; the file is to have been
// synced first (Sync), so that the server knows what it holds.

// at is the params of a question about a place in a file.
func at(path string, pos Position) map[string]any {
	return map[string]any{"textDocument": map[string]string{"uri": FileURI(path)}, "position": pos}
}

// Definition is where the symbol at pos is defined.
func (c *Client) Definition(ctx context.Context, path string, pos Position) ([]Location, error) {
	return c.locations(ctx, "textDocument/definition", at(path, pos))
}

// Implementation is what implements the interface, or the method, at
// pos.
func (c *Client) Implementation(ctx context.Context, path string, pos Position) ([]Location, error) {
	return c.locations(ctx, "textDocument/implementation", at(path, pos))
}

// References is every place that refers to the symbol at pos, its
// declaration among them.
func (c *Client) References(ctx context.Context, path string, pos Position) ([]Location, error) {
	p := at(path, pos)
	p["context"] = map[string]bool{"includeDeclaration": true}
	return c.locations(ctx, "textDocument/references", p)
}

// locations asks a question whose answer is a location, a list of them,
// a list of location links, or null.
func (c *Client) locations(ctx context.Context, method string, params any) ([]Location, error) {
	var raw json.RawMessage
	if err := c.call(ctx, method, params, &raw); err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '{' {
		raw = append(append([]byte{'['}, raw...), ']')
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	var out []Location
	for _, item := range items {
		var link locationLink
		if json.Unmarshal(item, &link) == nil && link.TargetURI != "" {
			out = append(out, Location{URI: link.TargetURI, Range: link.TargetSelectionRange})
			continue
		}
		var loc Location
		if json.Unmarshal(item, &loc) == nil && loc.URI != "" {
			out = append(out, loc)
		}
	}
	return out, nil
}

// Hover is what the server says of the symbol at pos (its type, its
// documentation), as text.
func (c *Client) Hover(ctx context.Context, path string, pos Position) (string, error) {
	var res struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := c.call(ctx, "textDocument/hover", at(path, pos), &res); err != nil {
		return "", err
	}
	return markedText(res.Contents), nil
}

// markedText is the text of a hover's contents: MarkupContent, a
// MarkedString (a string, or a language and a value), or a list of them.
func markedText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v struct {
		Value string `json:"value"`
	}
	if raw[0] == '{' && json.Unmarshal(raw, &v) == nil {
		return v.Value
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		var parts []string
		for _, item := range list {
			if t := markedText(item); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

// DocumentSymbols is the symbols of the file at path, a type's own inside
// it.
func (c *Client) DocumentSymbols(ctx context.Context, path string) ([]Symbol, error) {
	var raw []json.RawMessage
	p := map[string]any{"textDocument": map[string]string{"uri": FileURI(path)}}
	if err := c.call(ctx, "textDocument/documentSymbol", p, &raw); err != nil {
		return nil, err
	}
	uri := FileURI(path)
	var out []Symbol
	for _, item := range raw {
		var probe map[string]json.RawMessage
		if json.Unmarshal(item, &probe) != nil {
			continue
		}
		if _, flat := probe["location"]; flat {
			var si symbolInformation
			if json.Unmarshal(item, &si) == nil {
				out = append(out, Symbol{Name: si.Name, Kind: si.Kind, Location: si.Location, Container: si.ContainerName})
			}
			continue
		}
		var ds documentSymbol
		if json.Unmarshal(item, &ds) == nil {
			out = append(out, fromDocumentSymbol(uri, ds))
		}
	}
	return out, nil
}

// fromDocumentSymbol turns the hierarchical shape into Symbol.
func fromDocumentSymbol(uri string, ds documentSymbol) Symbol {
	s := Symbol{Name: ds.Name, Detail: ds.Detail, Kind: ds.Kind, Location: Location{URI: uri, Range: ds.SelectionRange}}
	for _, child := range ds.Children {
		s.Children = append(s.Children, fromDocumentSymbol(uri, child))
	}
	return s
}

// WorkspaceSymbols is the symbols of the whole workspace whose names
// match query, as the server matches them.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	var raw []symbolInformation
	if err := c.call(ctx, "workspace/symbol", map[string]string{"query": query}, &raw); err != nil {
		return nil, err
	}
	out := make([]Symbol, 0, len(raw))
	for _, si := range raw {
		out = append(out, Symbol{Name: si.Name, Kind: si.Kind, Location: si.Location, Container: si.ContainerName})
	}
	return out, nil
}

// IncomingCalls is the calls of the function at pos: who calls it, and
// where.
func (c *Client) IncomingCalls(ctx context.Context, path string, pos Position) ([]Call, error) {
	return c.calls(ctx, path, pos, "callHierarchy/incomingCalls", "from")
}

// OutgoingCalls is the calls the function at pos makes.
func (c *Client) OutgoingCalls(ctx context.Context, path string, pos Position) ([]Call, error) {
	return c.calls(ctx, path, pos, "callHierarchy/outgoingCalls", "to")
}

// calls prepares the call hierarchy at pos and asks method of each item it
// gives; end is the field that names the other end ("from" or "to").
func (c *Client) calls(ctx context.Context, path string, pos Position, method, end string) ([]Call, error) {
	var items []CallItem
	if err := c.call(ctx, "textDocument/prepareCallHierarchy", at(path, pos), &items); err != nil {
		return nil, err
	}
	var out []Call
	for _, item := range items {
		var raw []map[string]json.RawMessage
		if err := c.call(ctx, method, map[string]any{"item": item}, &raw); err != nil {
			return out, err
		}
		for _, r := range raw {
			var call Call
			if json.Unmarshal(r[end], &call.Item) != nil {
				continue
			}
			_ = json.Unmarshal(r["fromRanges"], &call.Ranges)
			out = append(out, call)
		}
	}
	return out, nil
}
