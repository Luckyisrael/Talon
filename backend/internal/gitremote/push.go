package gitremote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// pushTimeout bounds the whole push sequence. A push includes a network upload,
// so it gets more room than a local command but still cannot hang forever.
const pushTimeout = 120 * time.Second

// gitignoreContents is written into a workspace that has none, so the very first
// push cannot accidentally commit the node_modules symlink (which points into a
// shared template) or build output. It is intentionally minimal and only
// created when absent — a project that ships its own .gitignore is left alone.
const gitignoreContents = `# Written by SeekerStudio on first push.
node_modules/
.expo/
dist/
build/
web-build/
android/
ios/
*.log
.DS_Store
.env
.env.local
`

// PushOptions describes one push of a project's workspace to a GitHub repo.
type PushOptions struct {
	Dir         string // absolute path to the project's workspace
	RepoURL     string // clean HTTPS clone URL (no credentials), used for origin
	Token       string // OAuth access token — spliced into the push URL only, never stored
	Branch      string // target branch; defaults to "main"
	Message     string // commit message
	AuthorName  string // commit author name
	AuthorEmail string // commit author email
}

// PushResult reports what the push did, in terms safe to show a user. It never
// carries the token or the tokenized URL.
type PushResult struct {
	Branch       string `json:"branch"`
	CommitMade   bool   `json:"commit_made"`   // false when the tree was already clean
	NothingToDo  bool   `json:"nothing_to_do"` // clean tree AND remote already current
	Output       string `json:"-"`             // scrubbed combined output, for server logs
}

// Push initialises git in the workspace if needed, commits the current tree, and
// pushes it to the repository over HTTPS.
//
// The access token is used for exactly one command — the push — by splicing it
// into that command's URL argument. It is never written into .git/config (origin
// is set to the clean URL) and never persisted anywhere on disk. All captured
// output is scrubbed of the token before it is returned or logged, so a push
// failure that echoes the remote URL cannot leak the credential.
func Push(ctx context.Context, opts PushOptions) (PushResult, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		return PushResult{}, errors.New("push: workspace dir is empty")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return PushResult{}, errors.New("push: access token is empty")
	}
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		branch = "main"
	}
	message := strings.TrimSpace(opts.Message)
	if message == "" {
		message = "Update from SeekerStudio"
	}
	author := strings.TrimSpace(opts.AuthorName)
	if author == "" {
		author = "SeekerStudio"
	}
	email := strings.TrimSpace(opts.AuthorEmail)
	if email == "" {
		email = "noreply@seekerstudio.app"
	}

	// Build the tokenized push URL from the clean one. The token lives only in
	// this string, in memory, for the duration of the push command.
	u, err := url.Parse(opts.RepoURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return PushResult{}, fmt.Errorf("push: invalid repository URL")
	}
	pushURL := *u
	pushURL.User = url.UserPassword("x-access-token", opts.Token)
	tokenizedURL := pushURL.String()

	// Anything we must keep out of returned/logged text.
	secretsToScrub := []string{opts.Token, tokenizedURL, url.QueryEscape(opts.Token)}

	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()

	// Ensure a .gitignore exists before the first `git add`, so we never stage
	// node_modules or build artefacts.
	ensureGitignore(opts.Dir)

	var log bytes.Buffer
	run := func(args ...string) (string, error) {
		out, err := runGit(ctx, opts.Dir, args...)
		clean := scrub(out, secretsToScrub)
		log.WriteString(clean)
		return clean, err
	}

	// git init is idempotent; on an existing repo it just reports "reinitialized".
	if _, err := run("init"); err != nil {
		return PushResult{Output: log.String()}, wrapGit("git init", err, log.String())
	}
	// Name the default branch deterministically rather than relying on the host's
	// init.defaultBranch. -M renames the current branch (or the unborn one).
	if _, err := run("branch", "-M", branch); err != nil {
		return PushResult{Output: log.String()}, wrapGit("git branch", err, log.String())
	}
	// origin points at the CLEAN url. If it already exists with a different value
	// (e.g. the project was retargeted), set-url corrects it; otherwise add it.
	if _, err := run("remote", "add", "origin", opts.RepoURL); err != nil {
		// add fails if origin already exists — correct it instead.
		if _, err2 := run("remote", "set-url", "origin", opts.RepoURL); err2 != nil {
			return PushResult{Output: log.String()}, wrapGit("git remote", err2, log.String())
		}
	}

	if _, err := run("add", "-A"); err != nil {
		return PushResult{Output: log.String()}, wrapGit("git add", err, log.String())
	}

	// Commit with an explicit author via -c, so we never depend on or write host
	// git identity config.
	commitMade := true
	commitOut, err := run(
		"-c", "user.name="+author,
		"-c", "user.email="+email,
		"commit", "-m", message,
	)
	if err != nil {
		// A clean tree is not a failure: there is simply nothing new to commit.
		// We still attempt the push, in case earlier commits were never pushed.
		if isNothingToCommit(commitOut) {
			commitMade = false
		} else {
			return PushResult{Output: log.String()}, wrapGit("git commit", err, log.String())
		}
	}

	// The one command that carries the token. HEAD:refs/heads/<branch> pushes
	// whatever is checked out to the named remote branch.
	pushOut, err := runGit(ctx, opts.Dir, "push", tokenizedURL, "HEAD:refs/heads/"+branch)
	pushClean := scrub(pushOut, secretsToScrub)
	log.WriteString(pushClean)
	if err != nil {
		return PushResult{Branch: branch, CommitMade: commitMade, Output: log.String()},
			wrapGit("git push", err, pushClean)
	}

	nothingToDo := !commitMade && strings.Contains(pushClean, "up-to-date")
	return PushResult{
		Branch:      branch,
		CommitMade:  commitMade,
		NothingToDo: nothingToDo,
		Output:      log.String(),
	}, nil
}

// runGit runs one git command in dir with an isolated environment and returns
// its combined output. The environment deliberately excludes the server's
// secrets and pins git into non-interactive, host-config-free operation so a
// push can never block on a credential prompt or pick up an operator's identity.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// gitEnv is a minimal environment: enough for git to find itself and behave,
// nothing more. No server credentials are inherited, terminal prompts are
// disabled (a missing/invalid token then fails fast instead of hanging), and
// system/global git config is bypassed so the push is reproducible regardless
// of the host's ~/.gitconfig.
func gitEnv() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ASKPASS=/bin/true",
		"HOME=" + os.Getenv("HOME"),
		"LANG=C",
	}
	return env
}

// ensureGitignore writes a default .gitignore only when the workspace has none.
func ensureGitignore(dir string) {
	path := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return // already present — respect the project's own choices
	}
	// Best-effort: a failure here just means git may stage more than ideal, which
	// the caller's .gitignore-less state already implied. Not worth failing a push.
	_ = os.WriteFile(path, []byte(gitignoreContents), 0o644)
}

// scrub removes every secret from s, replacing each with a fixed marker. Called
// on all output before it is returned or logged.
func scrub(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		s = strings.ReplaceAll(s, sec, "***")
	}
	return s
}

func isNothingToCommit(out string) bool {
	return strings.Contains(out, "nothing to commit") ||
		strings.Contains(out, "nothing added to commit") ||
		strings.Contains(out, "working tree clean")
}

// wrapGit turns a git failure into an error that names the step and includes the
// (already scrubbed) tail of output, so callers get an actionable message
// without any credential in it.
func wrapGit(step string, err error, out string) error {
	out = strings.TrimSpace(out)
	if len(out) > 2000 {
		out = out[len(out)-2000:]
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("%s failed (exit %d): %s", step, exitErr.ExitCode(), out)
	}
	return fmt.Errorf("%s failed: %v: %s", step, err, out)
}
