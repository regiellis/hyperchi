package main

import (
	"bytes"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/regiellis/hyperchi/modules/markdown"
)

// Document types. Every file in the library gets one; only the first four
// have their contents indexed.
const (
	TypeMarkdown = "markdown"
	TypeText     = "text"
	TypeConfig   = "config"
	TypeScript   = "script"
	TypeOther    = "other"
)

// Types lists the document types in the order the filter shows them.
var Types = []string{TypeMarkdown, TypeText, TypeConfig, TypeScript, TypeOther}

// Reasons a listed file's contents are not searchable.
const (
	reasonUnsupported = "unsupported type"
	reasonBinary      = "binary content"
	reasonTooLarge    = "over the size cap"
)

var extensionTypes = map[string]string{
	".md": TypeMarkdown, ".markdown": TypeMarkdown, ".mdown": TypeMarkdown,

	".txt": TypeText, ".text": TypeText, ".rst": TypeText, ".adoc": TypeText, ".csv": TypeText, ".tsv": TypeText,

	".conf": TypeConfig, ".cfg": TypeConfig, ".cnf": TypeConfig, ".ini": TypeConfig, ".toml": TypeConfig,
	".yaml": TypeConfig, ".yml": TypeConfig, ".json": TypeConfig, ".jsonc": TypeConfig, ".xml": TypeConfig,
	".properties": TypeConfig, ".hcl": TypeConfig, ".tf": TypeConfig, ".nix": TypeConfig, ".rules": TypeConfig,
	".service": TypeConfig, ".timer": TypeConfig, ".socket": TypeConfig, ".mount": TypeConfig,
	".network": TypeConfig, ".netdev": TypeConfig, ".container": TypeConfig, ".list": TypeConfig,

	".sh": TypeScript, ".bash": TypeScript, ".zsh": TypeScript, ".fish": TypeScript, ".ps1": TypeScript,
	".py": TypeScript, ".rb": TypeScript, ".pl": TypeScript, ".lua": TypeScript,
}

// configNames are well-known configuration files without an extension.
var configNames = map[string]bool{
	"dockerfile": true, "containerfile": true, "caddyfile": true, "makefile": true, "justfile": true,
	"vagrantfile": true, "procfile": true, "crontab": true, "hosts": true, "fstab": true, "exports": true,
}

// classify returns the document type for a slash-separated library path.
func classify(rel string) string {
	base := strings.ToLower(path.Base(rel))
	if configNames[base] {
		return TypeConfig
	}
	if t, ok := extensionTypes[path.Ext(base)]; ok {
		return t
	}
	return TypeOther
}

// isHidden reports whether any segment of a slash-separated path starts with
// a dot. Hidden files and directories (.git, .env, editor swap files) are
// never indexed or served.
func isHidden(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// looksBinary reports content that is not text: a NUL byte in the first 8 KiB
// or invalid UTF-8 anywhere.
func looksBinary(b []byte) bool {
	head := b
	if len(head) > 8<<10 {
		head = head[:8<<10]
	}
	return bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(b)
}

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// normalizeTag lowercases a tag and keeps letters, digits, and the characters . - _;
// spaces become dashes. It returns "" for nothing usable.
func normalizeTag(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	tag := strings.Trim(b.String(), ".-_")
	if !tagPattern.MatchString(tag) {
		return ""
	}
	return tag
}

// pathTags returns the directory names of rel as tags: runbooks/network/dns.md
// is tagged runbooks and network.
func pathTags(rel string) []string {
	dir := path.Dir(rel)
	if dir == "." {
		return nil
	}
	var tags []string
	for _, part := range strings.Split(dir, "/") {
		if tag := normalizeTag(part); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// splitTags reads a front matter tags value: "a, b", "a b", or "[a, b]".
func splitTags(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var tags []string
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if tag := normalizeTag(strings.Trim(part, `"'`)); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// mergeTags returns the tags in order with duplicates removed.
func mergeTags(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, tag := range list {
			if !seen[tag] {
				seen[tag] = true
				out = append(out, tag)
			}
		}
	}
	return out
}

// fileTitle is a file's name, the title of anything without a better one.
// The extension stays: backup.sh and backup.timer are different documents.
func fileTitle(rel string) string { return path.Base(rel) }

// parsedMarkdown is what indexing and the document view need from a Markdown file.
type parsedMarkdown struct {
	Doc   *markdown.Document
	Title string
	Tags  []string
	// FrontMatterNote explains why front matter was set aside, if it was.
	FrontMatterNote string
}

// parseMarkdown renders a Markdown file with modules/markdown. Its front
// matter parser accepts flat key: value pairs only. A block it rejects (YAML
// lists, nested maps) is set aside so the body still renders; a tags line in
// that block is still read, so "tags: [a, b]" works either way.
func parseMarkdown(r *markdown.Renderer, rel string, src []byte) (parsedMarkdown, error) {
	var out parsedMarkdown
	doc, err := r.Parse(src)
	if err != nil {
		block, body, ok := splitFrontMatter(src)
		if !ok {
			return out, err
		}
		doc, err = r.Parse(body)
		if err != nil {
			return out, err
		}
		out.FrontMatterNote = "front matter uses YAML structure; only its tags were read"
		for _, line := range strings.Split(block, "\n") {
			if key, value, found := strings.Cut(line, ":"); found && strings.TrimSpace(key) == "tags" {
				out.Tags = splitTags(value)
			}
		}
	} else {
		out.Tags = splitTags(doc.Meta["tags"])
	}
	out.Doc = doc
	out.Title = strings.TrimSpace(doc.Meta["title"])
	if out.Title == "" {
		for _, h := range doc.Headings {
			if h.Level == 1 {
				out.Title = h.Text
				break
			}
		}
	}
	if out.Title == "" {
		out.Title = fileTitle(rel)
	}
	return out, nil
}

// splitFrontMatter separates a leading --- block from the body without
// interpreting it.
func splitFrontMatter(src []byte) (block string, body []byte, ok bool) {
	s := strings.ReplaceAll(strings.TrimPrefix(string(src), "\ufeff"), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", nil, false
	}
	block, rest, found := strings.Cut(s[len("---\n"):], "\n---\n")
	if !found {
		return "", nil, false
	}
	return block, []byte(rest), true
}

// cleanText removes control characters other than tab and newline. The index
// uses \x01 and \x02 as highlight markers, so they must never come from a file.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
