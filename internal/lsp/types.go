package lsp

import "encoding/json"

// The protocol's types that gnosh uses, with the protocol's own field
// names.

// Position is a place in a document: a line and a UTF-16 code unit within
// it, both from zero.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a stretch of a document, its end not included.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range in a document named by its URI.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// locationLink is the other shape a definition may come in.
type locationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// Severity is how bad a diagnostic is.
type Severity int

// The severities.
const (
	SeverityError       Severity = 1
	SeverityWarning     Severity = 2
	SeverityInformation Severity = 3
	SeverityHint        Severity = 4
)

// String is the severity's word.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInformation:
		return "info"
	case SeverityHint:
		return "hint"
	}
	return "error"
}

// Diagnostic is one problem a server found in a document.
type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity Severity        `json:"severity,omitempty"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

// Symbol is a symbol of a document or the workspace, in the one shape
// both kinds of answer are turned into. Children are a document symbol's
// own (a type's methods).
type Symbol struct {
	Name      string   `json:"name"`
	Detail    string   `json:"detail,omitempty"`
	Kind      int      `json:"kind"`
	Location  Location `json:"location"`
	Container string   `json:"containerName,omitempty"`
	Children  []Symbol `json:"children,omitempty"`
}

// documentSymbol is the hierarchical shape of textDocument/documentSymbol.
type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []documentSymbol `json:"children"`
}

// symbolInformation is the flat shape of documentSymbol and
// workspace/symbol. (A workspace symbol may also come with a location of
// a URI alone, which decodes here with a zero range.)
type symbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName"`
}

// CallItem is one end of a call: a function and where it is.
type CallItem struct {
	Name           string          `json:"name"`
	Kind           int             `json:"kind"`
	Detail         string          `json:"detail,omitempty"`
	URI            string          `json:"uri"`
	Range          Range           `json:"range"`
	SelectionRange Range           `json:"selectionRange"`
	Data           json.RawMessage `json:"data,omitempty"`
}

// Call is a call between two functions: From calls To, at Ranges (in
// From's document for an incoming call, in the item's own for an
// outgoing one).
type Call struct {
	Item   CallItem
	Ranges []Range
}

// SymbolKind names a symbol kind's number, as the protocol numbers them.
func SymbolKind(k int) string {
	kinds := []string{"", "file", "module", "namespace", "package", "class", "method", "property", "field",
		"constructor", "enum", "interface", "function", "variable", "constant", "string", "number", "boolean",
		"array", "object", "key", "null", "enum member", "struct", "event", "operator", "type parameter"}
	if k > 0 && k < len(kinds) {
		return kinds[k]
	}
	return "symbol"
}
