# redroid

The server-side Android device pool: docker-backed redroid containers,
port allocation (`PortAllocator` for the adb and relay ranges), a warm
pool, and reconciliation that keeps containers and requests in step.
Built for the device gates and the e2e run — booted at server start,
never from inside a turn.

## What the model sees

- **A device, or the absence of one.** When the pool hands the server a
  target, `DeviceAttached` flips and boot/vision/interact become required
  gates for that turn; without one they are classified best-effort and
  `SkippedGates` says so. The model sees that classification, never
  docker.
- **Gate results only.** Boot/vision/interact outcomes arrive as ordinary
  gate lines in the ladder — container ids, ports, and pool state stay in
  the server log.
