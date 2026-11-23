package fstools

import (
	"sort"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/tools/fstools/wild"
)

// fileTypes is Grep's table of file types: for each type name, the name
// patterns of the files that belong to it. The names are the short ones
// people already type after "rg -t", so that a model which knows those
// finds them here; the lists are this package's own. A file belongs to a
// type by its name alone, never by its content.
var fileTypes = map[string][]string{
	"asm":        {"*.asm", "*.s", "*.S"},
	"astro":      {"*.astro"},
	"awk":        {"*.awk"},
	"bazel":      {"*.bzl", "*.bazel", "BUILD", "WORKSPACE"},
	"c":          {"*.c", "*.h"},
	"clojure":    {"*.clj", "*.cljs", "*.cljc", "*.edn"},
	"cmake":      {"*.cmake", "CMakeLists.txt"},
	"config":     {"*.cfg", "*.conf", "*.config", "*.ini"},
	"cpp":        {"*.cc", "*.cpp", "*.cxx", "*.c++", "*.C", "*.h", "*.hh", "*.hpp", "*.hxx", "*.h++", "*.inl", "*.ipp", "*.tpp"},
	"crystal":    {"*.cr"},
	"cuda":       {"*.cu", "*.cuh"},
	"cs":         {"*.cs", "*.csx"},
	"csharp":     {"*.cs", "*.csx"},
	"css":        {"*.css", "*.scss", "*.sass", "*.less"},
	"csv":        {"*.csv", "*.tsv"},
	"dart":       {"*.dart"},
	"diff":       {"*.diff", "*.patch"},
	"docker":     {"*Dockerfile*", "*.dockerfile", "Containerfile", "Containerfile.*"},
	"elisp":      {"*.el"},
	"elixir":     {"*.ex", "*.exs", "*.eex", "*.heex"},
	"elm":        {"*.elm"},
	"erb":        {"*.erb"},
	"erlang":     {"*.erl", "*.hrl"},
	"fish":       {"*.fish"},
	"fortran":    {"*.f", "*.for", "*.f77", "*.f90", "*.f95", "*.f03", "*.f08"},
	"fsharp":     {"*.fs", "*.fsi", "*.fsx"},
	"gno":        {"*.gno", "gno.mod", "gnomod.toml"},
	"go":         {"*.go"},
	"gomod":      {"go.mod", "go.sum", "go.work", "go.work.sum"},
	"gradle":     {"*.gradle", "*.gradle.kts", "gradle.properties"},
	"graphql":    {"*.graphql", "*.gql"},
	"groovy":     {"*.groovy", "*.gradle"},
	"h":          {"*.h", "*.hh", "*.hpp", "*.hxx"},
	"haskell":    {"*.hs", "*.lhs", "*.cabal"},
	"html":       {"*.html", "*.htm", "*.xhtml"},
	"java":       {"*.java", "*.jsp", "*.jspx", "*.properties"},
	"js":         {"*.js", "*.jsx", "*.mjs", "*.cjs"},
	"json":       {"*.json", "*.jsonc", "*.json5"},
	"jsonl":      {"*.jsonl", "*.ndjson"},
	"julia":      {"*.jl"},
	"jupyter":    {"*.ipynb"},
	"kotlin":     {"*.kt", "*.kts"},
	"lisp":       {"*.lisp", "*.lsp", "*.cl"},
	"lock":       {"*.lock", "package-lock.json", "go.sum"},
	"log":        {"*.log"},
	"lua":        {"*.lua"},
	"make":       {"Makefile", "makefile", "GNUmakefile", "*.mk", "*.mak", "Makefile.*"},
	"markdown":   {"*.md", "*.markdown", "*.mdx", "*.mdown"},
	"md":         {"*.md", "*.markdown", "*.mdx", "*.mdown"},
	"nim":        {"*.nim", "*.nims"},
	"nix":        {"*.nix"},
	"objc":       {"*.m", "*.mm", "*.h"},
	"ocaml":      {"*.ml", "*.mli", "*.mll", "*.mly"},
	"perl":       {"*.pl", "*.pm", "*.t", "*.pod"},
	"php":        {"*.php", "*.phtml", "*.php3", "*.php4", "*.php5", "*.phps"},
	"proto":      {"*.proto"},
	"ps":         {"*.ps1", "*.psm1", "*.psd1"},
	"protobuf":   {"*.proto"},
	"py":         {"*.py", "*.pyi", "*.pyw"},
	"python":     {"*.py", "*.pyi", "*.pyw"},
	"r":          {"*.r", "*.R", "*.Rmd", "*.rmd"},
	"rb":         {"*.rb", "*.rake", "*.gemspec", "Gemfile", "Rakefile"},
	"rst":        {"*.rst"},
	"ruby":       {"*.rb", "*.rake", "*.gemspec", "Gemfile", "Rakefile"},
	"rust":       {"*.rs"},
	"scala":      {"*.scala", "*.sc", "*.sbt"},
	"sh":         {"*.sh", "*.bash", "*.zsh", "*.ksh", ".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile", ".zshenv"},
	"sol":        {"*.sol"},
	"solidity":   {"*.sol"},
	"sql":        {"*.sql"},
	"svelte":     {"*.svelte"},
	"svg":        {"*.svg"},
	"swift":      {"*.swift"},
	"tcl":        {"*.tcl"},
	"terraform":  {"*.tf", "*.tfvars", "*.hcl"},
	"tex":        {"*.tex", "*.sty", "*.cls", "*.bib", "*.ltx"},
	"tf":         {"*.tf", "*.tfvars", "*.hcl"},
	"toml":       {"*.toml"},
	"ts":         {"*.ts", "*.tsx", "*.mts", "*.cts"},
	"txt":        {"*.txt"},
	"typescript": {"*.ts", "*.tsx", "*.mts", "*.cts"},
	"vim":        {"*.vim", ".vimrc", "vimrc"},
	"vue":        {"*.vue"},
	"xml":        {"*.xml", "*.xsd", "*.xsl", "*.xslt", "*.dtd", "*.plist", "*.svg"},
	"yaml":       {"*.yaml", "*.yml"},
	"zig":        {"*.zig", "*.zon"},
}

// fileTypeAliases are other names a model is likely to write for a type.
var fileTypeAliases = map[string]string{
	"javascript": "js",
	"jsx":        "js",
	"tsx":        "ts",
	"golang":     "go",
	"rs":         "rust",
	"c++":        "cpp",
	"cxx":        "cpp",
	"cc":         "cpp",
	"kt":         "kotlin",
	"hs":         "haskell",
	"ex":         "elixir",
	"erl":        "erlang",
	"clj":        "clojure",
	"ml":         "ocaml",
	"pl":         "perl",
	"shell":      "sh",
	"bash":       "sh",
	"zsh":        "sh",
	"yml":        "yaml",
	"dockerfile": "docker",
	"makefile":   "make",
	"text":       "txt",
	"scss":       "css",
	"sass":       "css",
	"powershell": "ps",
	"less":       "css",
	"htm":        "html",
	"ini":        "config",
	"conf":       "config",
	"cfg":        "config",
	"hcl":        "terraform",
	"ipynb":      "jupyter",
	"patch":      "diff",
}

// fileType is one file type, ready to match names.
type fileType struct {
	name     string
	patterns []wild.Segment
}

// lookupFileType finds a type by the name a call gave, and reports false
// when there is none.
func lookupFileType(name string) (*fileType, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if alias, ok := fileTypeAliases[key]; ok {
		key = alias
	}
	patterns, ok := fileTypes[key]
	if !ok {
		return nil, false
	}
	ft := &fileType{name: key}
	for _, p := range patterns {
		seg, err := wild.CompileSegment(p)
		if err != nil {
			// The table is this package's own; a bad entry is a bug that
			// the tests catch. Leaving the pattern out keeps a search
			// working meanwhile.
			continue
		}
		ft.patterns = append(ft.patterns, seg)
	}
	return ft, true
}

// matches reports whether a file of this base name is of the type.
func (ft *fileType) matches(base string) bool {
	for _, p := range ft.patterns {
		if p.Match(base) {
			return true
		}
	}
	return false
}

// fileTypeNames lists every type name, sorted, for the message that
// answers an unknown one.
func fileTypeNames() []string {
	names := make([]string, 0, len(fileTypes))
	for name := range fileTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
