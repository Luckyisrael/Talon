# tooltimeout — the cooperative per-tool call-timeout policy

Bounds how long a single tool call may hold a round. Without it a hung tool — a
gradle daemon that never answers, a fetch to a dead host, a device command
parked on a locked boot — holds its round forever, and only the round budget
eventually ends the turn. By then the person has been staring at a "running"
row for minutes with no idea which call is stuck.

## The design constraint this package exists to honour

There is **no registry-wide default**. Different tools have wildly different
honest costs: a `read_file` answers in milliseconds, a device build in minutes.
One number would either kill real work or bound nothing. The policy maps a tool
to its own budget, and only the tools it names get a deadline. An unnamed tool
keeps the old unbounded behaviour rather than inheriting a default that is wrong
for it.

This is the opposite of a blanket `context.WithTimeout` around the executor. If
you find yourself adding a default here, you are almost certainly about to kill
a legitimate ten-minute build.

## Cooperative, not fatal

"Cooperative" means the tool is *asked* to stop, not killed. The derived
context's deadline is the signal; a tool that respects `ctx` stops itself and
returns its own partial story. A tool that ignores `ctx` runs past the deadline
— the policy then reports the timeout in the result rather than pretending the
call succeeded, and the round moves on. Nothing is sent a signal it did not ask
for, and no subprocess is force-killed by this layer.

## Nested deadlines

`Derive` never reinterprets a parent deadline as this tool's timeout. If the
parent context already carries an earlier deadline, it wins and the tool reads
the expiry as an *upstream cancel* — the turn is ending — so the reported cause
is the real one. A tool that times out because its budget ran out and a tool
that stops because the turn was cancelled are different events with different
meanings, and the results must not conflate them.

## What the model sees

A call that reaches its deadline comes back as a `tool_result` with `is_error`
naming the budget that was exceeded and the fact that the output may be
incomplete. A truncated result is never presented as a success — the model is
told to re-run the tool if it needs the full output, which is the honest
alternative to silently trusting a partial answer.

## Wiring

The loop wraps `RunOptions.Execute` once with `wrapWithTimeouts`, so every call
site — the tool loop, the forced typecheck, the verification gate — runs under
the same bound. `DefaultPolicy` seeds the shipped tools' budgets; registering a
tool already registered replaces its budget, because the map is the policy.

If you add a tool, name its budget in `DefaultPolicy`. Leaving it out is a
deliberate choice (unbounded), not an oversight — but a tool that can hang is a
tool that should be named.
