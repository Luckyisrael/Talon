# devclient

Dev-client lifecycle: installation, rebuild detection, and async rebuilds
for the Expo dev client, plus the wallet allowlist that pins which wallet
packages a rebuild may include. Wired behind the `install_package` tool
(`tools/install.go`) and the server.

## What the model sees

- **`install_package` results.** The tool reports what was installed — or
  why it was refused (not on the allowlist, rebuild required) — in the
  tool's normal output; the model reads that verdict, not Expo CLI's
  subprocess stream.
- **Rebuild status as a fact.** When a package change needs a client
  rebuild, the model is told the rebuild is required/was queued — async
  rebuild internals, cache paths, and allowlist bookkeeping stay behind
  the package.
- **Wallet policy without wallet plumbing.** The allowlist decides which
  wallet versions may enter a rebuild; the model only ever sees the
  accept/refuse outcome.
