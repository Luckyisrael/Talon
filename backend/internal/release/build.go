package release

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	gbuild "github.com/seekersstudio/backend/internal/build"
	sdk "github.com/seekersstudio/backend/internal/sdk"
	"github.com/seekersstudio/backend/internal/store"
)

// Builder runs isolated release builds: a scratch copy of the workspace gets
// a clean prebuild with the project's personalized identity, a full
// assembleRelease, and a real signature from the project's own keystore. The
// dev-loop workspace is never touched and the dev session never notices.
type Builder struct {
	Store        *store.Store
	Keys         *Keystore
	WorkspaceDir string
	TemplateDir  string
	ExportsDir   string
	Log          io.Writer
	OnPhase      func(phase string)
}

func (b *Builder) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print("release: " + msg)
	if b.Log != nil {
		fmt.Fprintln(b.Log, msg)
	}
}

func (b *Builder) phase(name string) {
	b.logf("phase: %s", name)
	if b.OnPhase != nil {
		b.OnPhase(name)
	}
}

func (b *Builder) exportsDir() string {
	if b.ExportsDir != "" {
		return b.ExportsDir
	}
	return "./exports"
}

// Build runs the full pipeline and returns the signed APK path.
func (b *Builder) Build(ctx context.Context, projectID int64) (string, error) {
	project, err := b.Store.GetProject(projectID)
	if err != nil {
		return "", fmt.Errorf("no such project: %w", err)
	}
	if strings.TrimSpace(project.AndroidPackage) == "" {
		return "", fmt.Errorf("project has no android package identity")
	}

	ks, err := b.Keys.Ensure(ctx, projectID)
	if err != nil {
		return "", err
	}
	password, err := b.Keys.Password(ks)
	if err != nil {
		return "", err
	}

	javaHome, androidHome := sdk.JavaHome(), sdk.AndroidHome()
	if javaHome == "" || androidHome == "" {
		return "", fmt.Errorf("JDK 17 or Android SDK not found")
	}
	apksigner, err := sdk.Apksigner(androidHome)
	if err != nil {
		return "", err
	}
	aapt := filepath.Join(filepath.Dir(apksigner), "aapt")
	if _, err := os.Stat(aapt); err != nil {
		return "", fmt.Errorf("aapt not found next to apksigner")
	}

	// Scratch copy: the dev workspace is never built in place and never
	// mutated — identity injection happens only in the copy.
	scratch, err := b.scratchCopy(projectID)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := os.RemoveAll(scratch); err != nil {
			log.Printf("release: could not clean scratch dir %s: %v", scratch, err)
		}
	}()

	homeDir, _ := os.UserHomeDir()
	env := append(os.Environ(),
		"JAVA_HOME="+javaHome,
		"ANDROID_HOME="+androidHome,
		"PATH="+javaHome+"/bin:"+androidHome+"/platform-tools:"+os.Getenv("PATH"),
		"GRADLE_USER_HOME="+filepath.Join(homeDir, ".gradle"),
		"CI=1",
	)

	b.phase("identity")
	if err := injectIdentity(scratch, project.AndroidPackage, project.Scheme); err != nil {
		return "", err
	}

	b.phase("prebuild")
	// No --clean: wiping android/ every release destroys incremental gradle
	// state. Identity staleness is handled surgically below (patchApplicationID)
	// and verified at verify-identity — a mismatch fails the build loudly
	// instead of shipping the wrong package.
	if out, err := runIn(ctx, scratch, env, "npx", "expo", "prebuild", "-p", "android", "--no-install"); err != nil {
		return "", fmt.Errorf("expo prebuild failed: %v\n%s", err, tail(out))
	}

	b.phase("identity-patch")
	if err := patchApplicationID(filepath.Join(scratch, "android", "app", "build.gradle"), project.AndroidPackage); err != nil {
		return "", err
	}

	b.phase("assembleRelease")
	androidDir := filepath.Join(scratch, "android")
	// Perf flags, same as the verify gate: the scratch tree is prebuilt fresh,
	// so its gradle.properties is always expo-default.
	_ = gbuild.EnsureGradlePerfFlags(androidDir)
	if out, err := runIn(ctx, androidDir, env, "./gradlew", "assembleRelease", "--build-cache", "--daemon"); err != nil {
		return "", fmt.Errorf("assembleRelease failed: %v\n%s", err, tail(out))
	}
	unsigned := filepath.Join(androidDir, "app", "build", "outputs", "apk", "release", "app-release.apk")
	if _, err := os.Stat(unsigned); err != nil {
		return "", fmt.Errorf("release APK not produced")
	}

	b.phase("verify-identity")
	actualPkg, err := apkPackage(ctx, aapt, unsigned)
	if err != nil {
		return "", err
	}
	if actualPkg != project.AndroidPackage {
		return "", fmt.Errorf("identity mismatch: artifact is %q, project is %q", actualPkg, project.AndroidPackage)
	}

	b.phase("sign")
	signed, err := b.signAPK(ctx, apksigner, unsigned, ks.Path, ks.Alias, password, projectID)
	if err != nil {
		return "", err
	}

	b.phase("verify-signature")
	certFP, err := signerFingerprint(ctx, apksigner, signed)
	if err != nil {
		return "", err
	}
	if !sameFingerprint(certFP, ks.Fingerprint) {
		return "", fmt.Errorf("signature check failed: APK signer %q != keystore %q", certFP, ks.Fingerprint)
	}

	b.logf("project %d: release verified (package %s, signer %s)", projectID, actualPkg, certFP)
	return signed, nil
}

// scratchCopy duplicates the workspace minus node_modules (relinked, not
// copied — the tree is gigabytes otherwise) into a timestamped scratch dir.
func (b *Builder) scratchCopy(projectID int64) (string, error) {
	src := filepath.Join(b.WorkspaceDir, fmt.Sprintf("%d", projectID))
	if _, err := os.Stat(filepath.Join(src, "package.json")); err != nil {
		return "", fmt.Errorf("workspace not found: %w", err)
	}
	dst := filepath.Join(b.exportsDir(), fmt.Sprintf("scratch-%d-%d", projectID, time.Now().Unix()))
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	// Relink dependencies instead of copying them (8GB+ — never copied for an
	// export). The link MUST resolve in one hop straight to the real directory:
	// Metro cannot resolve modules through a chained symlink (scratch ->
	// workspace -> template) and fails with "could not be found within the
	// project", so EvalSymlinks flattens workspace -> template here.
	realNM, err := filepath.EvalSymlinks(filepath.Join(src, "node_modules"))
	if err != nil {
		os.RemoveAll(dst)
		return "", fmt.Errorf("could not resolve node_modules: %w", err)
	}
	os.RemoveAll(filepath.Join(dst, "node_modules"))
	if err := os.Symlink(realNM, filepath.Join(dst, "node_modules")); err != nil {
		os.RemoveAll(dst)
		return "", fmt.Errorf("could not link node_modules: %w", err)
	}
	// Backfill template dirs older workspaces predate (notably assets/, which
	// app.json references and prebuild fails without). Scratch-only: the live
	// workspace is never touched.
	for _, dirname := range []string{"assets"} {
		if _, err := os.Stat(filepath.Join(dst, dirname)); err == nil {
			continue
		}
		if b.TemplateDir == "" {
			continue
		}
		if err := copyTree(filepath.Join(b.TemplateDir, dirname), filepath.Join(dst, dirname)); err != nil {
			os.RemoveAll(dst)
			return "", fmt.Errorf("could not backfill %s/: %w", dirname, err)
		}
	}
	return dst, nil
}

// injectIdentity writes the project's personalized bundle ID and scheme into
// the scratch copy's app.json. This is the first and only point where the
// real identity enters a native artifact — the dev loop always runs under the
// fixed template identity.
func injectIdentity(scratch, androidPackage, scheme string) error {
	path := filepath.Join(scratch, "app.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var app map[string]any
	if err := json.Unmarshal(raw, &app); err != nil {
		return err
	}
	expo, _ := app["expo"].(map[string]any)
	if expo == nil {
		expo = map[string]any{}
		app["expo"] = expo
	}
	expo["scheme"] = scheme
	android, _ := expo["android"].(map[string]any)
	if android == nil {
		android = map[string]any{}
		expo["android"] = android
	}
	android["package"] = androidPackage
	out, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// patchApplicationID surgically sets the package in a prebuilt android/
// tree: both the `namespace` and `applicationId` lines in app/build.gradle.
// A non-clean prebuild reuses android/ for gradle incrementality, but the
// tree may predate the project's identity — this pins it. Fails loudly when
// either line is absent (a generated tree always has both); the downstream
// verify-identity phase re-checks the built artifact independently.
func patchApplicationID(gradlePath, androidPackage string) error {
	raw, err := os.ReadFile(gradlePath)
	if err != nil {
		return fmt.Errorf("read build.gradle: %w", err)
	}
	out := string(raw)
	changed := 0
	for _, key := range []string{"namespace", "applicationId"} {
		re := regexp.MustCompile(`(?m)^(\s*` + key + `\s+)'[^']*'`)
		if re.MatchString(out) {
			out = re.ReplaceAllString(out, `${1}'`+androidPackage+`'`)
			changed++
		}
	}
	if changed != 2 {
		return fmt.Errorf("build.gradle lacks namespace/applicationId lines — refusing to guess identity")
	}
	if err := os.WriteFile(gradlePath, []byte(out), 0o644); err != nil {
		return fmt.Errorf("write build.gradle: %w", err)
	}
	return nil
}

// signAPK signs with apksigner, passing the password via a 0600 temp file —
// never on the command line where ps could read it — and returns the final
// artifact path under the exports dir.
func (b *Builder) signAPK(ctx context.Context, apksigner, unsigned, ksPath, alias, password string, projectID int64) (string, error) {
	pwPath, cleanup, err := passFile(password)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if err := os.MkdirAll(b.exportsDir(), 0o755); err != nil {
		return "", err
	}
	signed := filepath.Join(b.exportsDir(), fmt.Sprintf("project-%d-%d.apk", projectID, time.Now().Unix()))
	// NOTE: --ks-pass takes its provider as the NEXT arg ("--ks-pass",
	// "file:<path>"); the single-arg colon form (--ks-pass:file) is rejected
	// by this build-tools version.
	args := []string{"sign", "--ks", ksPath, "--ks-key-alias", alias,
		"--ks-pass", "file:" + pwPath, "--key-pass", "file:" + pwPath,
		"--out", signed, unsigned}
	if out, err := exec.CommandContext(ctx, apksigner, args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("apksigner failed: %v\n%s", err, tail(string(out)))
	}
	return signed, nil
}

var pkgRe = regexp.MustCompile(`package:\s+name='([^']+)'`)
var verCodeRe = regexp.MustCompile(`versionCode='(\d+)'`)
var verNameRe = regexp.MustCompile(`versionName='([^']+)'`)

// AppVersions carries versionCode/Name read from a built APK.
type AppVersions struct {
	Code int
	Name string
}

// APKVersions reads versionCode and versionName from the APK's own manifest.
// Versions come from the artifact, never from user input.
func APKVersions(apk string) (AppVersions, error) {
	androidHome := sdk.AndroidHome()
	if androidHome == "" {
		return AppVersions{}, fmt.Errorf("Android SDK not found")
	}
	apksigner, err := sdk.Apksigner(androidHome)
	if err != nil {
		return AppVersions{}, err
	}
	aapt := filepath.Join(filepath.Dir(apksigner), "aapt")
	_, code, name, err := apkIdentity(context.Background(), aapt, apk)
	if err != nil {
		return AppVersions{}, err
	}
	return AppVersions{Code: code, Name: name}, nil
}

func apkPackage(ctx context.Context, aapt, apk string) (string, error) {
	pkg, _, _, err := apkIdentity(ctx, aapt, apk)
	return pkg, err
}

// apkIdentity reads package, versionCode, and versionName from the built APK.
// Versions come from the artifact itself, never from user input.
func apkIdentity(ctx context.Context, aapt, apk string) (string, int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, aapt, "dump", "badging", apk).CombinedOutput()
	if err != nil {
		return "", 0, "", fmt.Errorf("aapt failed: %v", err)
	}
	m := pkgRe.FindStringSubmatch(string(out))
	if m == nil {
		return "", 0, "", fmt.Errorf("no package name in aapt output")
	}
	code := 0
	if vm := verCodeRe.FindStringSubmatch(string(out)); vm != nil {
		fmt.Sscanf(vm[1], "%d", &code)
	}
	name := ""
	if vm := verNameRe.FindStringSubmatch(string(out)); vm != nil {
		name = vm[1]
	}
	return m[1], code, name, nil
}

var certRe = regexp.MustCompile(`(?i)SHA-256 digest:\s*([0-9a-f:]+)`)

func signerFingerprint(ctx context.Context, apksigner, apk string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, apksigner, "verify", "--print-certs", apk).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("apksigner verify failed: %v\n%s", err, tail(string(out)))
	}
	if m := certRe.FindStringSubmatch(string(out)); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("no signer certificate in apksigner output")
}

func sameFingerprint(a, b string) bool {
	norm := func(s string) string {
		return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	}
	return norm(a) == norm(b) && a != ""
}

func runIn(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	const max = 4000
	if len(s) <= max {
		return s
	}
	return "… [" + fmt.Sprint(len(s)-max) + " earlier bytes omitted] …\n" + s[len(s)-max:]
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if info.IsDir() && (info.Name() == "node_modules" || info.Name() == ".gradle" || info.Name() == "build") {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
