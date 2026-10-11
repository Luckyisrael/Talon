// L73(d)/FM-WALLc(v) — the jest fake for `@react-native-async-storage/
// async-storage`, one source for every suite that loads `lib/walletlink`.
//
// The map lives on globalThis, not in the factory closure:
// `jest.resetModules()` re-runs the mock factory for the reloaded
// instance, and a reload simulation must see the bytes the pre-reload
// instance wrote — the disk survives the process, the closure does not.
export function asyncStorage() {
  const store = (): Map<string, string> => {
    const g = globalThis as { __talonProofStore?: Map<string, string> }
    g.__talonProofStore ??= new Map()
    return g.__talonProofStore
  }
  const impl = {
    getItem: async (k: string) => store().get(k) ?? null,
    setItem: async (k: string, v: string) => void store().set(k, v),
    removeItem: async (k: string) => void store().delete(k),
  }
  return { default: impl, ...impl }
}
