// Package lang maps file names to highlight.js language names. It is the
// single source of truth for both the UI (syntax highlighting) and the
// REVIEW.md export (code fence info strings).
package lang

import (
	"path"
	"strings"
)

var byExt = map[string]string{
	"go": "go", "pl": "perl", "pm": "perl", "cgi": "perl", "t": "perl",
	"py": "python", "rb": "ruby", "php": "php", "java": "java", "kt": "kotlin", "kts": "kotlin",
	"js": "javascript", "mjs": "javascript", "cjs": "javascript", "jsx": "javascript",
	"ts": "typescript", "tsx": "typescript", "json": "json",
	"html": "xml", "htm": "xml", "xml": "xml", "svg": "xml", "vue": "xml",
	"css": "css", "scss": "scss", "less": "less",
	"c": "c", "h": "c", "cpp": "cpp", "cc": "cpp", "hpp": "cpp", "cs": "csharp", "rs": "rust", "swift": "swift",
	"sh": "bash", "bash": "bash", "zsh": "bash", "ksh": "bash", "env": "bash",
	"sql": "sql", "yml": "yaml", "yaml": "yaml", "md": "markdown", "lua": "lua", "r": "r",
	"ini": "ini", "conf": "ini", "cfg": "ini", "toml": "ini", "properties": "ini",
	"mk": "makefile", "diff": "diff", "patch": "diff", "graphql": "graphql",
}

// ForPath returns the highlight.js language for a repository path, or "".
func ForPath(p string) string {
	name := path.Base(p)
	if name == "Makefile" || name == "GNUmakefile" || name == "makefile" {
		return "makefile"
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	return byExt[ext]
}
