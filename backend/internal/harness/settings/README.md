# settings

The H12 hot-reloadable model settings document. See `settings.go` for the
full contract.

## What the model sees

- **Routing changes on the next turn** — the model itself never sees the
  settings; it sees which model answered. A malformed file keeps the last
  good copy; routing never silently reverts.
