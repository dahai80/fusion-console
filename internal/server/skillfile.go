package server

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// skillDoc is a SKILL.md split into the two frontmatter fields the console
// edits (name, description), every other frontmatter line kept verbatim
// (Extra: allowed-tools, license, metadata, ...), and the markdown body.
//
// Only name/description are interpreted. Everything else is carried through
// untouched so editing a skill in the console never drops fields the agent
// relies on.
type skillDoc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Extra       string `json:"extra"`
	Body        string `json:"body"`
}

// splitSkillFile separates the YAML frontmatter block from the body. ok is
// false when the text has no well-formed "---" ... "---" block, in which case
// the whole text is the body.
func splitSkillFile(text string) (fm, body string, ok bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", text, false
	}
	rest := text[len("---\n"):]
	// The closing delimiter is a line that is exactly "---".
	for off := 0; off <= len(rest); {
		var lineEnd int
		if i := strings.IndexByte(rest[off:], '\n'); i >= 0 {
			lineEnd = off + i
		} else {
			lineEnd = len(rest)
		}
		if strings.TrimRight(rest[off:lineEnd], " \t") == "---" {
			body = ""
			if lineEnd < len(rest) {
				body = rest[lineEnd+1:]
			}
			return rest[:off], body, true
		}
		if lineEnd == len(rest) {
			break
		}
		off = lineEnd + 1
	}
	return "", text, false
}

// stripCodeFence removes one markdown code fence wrapping the whole text —
// LLM drafts sometimes come back as ```markdown ... ```. Fences inside the
// body are left alone.
var wrappingFenceRe = regexp.MustCompile("(?s)^```(?:markdown|md|yaml)?[ \t]*\n(.*)\n```[ \t]*$")

func stripCodeFence(text string) string {
	t := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if m := wrappingFenceRe.FindStringSubmatch(t); m != nil {
		return strings.TrimSpace(m[1])
	}
	return t
}

// parseSkillFile parses a SKILL.md (or an LLM draft of one).
func parseSkillFile(text string) skillDoc {
	fm, body, ok := splitSkillFile(stripCodeFence(text))
	if !ok {
		return skillDoc{Body: strings.TrimSpace(body)}
	}
	doc := skillDoc{Body: strings.TrimSpace(body)}
	lines := strings.Split(strings.TrimRight(fm, "\n"), "\n")
	var extra []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		key, value, isKey := topLevelKey(line)
		if !isKey || (key != "name" && key != "description") {
			extra = append(extra, line)
			continue
		}
		// Gather the value's continuation lines (indented or blank).
		var cont []string
		for i+1 < len(lines) && (lines[i+1] == "" || lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
			i++
			cont = append(cont, lines[i])
		}
		v := yamlScalarValue(value, cont)
		if key == "name" {
			doc.Name = v
		} else {
			doc.Description = v
		}
	}
	doc.Extra = strings.Trim(strings.Join(extra, "\n"), "\n")
	return doc
}

// topLevelKey reports whether line is an unindented "key: value" mapping
// entry (not a comment or list item).
func topLevelKey(line string) (key, value string, ok bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '-' {
		return "", "", false
	}
	i := strings.Index(line, ":")
	if i <= 0 {
		return "", "", false
	}
	if i+1 < len(line) && line[i+1] != ' ' && line[i+1] != '\t' {
		return "", "", false // "a:b" is a plain scalar, not a key
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

// yamlScalarValue resolves the scalar forms SKILL.md files use in practice:
// plain (optionally continued on indented lines), single- or double-quoted,
// and block scalars (">", "|", with chomping indicators). The console shows
// descriptions on one line, so line breaks are folded to spaces.
func yamlScalarValue(first string, cont []string) string {
	first = stripYAMLComment(first)
	trimmed := make([]string, 0, len(cont))
	for _, c := range cont {
		if t := strings.TrimSpace(c); t != "" {
			trimmed = append(trimmed, t)
		}
	}
	switch {
	case first != "" && (first[0] == '>' || first[0] == '|'):
		return strings.Join(trimmed, " ")
	case strings.HasPrefix(first, `"`):
		raw := strings.Join(append([]string{first}, trimmed...), " ")
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, `"`), `"`)
		r := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, " ", `\t`, " ")
		return strings.TrimSpace(r.Replace(raw))
	case strings.HasPrefix(first, "'"):
		raw := strings.Join(append([]string{first}, trimmed...), " ")
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "'"), "'")
		return strings.TrimSpace(strings.ReplaceAll(raw, "''", "'"))
	default:
		return strings.TrimSpace(strings.Join(append([]string{first}, trimmed...), " "))
	}
}

// stripYAMLComment drops a trailing " # comment" from an unquoted value.
func stripYAMLComment(v string) string {
	if v == "" || v[0] == '"' || v[0] == '\'' {
		return v
	}
	if i := strings.Index(v, " #"); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return v
}

// yamlQuote renders a one-line string as a YAML scalar, double-quoting it
// whenever a plain scalar would be misread (": ", leading indicators, ...).
func yamlQuote(s string) string {
	if s == "" {
		return `""`
	}
	needs := strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@` \t") ||
		strings.HasSuffix(s, " ") || strings.HasSuffix(s, ":") ||
		strings.Contains(s, ": ") || strings.Contains(s, " #")
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "on", "off", "null", "~":
		needs = true
	}
	if !needs {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// validateSkillExtra rejects extra frontmatter that would corrupt the file or
// shadow the fields the console writes itself.
func validateSkillExtra(extra string) error {
	for _, line := range strings.Split(extra, "\n") {
		if strings.TrimRight(line, " \t") == "---" {
			return fmt.Errorf("extra frontmatter must not contain a '---' line")
		}
		if key, _, ok := topLevelKey(line); ok && (key == "name" || key == "description") {
			return fmt.Errorf("extra frontmatter must not set %q — use the %s field", key, key)
		}
	}
	return nil
}

// renderSkillFile writes a SKILL.md: name, description, then the preserved
// extra frontmatter lines, then the body.
func renderSkillFile(doc skillDoc) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + doc.Name + "\n")
	if d := strings.Join(strings.Fields(doc.Description), " "); d != "" {
		b.WriteString("description: " + yamlQuote(d) + "\n")
	}
	if extra := strings.Trim(strings.ReplaceAll(doc.Extra, "\r\n", "\n"), "\n"); extra != "" {
		b.WriteString(extra + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(doc.Body) + "\n")
	return b.String()
}

// truncateUTF8 cuts s to at most max bytes without splitting a multi-byte
// character (CJK reference text is three bytes per rune).
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

// Skill audit: a heuristic scan of third-party or drafted SKILL.md content.
// Skills run with the agent's full permissions, so a malicious one can
// exfiltrate keys or run remote code. These checks surface the common
// patterns for human review; they are not a guarantee of safety.
var skillAuditRules = []struct {
	re  *regexp.Regexp
	msg string
}{
	{regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(ba|z)?sh\b`), "pipes a downloaded script straight into a shell"},
	{regexp.MustCompile(`(?i)\bbase64\s+(-d|--decode)\b`), "decodes base64 content (can hide commands)"},
	{regexp.MustCompile(`(?i)(~|\$HOME)/\.(ssh|aws|gnupg|kube|docker)\b|\bid_(rsa|ed25519)\b`), "touches credential directories (~/.ssh, ~/.aws, ...)"},
	{regexp.MustCompile(`(?i)\b(printenv|env)\s*\||\$\{?[A-Z0-9_]*(API_KEY|TOKEN|SECRET|PASSWORD)\b`), "reads secrets or environment variables"},
	{regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f?\s+(/|~|\$HOME)(\s|$)`), "recursive delete of / or the home directory"},
	{regexp.MustCompile(`(?i)ignore (all |any )?(previous|prior|above) (instructions|rules)`), "contains prompt-injection phrasing"},
	{regexp.MustCompile("[\u200b\u200c\u200d\u2060\ufeff\u202a-\u202e\u2066-\u2069]"), "contains invisible or bidi-control Unicode characters"},
	{regexp.MustCompile(`<!--`), "contains HTML comments (hidden from rendered previews)"},
}

// bundledRefRes find references to files bundled next to SKILL.md: paths in
// the conventional subdirectories, relative markdown links, and the
// upper-case companion docs skills use (FORMS.md, REFERENCE.md).
var bundledRefRes = []*regexp.Regexp{
	regexp.MustCompile(`(?:\]\(\.?/?|\b)((?:scripts|references|assets|templates)/[A-Za-z0-9_./-]+)`),
	regexp.MustCompile(`\]\((?:\./)?([A-Za-z0-9_-][A-Za-z0-9_./-]*\.[A-Za-z0-9]+)\)`),
	regexp.MustCompile(`\b([A-Z][A-Z0-9_-]*\.md)\b`),
}

// notBundled are file names skills mention without shipping them.
var notBundled = map[string]bool{"SKILL.md": true, "README.md": true, "CLAUDE.md": true, "AGENTS.md": true}

func auditSkillContent(content string) []string {
	var warnings []string
	for _, rule := range skillAuditRules {
		if rule.re.MatchString(content) {
			warnings = append(warnings, rule.msg)
		}
	}
	return warnings
}

// bundledFileRefs lists the bundled files a SKILL.md points at (scripts/,
// references/, ...). Import fetches SKILL.md only, so these are missing.
func bundledFileRefs(content string) []string {
	seen := map[string]bool{}
	var refs []string
	for _, re := range bundledRefRes {
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			ref := strings.TrimRight(m[1], ".")
			if !seen[ref] && !notBundled[ref] && !strings.Contains(ref, "..") {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}
