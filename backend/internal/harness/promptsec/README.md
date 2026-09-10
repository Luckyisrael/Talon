# promptsec

The H7 system-prompt section registry: ordered, fingerprinted sections. See
`promptsec.go` for the full contract.

## What the model sees

- **The assembled prompt** — identical bytes to the monolith it replaced.
  The model cannot tell the registry exists.
- **What changes caches:** everything, by design — this is where the cache
  discipline lives. A section whose render changes shifts its fingerprint;
  only a change at or before the head section breaks the cached prefix.
  Late sections (memory, ADRs) move without invalidating what precedes.
