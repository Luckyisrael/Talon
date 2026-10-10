import { AppIdentity, createSolanaDevnet, createSolanaTestnet, SolanaCluster } from '@wallet-ui/react-native-kit'

// FM-WALLc(iv)/L73(a) — this identity is what the wallet's consent prompt
// shows the human being asked to approve Talon. It was the scaffold's
// `kit-expo`; now it is the app's own name, the web origin the operator
// configured (the same env-first shape docs.tsx resolves URLs with), and —
// only where a web origin can actually serve one — the favicon that MWA
// resolves relative to uri (frontend/app/favicon.ico).
function appIdentity(): AppIdentity {
  const explicit = (process.env.EXPO_PUBLIC_WEB_URL ?? '').trim().replace(/\/+$/, '')
  const api = (process.env.EXPO_PUBLIC_API_URL ?? '').trim()
  const fromApi = api.match(/^(https?:\/\/[^/:]+)/i)
  const uri = explicit || (fromApi ? `${fromApi[1]}:3000` : 'talon://app')
  // AppIdentity is readonly — declared whole: the icon only where a web
  // origin can actually serve it.
  return /^https?:/i.test(uri)
    ? { name: 'Talon', uri, icon: 'favicon.ico' }
    : { name: 'Talon', uri }
}

export class AppConfig {
  static identity: AppIdentity = appIdentity()
  static networks: SolanaCluster[] = [
    createSolanaDevnet({ url: 'https://api.devnet.solana.com' }),
    createSolanaTestnet({ url: 'https://api.testnet.solana.com' }),
  ]
}
