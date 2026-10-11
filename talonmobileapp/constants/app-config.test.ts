// FM-WALLc(iv) / L73(a) — the wallet's consent prompt is the one screen
// that must establish trust, and the scaffold's name was still on it.
//
// Red before L73 (observed): AppConfig.identity.name === 'kit-expo'
// (constants/app-config.ts:4) — the authorization UI a human reads before
// approving Talon named a template instead.
//
// The URI clause is the kit's own law (assert-valid-identity-uri): a
// scheme-bearing absolute URI, or nothing. The fix must ship one.
//
// Corrected live 2026-10-10 (FM-WALLd session): Phantom 26.32 REFUSES
// authorizes carrying an unverifiable identity uri/icon — the refusal
// lands inside the wallet as `-1/authorization request failed`, with no
// consent UI (every uri+icon round on device; the workspace-40
// reference app — name-only, same kit, same Phantom — connects on the
// first press). The kit's law allows nothing: `uri == null` returns.
// The prompt's whole duty is the NAME.

import { AppConfig } from '@/constants/app-config'

// The kit is an ESM/native session module (nanostores fails to load under
// jest's CJS runtime) — mocked at the seam like every other suite here.
// Only the two factory functions app-config.ts calls at import matter.
jest.mock('@wallet-ui/react-native-kit', () => ({
  createSolanaDevnet: jest.fn((c: unknown) => ({ kind: 'devnet', ...(c as object) })),
  createSolanaTestnet: jest.fn((c: unknown) => ({ kind: 'testnet', ...(c as object) })),
}))

it('L73 — the wallet consent prompt carries Talon, not the scaffold', () => {
  expect(AppConfig.identity.name).toBe('Talon')

  // The identity ships name and nothing else: an uri or icon the wallet
  // cannot verify makes Phantom refuse the whole authorize (-1, no UI).
  // Red before this clause (observed): uri was 'http://localhost:3000'
  // with icon 'favicon.ico' — and every connect round died in Phantom.
  expect(AppConfig.identity.uri).toBeUndefined()
  expect((AppConfig.identity as { icon?: string }).icon).toBeUndefined()
})
