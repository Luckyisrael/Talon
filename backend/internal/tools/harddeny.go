package tools

// harddeny.go — W9.4's adapter: the shell's always-blocked class exposed
// to the approval seam without duplicating a single pattern.
//
// blockedPatterns/classifyCommand remain the one definition of "never
// allowed" (secrets, the host, the network). This file only names that
// class for the seam's HardDeny hook, so auto mode and standing grants
// both fail against exactly what the shell already refuses upstream.

import "strings"

// BlockedReason reports the hard-deny reason for an exact shell command —
// the tierBlocked class, which no permission mode, allowlist entry, or
// standing grant can waive. Non-blocked commands report false.
func BlockedReason(command string) (string, bool) {
	tier, reason := classifyCommand(strings.TrimSpace(command))
	if tier != tierBlocked {
		return "", false
	}
	if reason == "" {
		reason = "always-blocked command class"
	}
	return reason, true
}

// CommandFromAction extracts the exact command from a tier-2 escalation's
// Action text — the inverse of sandbox's escalationAction ("shell <cmd> —
// <justification>"). Non-shell or unparseable actions report false, and
// the caller treats them as having no command (never as a match).
//
// The split targets the FIRST " — " (space-emdash-space): the separator
// escalationAction itself writes, chosen because it is not valid shell
// syntax in practice; a command that genuinely contains one parses to a
// prefix that then fails BlockedReason's exact classification — failing
// toward the prompt, never toward an allow.
func CommandFromAction(action string) (string, bool) {
	const shellPrefix = "shell "
	if !strings.HasPrefix(action, shellPrefix) {
		return "", false
	}
	rest := action[len(shellPrefix):]
	if i := strings.Index(rest, " — "); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}
