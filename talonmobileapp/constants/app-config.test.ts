// FM-WALLc(iv) / L73(a) — the wallet's consent prompt is the one screen
// that must establish trust, and the scaffold's name was still on it.
//
// Red before L73 (observed): AppConfig.identity.name === 'kit-expo'
// (constants/app-config.ts:4) — the authorization UI a human reads before
// approving Talon named a template instead.
//
// The URI clause is the kit's own law (assert-valid-identity-uri): a
// scheme-bearing absolute URI, or nothing. The fix must ship one.

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

  const uri = AppConfig.identity.uri
  expect(typeof uri).toBe('string')
  expect(String(uri)).toMatch(/^[A-Za-z][A-Za-z0-9+.-]*:/)
})
