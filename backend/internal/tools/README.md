# tools

The model-facing tool registry. Every tool's Description() is the model's
contract; this README is the author's.

## What the model sees — per tool group

- **Reading** (list_files, read_file, grep_codebase/search_files, find_files,
  explore, project_map, inspect_package): read-only, safe to overlap.
  read_file accepts ranges; project_map is the grounded snapshot. Budgets:
  output cap 32 KB per result (H8 spill replaces silent truncation when the
  store is attached).
- **Writing** (write_file, edit_file, multi_edit, delete_file): read-before-
  write enforced (write_conflict when the file changed since the model last
  saw it — re-read, incorporate, write again); paths sandbox-checked (H4).
- **Shelling** (run_command, run_shell, run_typecheck, run_lint): tiered —
  read-only diagnostics free, workspace mutation logged, destructive or
  egress needs approval via ask_user. Timeout 60s default, 300s max; output
  streamed live.
- **Skills** (read_skill): loads a body on demand; the catalog manifest
  lives in the prompt (H6).
- **Delegation** (delegate, delegate_fork, list_children, run_workflow): H9/H10 fan-out;
  reports arrive between CHILD REPORT markers, framed as data.
- **Questions** (ask_user): pauses the turn; answers bind approvals.
- **Memory** (write_memory, read_memory, ADR tools): the project's durable
  knowledge; bounded lines per entry.
- **Tasks** (mark_task): plan checklist status flips — the checklist is a
  prompt section (H7), so a flip moves that section's fingerprint only.

## What changes caches

Tool definitions are sorted for the provider's tool-schema cache: renaming
or re-describing a tool busts every session's cached tools. Add tools; do
not churn descriptions casually.
