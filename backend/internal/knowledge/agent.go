// Package agent holds the pre-baked knowledge the build agent works from: terse,
// always-on rules and load-on-demand skills. Both are authored as Markdown with a
// small front-matter header and embedded into the binary, so they ship with the
// server and cannot go missing in a deployment.
//
// Rules are constraints ("never do X") kept small enough to sit in the cached
// system-prompt prefix on every request. Skills are detailed, version-pinned
// how-tos pulled in only when relevant — either by the model calling read_skill,
// or auto-attached when a tool call trips one of the skill's triggers. This is
// how quality holds across models: a weak model that would not know the current
// API is handed it at the moment it acts, rather than being trusted to remember.
package knowledge

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

//go:embed content
var contentFS embed.FS

// Rule is an always-on (or, in future, glob-scoped) constraint block.
type Rule struct {
	Name        string
	Description string
	AlwaysApply bool
	Globs       []string
	Body        string
}

// Skill is a load-on-demand capability document.
type Skill struct {
	Name        string
	Description string
	Version     string
	AutoAttach  bool
	Triggers    []string
	Body        string
}

// Registry holds all loaded rules and skills plus the preinstalled-package list
// read from the project template. It is built once at startup and is read-only
// thereafter, so a single instance is safe to share across concurrent requests.
type Registry struct {
	rules        []Rule
	skills       []Skill
	skillByName  map[string]*Skill
	preinstalled []string
}

// Load reads the embedded rules and skills, validates them, and reads the
// preinstalled-package list from the given template package.json.
//
// A malformed embedded file is fatal: it is authored content that must be correct,
// and a unit test exercises this so it never reaches production. A missing or
// unreadable template package.json is not fatal — it degrades to an empty list,
// leaving the rest of the registry intact, because the embedded knowledge is the
// core value and the package list is an enhancement.
func Load(templatePackageJSON string) (*Registry, error) {
	r := &Registry{skillByName: map[string]*Skill{}}

	if err := r.loadRules(); err != nil {
		return nil, err
	}
	if err := r.loadSkills(); err != nil {
		return nil, err
	}
	r.preinstalled = readPreinstalled(templatePackageJSON)
	return r, nil
}

func (r *Registry) loadRules() error {
	entries, err := fs.ReadDir(contentFS, "content/rules")
	if err != nil {
		return fmt.Errorf("reading embedded rules: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := path.Join("content/rules", e.Name())
		raw, err := contentFS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		scalars, lists, body, ok := parseFrontMatter(string(raw))
		if !ok {
			return fmt.Errorf("%s: missing front matter", p)
		}
		name := scalars["name"]
		if name == "" {
			return fmt.Errorf("%s: rule has no name", p)
		}
		if strings.TrimSpace(body) == "" {
			return fmt.Errorf("%s: rule %q has an empty body", p, name)
		}
		r.rules = append(r.rules, Rule{
			Name:        name,
			Description: scalars["description"],
			AlwaysApply: scalars["alwaysApply"] == "true",
			Globs:       lists["globs"],
			Body:        strings.TrimSpace(body),
		})
	}
	sort.Slice(r.rules, func(i, j int) bool { return r.rules[i].Name < r.rules[j].Name })
	return nil
}

func (r *Registry) loadSkills() error {
	entries, err := fs.ReadDir(contentFS, "content/skills")
	if err != nil {
		return fmt.Errorf("reading embedded skills: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := path.Join("content/skills", e.Name(), "SKILL.md")
		raw, err := contentFS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("skill %q has no SKILL.md: %w", e.Name(), err)
		}
		scalars, lists, body, ok := parseFrontMatter(string(raw))
		if !ok {
			return fmt.Errorf("%s: missing front matter", p)
		}
		name := scalars["name"]
		switch {
		case name == "":
			return fmt.Errorf("%s: skill has no name", p)
		case name != e.Name():
			return fmt.Errorf("%s: skill name %q does not match its directory %q", p, name, e.Name())
		case scalars["description"] == "":
			return fmt.Errorf("%s: skill %q has no description", p, name)
		case strings.TrimSpace(body) == "":
			return fmt.Errorf("%s: skill %q has an empty body", p, name)
		}
		r.skills = append(r.skills, Skill{
			Name:        name,
			Description: scalars["description"],
			Version:     scalars["version"],
			AutoAttach:  scalars["autoAttach"] == "true",
			Triggers:    lists["triggers"],
			Body:        strings.TrimSpace(body),
		})
	}
	if len(r.skills) == 0 {
		return fmt.Errorf("no skills found in embedded content")
	}
	sort.Slice(r.skills, func(i, j int) bool { return r.skills[i].Name < r.skills[j].Name })
	// Index after the final sort so the pointers stay valid.
	for i := range r.skills {
		r.skillByName[r.skills[i].Name] = &r.skills[i]
	}
	return nil
}

// readPreinstalled reads dependency names from a package.json. Best-effort: any
// problem returns an empty list rather than failing the whole registry.
func readPreinstalled(packageJSONPath string) []string {
	if packageJSONPath == "" {
		return nil
	}
	raw, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil
	}
	set := map[string]bool{}
	for name := range pkg.Dependencies {
		set[name] = true
	}
	for name := range pkg.DevDependencies {
		set[name] = true
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RulesBlock renders the always-on rules plus the preinstalled-package list as a
// single Markdown section for the system-prompt prefix. Returns "" if empty.
func (r *Registry) RulesBlock() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	for _, rule := range r.rules {
		if !rule.AlwaysApply {
			continue
		}
		b.WriteString(rule.Body)
		b.WriteString("\n\n")
	}
	if len(r.preinstalled) > 0 {
		b.WriteString("## Preinstalled packages (already in every project — never reinstall)\n\n")
		b.WriteString(strings.Join(r.preinstalled, ", "))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// PreinstalledSet returns the preinstalled package names as a lookup set,
// lowercased so it matches however the model spelled a name. The guardrail layer
// uses it to refuse reinstalling something every project already ships with.
// Returns nil when nothing is preinstalled, which disables that check.
func (r *Registry) PreinstalledSet() map[string]bool {
	if r == nil || len(r.preinstalled) == 0 {
		return nil
	}
	set := make(map[string]bool, len(r.preinstalled))
	for _, p := range r.preinstalled {
		set[strings.ToLower(p)] = true
	}
	return set
}

// PreinstalledList returns the preinstalled package names in the same stable,
// sorted order RulesBlock renders them in — the display list, not the lowercased
// lookup set. Plan mode injects it into its system prompt so it names the same
// packages Build mode treats as already installed. Returns nil when nothing is
// preinstalled. The returned slice is a copy; callers must not mutate the registry.
func (r *Registry) PreinstalledList() []string {
	if r == nil || len(r.preinstalled) == 0 {
		return nil
	}
	out := make([]string, len(r.preinstalled))
	copy(out, r.preinstalled)
	return out
}

// Manifest renders the one-line description of every skill: what exists and when to
// reach for it. Only descriptions live here — never bodies — so it stays cheap in
// the cached prefix.
func (r *Registry) Manifest() string {
	if r == nil || len(r.skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Skills available\n\n")
	b.WriteString("Detailed, version-pinned how-tos for this project's stack. Call `read_skill` with " +
		"a name to load one before working in its area; some attach automatically when relevant. " +
		"Prefer a skill over memory — the stack here is newer than most training data.\n\n")
	for _, s := range r.skills {
		fmt.Fprintf(&b, "- **%s** — %s\n", s.Name, s.Description)
	}
	return strings.TrimSpace(b.String())
}

// SkillBody returns the full body of a named skill and whether it exists.
func (r *Registry) SkillBody(name string) (string, bool) {
	if r == nil {
		return "", false
	}
	s, ok := r.skillByName[strings.TrimSpace(name)]
	if !ok {
		return "", false
	}
	return s.Body, true
}

// SkillNames returns the loaded skill names, sorted.
func (r *Registry) SkillNames() []string {
	if r == nil {
		return nil
	}
	names := make([]string, len(r.skills))
	for i, s := range r.skills {
		names[i] = s.Name
	}
	return names
}

// AutoAttachFor returns the auto-attach skills whose triggers match a tool call.
// It is deliberately forgiving: import/content triggers are substring-matched
// against the raw tool input (which catches file contents in write_file, the new
// text in edit_file, and so on), path triggers glob-match the path argument, and
// pkg triggers prefix-match an install name. Only skills marked autoAttach are
// considered — everything else is left for the model to pull deliberately.
func (r *Registry) AutoAttachFor(toolName string, rawInput []byte) []*Skill {
	if r == nil {
		return nil
	}
	lowerInput := strings.ToLower(string(rawInput))
	var args struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(rawInput, &args)

	var out []*Skill
	for i := range r.skills {
		s := &r.skills[i]
		if s.AutoAttach && skillMatches(s, toolName, lowerInput, args.Path, args.Name) {
			out = append(out, s)
		}
	}
	return out
}

func skillMatches(s *Skill, toolName, lowerInput, argPath, argName string) bool {
	for _, t := range s.Triggers {
		kind, val, found := strings.Cut(t, ":")
		if !found {
			continue
		}
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		switch kind {
		case "import", "content":
			if strings.Contains(lowerInput, strings.ToLower(val)) {
				return true
			}
		case "path":
			if argPath != "" && globMatch(val, argPath) {
				return true
			}
		case "pkg":
			if toolName == "install_package" && argName != "" && pkgMatch(val, argName) {
				return true
			}
		}
		// `symptom:` and `phase:` are matched by SkillsForSymptom / SkillsForPhase,
		// not by AutoAttachFor, because they fire on tool *outputs* and on the
		// orchestrator's phase — not on a tool's *input*. They are silently
		// skipped here so a trigger frontmatter can advertise them without
		// breaking input-side dispatch.
	}
	return false
}

// SkillsForSymptom returns the auto-attach skills whose `symptom:` triggers
// match the given error text. The string after `symptom:` is treated as a Go
// RE2 regular expression (e.g. `symptom:TS[0-9]+` matches "error TS2322");
// when the pattern does not compile, fall back to a literal substring match
// so plain phrases like "adb: device not found" still resolve. Match is
// case-insensitive.
//
// The method is exported because the orchestrator owns the post-tool-error
// decision; it is not part of input-side AutoAttachFor. A return value of nil
// means "no skill matches this symptom", not "no skills are loaded".
func (r *Registry) SkillsForSymptom(errText string) []*Skill {
	if r == nil || errText == "" {
		return nil
	}
	lower := strings.ToLower(errText)
	var out []*Skill
	for i := range r.skills {
		s := &r.skills[i]
		// Symptom dispatch is the post-tool-error path; it must not require
		// autoAttach. Auto-attach is for input-side triggers (imports, paths,
		// file content). The orchestrator already decides whether the calling
		// context is allowed to use a skill.
		for _, t := range s.Triggers {
			kind, val, found := strings.Cut(t, ":")
			if !found || kind != "symptom" {
				continue
			}
			val = strings.TrimSpace(val)
			if val == "" {
				continue
			}
			pat := strings.ToLower(val)
			if re, err := regexp.Compile(pat); err == nil {
				if re.MatchString(lower) {
					out = append(out, s)
					break
				}
				continue
			}
			if strings.Contains(lower, pat) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// SkillsForPhase returns the skills whose `phase:` triggers name the given
// phase. The orchestrator calls this once per round to seed phase-appropriate
// knowledge (e.g. a PhaseHeal round automatically pulls `failure-patterns`).
//
// Unknown phase names return an empty list rather than an error — a phase
// added in code without a matching skill is normal during development, and
// the loop must keep working.
func (r *Registry) SkillsForPhase(phase string) []*Skill {
	if r == nil || phase == "" {
		return nil
	}
	var out []*Skill
	for i := range r.skills {
		s := &r.skills[i]
		for _, t := range s.Triggers {
			kind, val, found := strings.Cut(t, ":")
			if !found || kind != "phase" {
				continue
			}
			if strings.TrimSpace(val) == phase {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// globMatch is a small path glob supporting * (within a segment) and ** (any
// depth). It is not a full glob engine — only enough for the path triggers we
// author, e.g. "app/**".
func globMatch(pattern, p string) bool {
	pattern = strings.TrimSpace(pattern)
	p = strings.TrimPrefix(p, "./")
	switch {
	case pattern == "**" || pattern == "*":
		return true
	case strings.HasSuffix(pattern, "/**"):
		prefix := strings.TrimSuffix(pattern, "/**")
		return p == prefix || strings.HasPrefix(p, prefix+"/")
	case strings.HasPrefix(pattern, "**/"):
		suffix := strings.TrimPrefix(pattern, "**/")
		return p == suffix || strings.HasSuffix(p, "/"+suffix) || strings.Contains(p, "/"+suffix+"/")
	default:
		ok, err := path.Match(pattern, p)
		return err == nil && ok
	}
}

// pkgMatch prefix-matches an npm package name, tolerating a trailing "*".
func pkgMatch(pattern, name string) bool {
	pattern = strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(name, pattern)
}
