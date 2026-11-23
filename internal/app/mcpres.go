package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/mcp"
)

// This file is the two tools that read what MCP servers offer beside their
// tools (spec/cc/mcp.md 5): ListMcpResourcesTool lists the resources of
// the connected servers that have some, and ReadMcpResourceTool reads one
// by its URI. They are offered once a connected server says it has
// resources. They never ask: they only read from a server the user set
// up, and the permission rules can still switch either off by name.

// The names of the two tools, as Claude Code's.
const (
	listResourcesName = "ListMcpResourcesTool"
	readResourceName  = "ReadMcpResourceTool"
)

// mcpResourcesMost is the most resources one listing names.
const mcpResourcesMost = 200

// listResourcesTool is ListMcpResourcesTool.
type listResourcesTool struct{ l *live }

// readResourceTool is ReadMcpResourceTool.
type readResourceTool struct{ l *live }

// resourceTools are the two tools, for a session where a connected server
// has resources.
func (l *live) resourceTools() []core.Tool {
	return []core.Tool{&listResourcesTool{l: l}, &readResourceTool{l: l}}
}

// Spec says what the model is told about the tool.
func (t *listResourcesTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name: listResourcesName,
		Description: "Lists the resources the connected MCP servers offer: data a server makes readable by a URI, such as documents, records or files. " +
			"Each line gives the server, the URI, the resource's name and what it is. Read one with " + readResourceName + ".",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"server":{"type":"string","description":"The name of the one MCP server whose resources to list. Leave it out for every server's."}},"additionalProperties":false}`),
	}
}

// Parallel says a listing can run beside other calls.
func (t *listResourcesTool) Parallel(core.ToolCall) bool { return true }

// Run lists the resources.
func (t *listResourcesTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	var in struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return core.ErrorResult(fmt.Sprintf("The arguments cannot be read (%v).", err)), nil
	}
	name := strings.TrimSpace(in.Server)
	servers, offering := t.l.resourceServers(name)
	if len(servers) == 0 {
		if name != "" {
			return core.ErrorResult(fmt.Sprintf("No connected MCP server named %q offers resources. Those that do: %s.", name, firstOf(strings.Join(offering, ", "), "none"))), nil
		}
		return core.ErrorResult("No connected MCP server offers resources."), nil
	}
	var b strings.Builder
	shown, left := 0, 0
	for _, s := range servers {
		list, err := s.client.ListResources(ctx)
		switch {
		case err != nil:
			fmt.Fprintf(&b, "%s: its resources could not be listed (%v)\n", s.name, err)
			continue
		case len(list) == 0:
			fmt.Fprintf(&b, "%s: no resources\n", s.name)
			continue
		}
		for _, r := range list {
			if shown >= mcpResourcesMost {
				left++
				continue
			}
			shown++
			line := s.name + "  " + r.URI
			if what := firstOf(r.Title, r.Name); what != "" {
				line += "  " + oneLine(what, 120)
			}
			if r.MimeType != "" {
				line += " (" + r.MimeType + ")"
			}
			if r.Description != "" {
				line += ": " + oneLine(r.Description, 200)
			}
			b.WriteString(line + "\n")
		}
	}
	if left > 0 {
		fmt.Fprintf(&b, "(%d more are not listed here; list one server's at a time to see them.)\n", left)
	}
	return core.TextResult(strings.TrimRight(b.String(), "\n")), nil
}

// Spec says what the model is told about the tool.
func (t *readResourceTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        readResourceName,
		Description: "Reads one resource of a connected MCP server by its URI, as " + listResourcesName + " lists it, and returns what it holds.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"server":{"type":"string","description":"The name of the MCP server the resource is on."},"uri":{"type":"string","description":"The resource's URI."}},"required":["server","uri"],"additionalProperties":false}`),
	}
}

// Parallel says a read can run beside other calls.
func (t *readResourceTool) Parallel(core.ToolCall) bool { return true }

// Run reads the resource.
func (t *readResourceTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	var in struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return core.ErrorResult(fmt.Sprintf("The arguments cannot be read (%v).", err)), nil
	}
	name, uri := strings.TrimSpace(in.Server), strings.TrimSpace(in.URI)
	if name == "" || uri == "" {
		return core.ErrorResult("Give both the server and the uri of the resource, as " + listResourcesName + " lists them."), nil
	}
	servers, offering := t.l.resourceServers(name)
	if len(servers) == 0 {
		return core.ErrorResult(fmt.Sprintf("No connected MCP server named %q offers resources. Those that do: %s.", name, firstOf(strings.Join(offering, ", "), "none"))), nil
	}
	parts, err := servers[0].client.ReadResource(ctx, uri)
	if err != nil {
		return core.ErrorResult(fmt.Sprintf("The resource %s of %s could not be read: %v.", uri, name, err)), nil
	}
	return core.TextResult(resourceText(parts, uri)), nil
}

// resourceText is what a resource holds, as text: its text parts, and a
// line for each part that is binary.
func resourceText(parts []mcp.ResourceContents, uri string) string {
	var out []string
	for _, p := range parts {
		switch {
		case p.Text != "":
			out = append(out, p.Text)
		case p.Blob != "":
			out = append(out, fmt.Sprintf("[%s holds binary data (%s, about %s), which is not shown here.]",
				firstOf(p.URI, uri), firstOf(p.MimeType, "of a type it does not name"), ByteCount(int64(len(p.Blob)*3/4))))
		}
	}
	if len(out) == 0 {
		return "(The resource is empty.)"
	}
	return strings.Join(out, "\n\n")
}

// resourceServer is a connected server that offers resources.
type resourceServer struct {
	name   string
	client *mcp.Client
}

// resourceServers returns the connected servers that offer resources, or
// the one of them named name; and the names of all that offer some.
func (l *live) resourceServers(name string) (found []resourceServer, offering []string) {
	if l.mcp == nil {
		return nil, nil
	}
	l.mcp.mu.Lock()
	defer l.mcp.mu.Unlock()
	for _, s := range l.mcp.servers {
		if s.client == nil || !s.client.Info().Resources {
			continue
		}
		offering = append(offering, s.cfg.Name)
		if name == "" || s.cfg.Name == name {
			found = append(found, resourceServer{name: s.cfg.Name, client: s.client})
		}
	}
	slices.Sort(offering)
	return found, offering
}
