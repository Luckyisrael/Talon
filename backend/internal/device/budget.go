// Package device holds constants shared by everything that waits for an
// Android device to become usable. Three waiters used to carry three private
// budgets (45s, 60s, 90s) while real cold boot measures ~2m13s — the
// sessions waiter expired, the wallet waiter rescued it, nobody planned
// that. One budget, measured plus margin.
package device

import "time"

// BootBudget is how long a cold container may take from start to
// boot_completed=1. Measured ~2m13s on this host; 3 minutes leaves margin
// for a loaded machine without inviting an hour-long wedge (each waiter
// still applies its own per-command timeouts).
const BootBudget = 3 * time.Minute
