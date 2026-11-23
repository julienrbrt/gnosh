package agentdef

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ScopeCommandLine is the scope of a definition given with --agents.
const ScopeCommandLine = "command line"

// FromJSON reads the definitions of --agents: a JSON object whose keys are
// the agents' names, each holding the settings a definition file's
// frontmatter has (description, tools, disallowedTools, model,
// permissionMode, maxTurns, omitClaudeMd, background, hooks, skills,
// effort, mcpServers, memory, autoCompactWindow and the rest), with prompt
// for the file's body. color and
// experimental are ignored, as Claude Code ignores them, and any other key
// is a mistake. Each definition comes back with the scope "command line"
// and the source "--agents", in the order of their names. An error says
// what is wrong, and with which agent.
func FromJSON(text string) ([]Def, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &all); err != nil || all == nil {
		return nil, fmt.Errorf(`it is not a JSON object of agents by name, such as {"reviewer": {"description": "Reviews a change", "prompt": "You review..."}}`)
	}
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	slices.Sort(names)
	var out []Def
	for _, name := range names {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(all[name], &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("the agent %q is not a JSON object of its settings", name)
		}
		d := Def{Name: name, Scope: ScopeCommandLine, Source: "--agents"}
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, key := range keys {
			val := fields[key]
			bad := func(want string) error {
				return fmt.Errorf("the agent %q has %s set to %s, which should be %s", name, key, clip(string(val), 60), want)
			}
			switch key {
			case "description", "prompt", "model", "permissionMode", "effort", "initialPrompt", "isolation":
				var s string
				if json.Unmarshal(val, &s) != nil {
					return nil, bad("a string")
				}
				switch key {
				case "description":
					d.Description = strings.TrimSpace(s)
				case "prompt":
					d.Prompt = strings.TrimSpace(s)
				case "model":
					d.Model = strings.TrimSpace(s)
				case "effort":
					d.Effort = strings.TrimSpace(s)
				case "initialPrompt":
					d.InitialPrompt = strings.TrimSpace(s)
				case "isolation":
					d.Isolation = strings.TrimSpace(s)
				default:
					d.PermissionMode = strings.TrimSpace(s)
				}
			case "skills":
				list, ok := jsonList(val)
				if !ok {
					return nil, bad("a list of skill names, or one string of them with commas")
				}
				d.Skills = cleanNames(list)
			case "tools", "disallowedTools":
				list, ok := jsonList(val)
				if !ok {
					return nil, bad("a list of tool names, or one string of them with commas")
				}
				if key == "tools" {
					d.Tools, d.ToolsSet, d.AgentTypes = toolNames(list), true, agentTypes(list)
				} else {
					d.Disallowed = toolNames(list)
				}
			case "maxTurns":
				var n int
				if json.Unmarshal(val, &n) != nil || n < 1 {
					return nil, bad("a number of turns, 1 or more")
				}
				d.MaxTurns = n
			case "omitClaudeMd", "background":
				var b bool
				if json.Unmarshal(val, &b) != nil {
					return nil, bad("true or false")
				}
				if key == "omitClaudeMd" {
					d.OmitInstructions = b
				} else {
					d.Background = b
				}
			case "hooks":
				var events map[string]json.RawMessage
				if json.Unmarshal(val, &events) != nil {
					return nil, bad("a map of events to their hooks")
				}
				if len(events) > 0 {
					d.Hooks = val
				}
			case "mcpServers":
				servers, why := mcpServers(val)
				if why != "" {
					return nil, fmt.Errorf("the agent %q's mcpServers %s", name, why)
				}
				d.MCPServers = servers
			case "memory":
				var s string
				if string(val) != "null" && json.Unmarshal(val, &s) != nil {
					return nil, bad("user, project or local")
				}
				if why := memoryScope(s); why != "" {
					return nil, fmt.Errorf("the agent %q: %s", name, why)
				}
				d.Memory = s
			case "autoCompactWindow":
				d.compactWindow(strings.TrimSpace(string(val)))
			case "color", "experimental":
			default:
				return nil, fmt.Errorf("the agent %q has %q, which is not a setting of an agent", name, key)
			}
		}
		if why := d.problem(); why != "" {
			return nil, fmt.Errorf("%s", why)
		}
		if d.Model == "inherit" {
			d.Model, d.InheritModel = "", true
		}
		out = append(out, d)
	}
	return out, nil
}

// jsonList reads a JSON list of strings, or one string that is a list
// with commas.
func jsonList(raw json.RawMessage) ([]string, bool) {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return field{scalar: s}.list(), true
	}
	return nil, false
}

// Over returns a set in which defs come before s's own: a definition of
// defs is used in place of one of s with the same name, as --agents is
// over the files and gnosh's own.
func Over(s *Set, defs []Def) *Set {
	if len(defs) == 0 {
		return s
	}
	out := &Set{skipped: slices.Clone(s.skipped)}
	have := map[string]bool{}
	for _, d := range defs {
		out.defs = append(out.defs, d)
		have[d.Name] = true
	}
	for _, d := range s.defs {
		if !have[d.Name] {
			out.defs = append(out.defs, d)
		}
	}
	return out
}
