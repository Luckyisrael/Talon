# worktree

The W12.1 write overlay for mutating children: a private, detached git
worktree of the workspace, opened before the child is assembled and
disposed before its outcome is reported. See `worktree.go` for the full
contract.

## What the model sees

Nothing on success — a mutating child's edits arrive in the workspace
as a normal diff and the worktree itself is never mentioned again. What
the model *does* see, all under the structured `CHILD_WORKTREE:` prefix
inside the child's failed report (or the delegate refusal):

- **Open failures** — `CHILD_WORKTREE: <ws> is not a git repository …`
  (the workspace cannot be overlaid at all) or `git worktree add failed`
  (a repository with no commits has nothing to check out). The child
  never runs; nothing is reserved beyond what the Service already vets.
- **Merge conflicts** — `CHILD_WORKTREE: merge apply failed …` with the
  child's full patch in the error: the child's base and the workspace
  now disagree (the lost-update class surfacing instead of winning
  silently). The parent's state is untouched and the worktree is
  cleaned up regardless — resolve by hand or re-run the child.
- **Merge verify** — the patch applied but did not reverse-check: a
  belt-and-braces refusal rather than trusting `apply`'s exit code.
- **Cleanup** — `CHILD_WORKTREE: cleanup failed …` names a leftover
  directory or registration; disk debt, reported honestly, never a
  silent leak.

Under the default readonly posture (`TALONSTUDIO_CHILD_ISOLATION`)
none of this is reachable: a `mutating: true` spawn refuses earlier at
the Service seam with `DELEGATION_ISOLATION`, which names
`TALONSTUDIO_CHILD_ISOLATION=worktree` as the escape hatch.

## Contract

- **Three stages + always-cleanup**: capture (the worktree's own
  per-worktree index — `git add -A` so creations ride, then a
  binary-safe patch), apply (`git apply --binary` against the parent
  working tree), verify (reverse `--check` of the same patch proves it
  landed), then `git worktree remove --force` with prune/RemoveAll
  fallbacks — attempted on success, conflict, discard, and any early
  return (the runner's defer).
- **Success merges, failure discards**: `MergeBack` for a child that
  finished; `Discard` for one that failed — a broken child's partial
  state never touches the parent.
- **`.talon` rides along**: untracked files never reach a checkout, so
  the config surface (config, hooks, modes, pins) is copied in at Open;
  copy failure fails the overlay closed (a child governed by defaults
  it was never governed by would be a lie).
- **Serialized per workspace**: parallel children's merge-backs take a
  per-workspace lock — disjoint patches apply in any order with no lost
  update; same-region patches conflict as structured errors.

Gates: `worktree` package tests (apply/conflict/discard/no-change/
.talon-fidelity/cleanup-on-failure) and
`api/TestPhaseW12MutatingChildrenIsolated` (two parallel mutating
children, disjoint diffs, no lost update, dispose cleanup).
