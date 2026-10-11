import { AppIdentity, createSolanaDevnet, createSolanaTestnet, SolanaCluster } from '@wallet-ui/react-native-kit'

// FM-WALLc(iv)/L73(a), corrected live 2026-10-10 — this identity is what
// the wallet's consent prompt shows the human being asked to approve
// Talon. It was the scaffold's `kit-expo`; it is the app's own name.
//
// It carries NOTHING else. Phantom 26.32 refuses authorizes that ship an
// identity `uri`/`icon` it cannot verify — refused inside the wallet with
// `-1/authorization request failed` and no consent UI at all (observed on
// device: every uri+icon round failed; the workspace-40 reference app —
// name-only identity, same kit, same Phantom — connects on the first
// press). The kit's own law agrees: `assert-valid-identity-uri` accepts
// an absent uri (`uri == null` returns), it only rejects malformed
// schemes. Name is the whole law: the prompt must read Talon, never a
// template, and metadata the wallet cannot check is metadata the wallet
// will not carry.
function appIdentity(): AppIdentity {
  return { name: 'Talon' }
}

export class AppConfig {
  static identity: AppIdentity = appIdentity()
  static networks: SolanaCluster[] = [
    createSolanaDevnet({ url: 'https://api.devnet.solana.com' }),
    createSolanaTestnet({ url: 'https://api.testnet.solana.com' }),
  ]
}
