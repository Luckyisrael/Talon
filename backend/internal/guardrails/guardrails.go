package guardrails

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type Guardrail interface {
	Name() string
	Validate(files []FilePatch) error
}

type FilePatch struct {
	Path    string
	Content string
}

type Registry struct {
	guardrails []Guardrail
}

func NewRegistry() *Registry {
	return &Registry{
		guardrails: []Guardrail{
			&ReactNativeOnly{},
			&NoSecrets{},
			&ExpoVersionPin{},
		},
	}
}

func (r *Registry) Validate(files []FilePatch) error {
	for _, g := range r.guardrails {
		if err := g.Validate(files); err != nil {
			return fmt.Errorf("guardrail %s: %w", g.Name(), err)
		}
	}
	return nil
}

type ReactNativeOnly struct{}

func (g *ReactNativeOnly) Name() string { return "react_native_only" }

func (g *ReactNativeOnly) Validate(files []FilePatch) error {
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f.Path))
		if ext == ".html" || ext == ".css" {
			return fmt.Errorf("non-React Native file detected: %s (HTML/CSS files are not allowed)", f.Path)
		}

		if ext == ".tsx" || ext == ".ts" || ext == ".js" || ext == ".jsx" {
			content := strings.ToLower(f.Content)
			if strings.Contains(content, "<div") || strings.Contains(content, "<span") || strings.Contains(content, "<button") {
				return fmt.Errorf("HTML elements detected in %s (use React Native components instead)", f.Path)
			}
			if strings.Contains(content, "document.getelementbyid") || strings.Contains(content, "window.") {
				return fmt.Errorf("DOM APIs detected in %s (use React Native APIs instead)", f.Path)
			}
		}
	}
	return nil
}

type NoSecrets struct{}

func (g *NoSecrets) Name() string { return "no_secrets" }

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)private[_-]?key\s*[:=]\s*["'][A-Za-z0-9+/=]{20,}["']`),
	regexp.MustCompile(`(?i)seed[_-]?phrase\s*[:=]\s*["'][A-Za-z0-9\s]{20,}["']`),
	regexp.MustCompile(`(?i)api[_-]?key\s*[:=]\s*["'][A-Za-z0-9]{20,}["']`),
	regexp.MustCompile(`(?i)sk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`(?i)0x[A-Fa-f0-9]{40,}`),
}

func (g *NoSecrets) Validate(files []FilePatch) error {
	for _, f := range files {
		for _, pattern := range secretPatterns {
			if pattern.MatchString(f.Content) {
				return fmt.Errorf("potential secret detected in %s", f.Path)
			}
		}
	}
	return nil
}

type ExpoVersionPin struct{}

func (g *ExpoVersionPin) Name() string { return "expo_version_pin" }

func (g *ExpoVersionPin) Validate(files []FilePatch) error {
	for _, f := range files {
		if filepath.Base(f.Path) == "package.json" {
			var pkg struct {
				Dependencies    map[string]string `json:"dependencies"`
				DevDependencies map[string]string `json:"devDependencies"`
			}
			if err := json.Unmarshal([]byte(f.Content), &pkg); err != nil {
				continue
			}

			if pkg.Dependencies != nil {
				if expo, ok := pkg.Dependencies["expo"]; ok {
					if !strings.HasPrefix(expo, "~55.") && !strings.HasPrefix(expo, "^55.") {
						return fmt.Errorf("Expo SDK version change detected: %s (must stay on SDK 55)", expo)
					}
				}
			}
		}
	}
	return nil
}
