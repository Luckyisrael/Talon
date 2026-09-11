package identity

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	// Base package name for all projects (reverse-DNS prefix)
	basePackage = "com.talon"
)

// ProjectIdentity holds the generated identity values for a project.
type ProjectIdentity struct {
	Name           string // Display name (from user input)
	Slug           string // URL-safe slug (unique)
	AndroidPackage string // e.g., com.talon.myapp
	IOSBundleID    string // same as AndroidPackage
	Scheme         string // deep-link scheme (unique)
}

// Generate creates a ProjectIdentity from a user-chosen name.
// It does NOT check for collisions — call EnsureUnique after.
func Generate(name string) ProjectIdentity {
	slug := generateSlug(name)
	pkg := generatePackage(slug)
	scheme := generateScheme(slug)

	return ProjectIdentity{
		Name:           name,
		Slug:           slug,
		AndroidPackage: pkg,
		IOSBundleID:    pkg,
		Scheme:         scheme,
	}
}

// generateSlug converts a display name to a URL-safe slug.
// Rules: lowercase, alphanumeric + hyphens, no leading/trailing hyphens.
func generateSlug(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1 // drop
	}, s)

	// Collapse multiple hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")

	if s == "" {
		s = "app"
	}
	return s
}

// generatePackage creates a valid Android package name from a slug.
// Format: com.talon.<slug> (only lowercase letters and dots).
func generatePackage(slug string) string {
	// Sanitize slug for package: only lowercase alphanumeric and dots
	sanitized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' {
			return r
		}
		return -1
	}, strings.ToLower(slug))

	// Remove leading digits
	for len(sanitized) > 0 && unicode.IsDigit(rune(sanitized[0])) {
		sanitized = sanitized[1:]
	}
	if sanitized == "" {
		sanitized = "app"
	}

	return fmt.Sprintf("%s.%s", basePackage, sanitized)
}

// generateScheme creates a deep-link scheme from a slug.
// Scheme must start with a letter and contain only alphanumeric and hyphens/dots/plus.
func generateScheme(slug string) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '.' || r == '+' {
			return r
		}
		return -1
	}, strings.ToLower(slug))

	if s == "" || !unicode.IsLetter(rune(s[0])) {
		s = "app" + s
	}
	return s
}

// ValidatePackage checks if a string is a valid Android Java package name.
// Rules: lowercase segments separated by dots, no leading digits, no hyphens.
var validPackage = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*$`)

func ValidatePackage(pkg string) error {
	if !validPackage.MatchString(pkg) {
		return fmt.Errorf("invalid package name %q: must be lowercase reverse-DNS (e.g., com.example.app)", pkg)
	}
	return nil
}

// EnsureUnique appends a numeric suffix to slug/package/scheme until all are unique.
// Takes a checker function that returns true if a value already exists.
func EnsureUnique(id ProjectIdentity, slugExists, packageExists func(string) bool) ProjectIdentity {
	result := id
	suffix := 1

	for slugExists(result.Slug) || packageExists(result.AndroidPackage) {
		suffix++
		suffixStr := fmt.Sprintf("-%d", suffix)
		result.Slug = id.Slug + suffixStr
		newPkg := fmt.Sprintf("%s.%s", basePackage, strings.ReplaceAll(result.Slug, "-", ""))
		result.AndroidPackage = newPkg
		result.IOSBundleID = newPkg
		result.Scheme = id.Scheme + suffixStr
	}

	return result
}
