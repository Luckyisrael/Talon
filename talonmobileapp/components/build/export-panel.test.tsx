import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native'
import { Linking } from 'react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useMobileWallet } from '@wallet-ui/react-native-kit'
import type { Instruction } from '@solana/kit'
import { ExportPanel } from '@/components/build/ExportPanel'
import { exportApi, skrApi } from '@/lib/api'

// Row L37 — FM-SKRb(ii)/(iv)/(v): the phone's side of the payment.
//
// N1 the offer must be the server's: amount, mint and treasury are all read
//    from GET /v1/skr/quote. A price or mint baked into the binary cannot be
//    changed without a store release, so the test quotes an unusual price
//    (37) against an unusual mint and asserts BOTH reach the instructions —
//    the mint the backend actually verifies against, and nothing else.
// N2 no credits → no charge, and credits → no charge: `spare_credits >= 1`
//    skips the sheet entirely, and a zero balance must open it. Paying when
//    the user already holds a credit would be a straight theft; charging a
//    broke user would be a dead end with no way out.
// N3 a rejected payment must not start the build: `wrong_amount`/`wrong_mint`/
//    `wrong_recipient` arrive on a 422 with the reason in the body, and the
//    whole point of spending a credit is that it bought something. Starting
//    anyway spends nothing and ships a release the ledger never paid for.
// N4 `sendTransactions` resolves on SUBMISSION, not on confirmation, so the
//    first verify almost always answers `pending` (200, verified:false).
//    Treating that as failure abandons a payment that is still in flight;
//    re-sending instead would build a SECOND transfer for one purchase,
//    which is exactly what the ledger's UNIQUE tx_signature exists to make
//    impossible — so the signature is kept and only re-verified.
//
// Harness notes: '@/lib/api' is partially mocked (route shapes and everything
// the hook barrel pulls stay real, the two SKR calls and export start are
// captured); '@wallet-ui/react-native-kit' is replaced wholesale because the
// wallet is a native session. RTL v14 render() is async.

const OWNER = '7jAqQXSEWNoNon45Noctg692w9cQJPdmL3vzhxYdKPFP'
const TREASURY = '5xJqQXSEWNoNon45Noctg692w9cQJPdmL3vzhxYdKPFP'
const QUOTED_MINT = '315RTJYfgHANxzwrHFodmfi6NJ6ELBN1Z55fFxTEXCtx'
// The mint the backend verifies against — must never appear unless quoted.
const BACKEND_MINT = 'SKRbvo6Gf7GondiT3BbTfuRDPqLWei4j2Qy2NPGZhW3'
const SIGNATURE = '5K9exampleSignatureAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'

jest.mock('@/lib/api', () => {
  const actual = jest.requireActual('@/lib/api')
  return {
    ...actual,
    skrApi: { quote: jest.fn(), verify: jest.fn() },
    exportApi: { ...actual.exportApi, start: jest.fn(), get: jest.fn(), bundle: jest.fn() },
  }
})

jest.mock('@wallet-ui/react-native-kit', () => ({
  useMobileWallet: jest.fn(),
}))

// The real hook barrel (useStartExport) reaches @/stores/app, which reads
// AsyncStorage — a native module with no backing store behind it in jest.
jest.mock('@react-native-async-storage/async-storage', () => ({
  default: {
    getItem: async () => null,
    setItem: async () => undefined,
    removeItem: async () => undefined,
    getAllKeys: async () => [],
  },
  getItem: async () => null,
  setItem: async () => undefined,
  removeItem: async () => undefined,
  getAllKeys: async () => [],
}))

// Row L39 — FM-APKa: two native modules the download seam talks to, and the
// session whose token rides along. Both modules are real on a device and
// absent under jest; what matters here is the CONTRACT between them — the
// header + overwrite that go INTO the download, and the file uri + APK mime
// that come OUT into the share sheet.
jest.mock('expo-file-system', () => {
  const downloadFileAsync = jest.fn(async () => ({ uri: 'file:///cache/release.apk' }))
  class File {
    constructor(..._path: unknown[]) {}
    static downloadFileAsync = downloadFileAsync
  }
  return { File, Paths: { cache: 'file:///cache/' } }
})

jest.mock('expo-sharing', () => ({
  // Named export — expo-sharing declares `shareAsync` and no default, so the
  // mock has to match the shape the real package (and the code under test)
  // actually resolves.
  shareAsync: jest.fn(async () => undefined),
}))

// The download endpoint takes a Bearer token (FM-APKa(ii)); getToken has to
// answer with something a header can be built from.
jest.mock('@/lib/auth', () => ({
  secureStoreFault: jest.fn(() => null),
  getToken: jest.fn(async () => 'test-token'),
  setToken: jest.fn(async () => undefined),
  getRefreshToken: jest.fn(async () => null),
  setRefreshToken: jest.fn(async () => undefined),
  clearToken: jest.fn(async () => undefined),
  isAuthenticated: jest.fn(async () => true),
}))

const quoteOf = (over: Partial<Record<string, unknown>> = {}) => ({
  skr_amount: 37,
  credits: 5,
  mint: QUOTED_MINT,
  treasury: TREASURY,
  spare_credits: 0,
  ...over,
})

const wallet = {
  account: undefined,
  connect: jest.fn(async () => ({ address: OWNER })),
  // The parameter is what makes `calls[0][0]` a typed instruction list rather
  // than an empty tuple — jest infers a mock's arguments from its implementation.
  sendTransactions: jest.fn(async (_instructions: readonly Instruction[]) => SIGNATURE),
  client: {
    rpc: {
      // kit's Rpc wraps every call in a request object: getTokenSupply(...)
      // returns { send() }, not a promise. Mirrors use-account-get-balance.
      getTokenSupply: jest.fn(() => ({
        send: async () => ({
          value: { decimals: 6, amount: '0', uiAmount: null, uiAmountString: '0' },
        }),
      })),
    },
  },
  chain: 'solana:mainnet',
}

async function renderPanel(projectId = 7) {
  // gcTime: 0 — a query's default 5-minute garbage-collection timer would
  // keep the event loop alive after the suite and stop jest from exiting.
  //
  // MUTATIONS carry their own gcTime, configured separately under
  // `defaultOptions.mutations`, and the pay path leaves one behind: pressing
  // Pay settles `startExport.mutate`, and the mutation cache then holds that
  // settled mutation for five minutes whether or not anything reads it again.
  // Queries-only therefore covered half the story — which is why this suite
  // printed its results and sat for five minutes whenever it was run on its
  // own, while render-only suites (and build-export, which stubs the mutation
  // hook) exited normally.
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { gcTime: 0 },
    },
  })
  const view = await render(
    <QueryClientProvider client={client}>
      <ExportPanel projectId={projectId} />
    </QueryClientProvider>,
  )
  return view
}

/** Every address named by any instruction handed to the wallet. */
function addressesSent() {
  const instructions = wallet.sendTransactions.mock.calls[0][0]
  return instructions.flatMap((ix: any) => ix.accounts.map((a: any) => String(a.address)))
}

beforeEach(() => {
  jest.clearAllMocks()
  ;(useMobileWallet as jest.Mock).mockReturnValue(wallet)
  // Once a start lands, the panel polls GET /v1/exports/:id. TanStack rejects
  // `undefined` data outright, so the stub has to answer with a real job.
  ;(exportApi.get as jest.Mock).mockResolvedValue({
    id: 41,
    project_id: 7,
    status: 'queued',
  })
})

it('prices the pack from the quote the server sent, never from a constant (FM-SKRb(ii))', async () => {
  ;(skrApi.quote as jest.Mock).mockResolvedValue(quoteOf({ spare_credits: 0 }))
  ;(skrApi.verify as jest.Mock).mockResolvedValue({
    verified: true,
    reason: 'verified',
    credits_added: 5,
    spare_credits: 5,
  })
  ;(exportApi.start as jest.Mock).mockResolvedValue({ job_id: 41 })

  await renderPanel()

  // The balance the backend reports, not an assumed 0 or 5.
  expect(await screen.findByText('0 of 5 export credits left')).toBeTruthy()

  fireEvent.press(await screen.findByText('Export'))
  // The offer names the quoted price — 37, not the SKR pack of 100.
  expect(await screen.findByText('37 SKR unlocks 5 exports')).toBeTruthy()

  fireEvent.press(await screen.findByText('Pay 37 SKR'))
  await waitFor(() => expect(wallet.sendTransactions).toHaveBeenCalledTimes(1))

  const seen = addressesSent()
  // The mint travels with the payment because the server quoted it…
  expect(seen).toContain(QUOTED_MINT)
  // …and the hardcoded one does not.
  expect(seen).not.toContain(BACKEND_MINT)
  expect(seen).toContain(TREASURY)
})

it('skips the paywall when the account already holds a credit (FM-SKRb(ii))', async () => {
  ;(skrApi.quote as jest.Mock).mockResolvedValue(quoteOf({ spare_credits: 2 }))
  ;(exportApi.start as jest.Mock).mockResolvedValue({ job_id: 41 })

  await renderPanel()

  expect(await screen.findByText('2 of 5 export credits left')).toBeTruthy()
  fireEvent.press(await screen.findByText('Export'))

  // Straight to the build — no sheet, no wallet, no chain.
  await waitFor(() => expect(exportApi.start).toHaveBeenCalledWith(7, expect.any(String)))
  expect(wallet.sendTransactions).not.toHaveBeenCalled()
  expect(screen.queryByText(/unlocks 5 exports/)).toBeNull()
})

it('a rejected payment never starts the build (FM-SKRb(iv))', async () => {
  ;(skrApi.quote as jest.Mock).mockResolvedValue(quoteOf({ spare_credits: 0 }))
  ;(skrApi.verify as jest.Mock).mockRejectedValue(
    Object.assign(new Error('the payment is not this pack'), {
      status: 422,
      detail: {
        verified: false,
        reason: 'wrong_amount',
        credits_added: 0,
        spare_credits: 0,
      },
    }),
  )
  ;(exportApi.start as jest.Mock).mockResolvedValue({ job_id: 41 })

  await renderPanel()

  fireEvent.press(await screen.findByText('Export'))
  fireEvent.press(await screen.findByText('Pay 37 SKR'))

  // The verdict is shown as itself, and no release is kicked off behind it.
  expect(await screen.findByText(/wrong_amount/)).toBeTruthy()
  expect(exportApi.start).not.toHaveBeenCalled()
})

it('a pending payment re-verifies the same signature instead of resending (FM-SKRb(v))', async () => {
  ;(skrApi.quote as jest.Mock).mockResolvedValue(quoteOf({ spare_credits: 0 }))
  ;(skrApi.verify as jest.Mock)
    .mockResolvedValueOnce({ verified: false, reason: 'pending', credits_added: 0, spare_credits: 0 })
    .mockResolvedValueOnce({ verified: true, reason: 'verified', credits_added: 5, spare_credits: 5 })
  ;(exportApi.start as jest.Mock).mockResolvedValue({ job_id: 41 })

  await renderPanel()

  fireEvent.press(await screen.findByText('Export'))
  fireEvent.press(await screen.findByText('Pay 37 SKR'))

  await waitFor(() => expect(exportApi.start).toHaveBeenCalledWith(7, expect.any(String)))

  // One transfer was ever built…
  expect(wallet.sendTransactions).toHaveBeenCalledTimes(1)
  // …and both asks named the signature that transfer produced.
  expect(skrApi.verify).toHaveBeenCalledTimes(2)
  const asked = (skrApi.verify as jest.Mock).mock.calls.map((c) => c[0])
  expect(asked[0]).toBe(SIGNATURE)
  expect(asked[1]).toBe(SIGNATURE)
  expect(asked[0]).toBe(asked[1])
})

// Row L38 — FM-SUBa(i)/(ii): a paid export has to hand over HOW to submit it.
//
// The bundle is written by the server and served by the server; the phone's
// only job is to show what it says. The package below is a value this panel
// has no other way to obtain, so finding it on screen proves the bundle
// arrived. The portal address is deliberately one no client would have
// hardcoded — pressing the button can only reach it through
// `manifest.portal_url`, which is the drift FM-SUBa(ii) names.
const PORTAL = 'https://publish.example.test/portal'
const PACKAGE = 'com.talon.l38demo'

const servedBundle = () => ({
  submit_md: [
    '# Submit this release to the Solana dApp Store',
    '',
    'Talon built and signed this APK; submission is yours, with your own wallet.',
    '',
    '## Option A — Publisher Portal (recommended for a first release)',
    '',
    `1. Sign up at ${PORTAL} and complete KYC/KYB.`,
    `2. Create the app in the portal if it does not exist yet (the portal matches`,
    `   it by package name \`${PACKAGE}\`).`,
    '3. Upload this APK as a new version and approve the signing requests.',
  ].join('\n'),
  manifest: {
    android_package: PACKAGE,
    portal_url: PORTAL,
    costs_note:
      'Publishing to the Solana dApp Store costs real SOL: keep ~0.2 SOL in your publisher wallet.',
    publish_command: 'dapp-store --apk-file ./release.apk --keypair <publisher-keypair.json>',
  },
})

/** Drive the panel to the state a finished export leaves behind. */
async function renderFinishedExport() {
  ;(skrApi.quote as jest.Mock).mockResolvedValue(quoteOf({ spare_credits: 5 }))
  ;(exportApi.start as jest.Mock).mockResolvedValue({ job_id: 41 })
  ;(exportApi.get as jest.Mock).mockResolvedValue({ id: 41, project_id: 7, status: 'done' })
  ;(exportApi.bundle as jest.Mock).mockResolvedValue(servedBundle())

  await renderPanel()
  // A balance of 5 spends nothing — the export goes straight to the build.
  fireEvent.press(await screen.findByText('Export'))
}

it('a finished export hands over the submission bundle, not just a status (FM-SUBa(i))', async () => {
  await renderFinishedExport()

  // The done branch used to print only "Release build done": an APK with no
  // idea what to do with it, because SUBMIT.md lives on the server's disk.
  expect(await screen.findByText('Open Publisher Portal')).toBeTruthy()
  expect(exportApi.bundle).toHaveBeenCalledWith(41)
})

it('the package name sits beside the portal button, and the link is the one the server sent (FM-SUBa(ii))', async () => {
  await renderFinishedExport()

  // A package this panel cannot know on its own — only the bundle carries it.
  // findAll: the README quotes it too, and markdown renders that separately.
  expect((await screen.findAllByText(PACKAGE)).length).toBeGreaterThan(0)

  const openURL = jest.spyOn(Linking, 'openURL').mockResolvedValue(true)
  fireEvent.press(screen.getByText('Open Publisher Portal'))
  expect(openURL).toHaveBeenCalledWith(PORTAL)
})

// Row L39 — FM-APKa: the artifact has to reach the human who paid for it.
//
// N1 the done branch is where the user learns the build finished, and a
//    finished build with no way to take the file is a build they cannot use
//    (FM-APKa(i)). The web panel has had this button all along.
// N2 the download endpoint needs the session token, which no link can carry
//    (FM-APKa(ii) — the web panel says so in its own comment), and
//    expo-file-system refuses a second write to the same name unless it is
//    told to overwrite (FM-APKa(iv)). The hand-off to the share sheet is what
//    makes the bytes reachable at all (FM-APKa(iii)) — a file written into
//    the app's sandbox is not a download the user can open.
it('a finished export offers the signed APK it was paid for (FM-APKa(i))', async () => {
  await renderFinishedExport()

  // Today the done branch prints a status line and the submission assist:
  // it says HOW to submit and hands over nothing to submit.
  expect(await screen.findByText('Download signed APK')).toBeTruthy()
})

it('the download carries the session token and shares the result (FM-APKa(ii)/(iii)/(iv))', async () => {
  const { File } = jest.requireMock('expo-file-system') as {
    File: { downloadFileAsync: jest.Mock }
  }
  const Sharing = jest.requireMock('expo-sharing') as { shareAsync: jest.Mock }

  const uri = await exportApi.downloadApk(41, 'project-7-release.apk')

  const call = File.downloadFileAsync.mock.calls[0] as [
    string,
    unknown,
    { headers?: Record<string, string>; idempotent?: boolean },
  ]
  expect(File.downloadFileAsync).toHaveBeenCalledTimes(1)
  // The endpoint the server registered, not a URL typed beside it.
  expect(call[0]).toContain('/v1/exports/41/download')
  // The session rides the request, or the endpoint answers 401.
  expect(call[2].headers?.Authorization).toBe('Bearer test-token')
  // Otherwise the SECOND export throws on a name the device still holds.
  expect(call[2].idempotent).toBe(true)

  const shared = Sharing.shareAsync.mock.calls[0] as [string, { mimeType?: string }]
  expect(Sharing.shareAsync).toHaveBeenCalledTimes(1)
  // The bytes leave the sandbox — that is the whole point of iii.
  expect(shared[0]).toBe('file:///cache/release.apk')
  expect(String(shared[1].mimeType)).toContain('android.package-apk')
  expect(uri).toBe('file:///cache/release.apk')
})

it('L66 — a 402 that escapes the pre-gate lands as the full offer, never silence (FM-SKRd(ii))', async () => {
  // The quote says a credit is held, so the gate passes straight through —
  // and the server then refuses: the balance raced (spent elsewhere since
  // the quote). Pre-L66 the mutation had no onError, so this refusal
  // rendered NOTHING — silence where an offer belonged.
  ;(skrApi.quote as jest.Mock).mockResolvedValue(quoteOf({ spare_credits: 1 }))
  ;(exportApi.start as jest.Mock).mockRejectedValue(
    Object.assign(new Error('no export credits left'), {
      status: 402,
      detail: {
        code: 'credits_required',
        spare_credits: 0,
        skr_amount_per_pack: 41,
        credits_per_pack: 6,
      },
    }),
  )

  await renderPanel()

  expect(await screen.findByText('1 of 5 export credits left')).toBeTruthy()
  fireEvent.press(await screen.findByText('Export'))

  // The refusal IS the offer — the sheet opens on the server's own numbers.
  expect(await screen.findByText('No export credits left')).toBeTruthy()
  expect(screen.getByText('41 SKR unlocks 6 exports')).toBeTruthy()
  // …and it teaches the system, not only the price (FM-SKRd(iii)):
  // what a credit covers, the balance at refusal time, why SKR.
  expect(screen.getByText(/One credit covers one release export/)).toBeTruthy()
  expect(screen.getByText('Balance: 0 export credits')).toBeTruthy()
  expect(screen.getByText(/paid in SKR/)).toBeTruthy()
})

it('L66 — a quote failure shows the reason the wire carried, not only "try again" (FM-SKRd(i))', async () => {
  ;(skrApi.quote as jest.Mock).mockRejectedValue(new Error('the credits ledger is offline'))

  await renderPanel()

  expect(await screen.findByText(/the credits ledger is offline/)).toBeTruthy()
})

// FM-WALLc(v) / L73(e) — the FIRST pay: no account is chosen at render, so
// the kit's sendTransactions is bound to null and throws "No account
// selected" (index.native.mjs:483) no matter what connect() later returns.
// The send must run through the handle the connect produced — the render
// AFTER it — never the render-time closure.
//
// Red before L73 (observed): pay() destructured sendTransactions at render,
// awaited connect() mid-function, then signed through the stale closure →
// 'No account selected', exportApi.start never called.
it('L73 — the first pay acquires the wallet handle AFTER connecting (FM-WALLc(v))', async () => {
  ;(skrApi.quote as jest.Mock).mockResolvedValue(quoteOf({ spare_credits: 0 }))
  ;(skrApi.verify as jest.Mock).mockResolvedValue({
    verified: true,
    reason: 'verified',
    credits_added: 5,
    spare_credits: 5,
  })
  ;(exportApi.start as jest.Mock).mockResolvedValue({ job_id: 41 })

  // A handle that changes across renders, exactly like the real one: the
  // pre-connect instance has no account and throws; once connect() has
  // chosen an account, the next render's instance sends.
  const session = { connected: false }
  const stale = {
    account: undefined,
    connect: jest.fn(async () => {
      session.connected = true
      return { address: OWNER }
    }),
    sendTransactions: jest.fn(async (_instructions: readonly Instruction[]) => {
      throw new Error('No account selected')
    }),
    client: wallet.client,
    chain: 'solana:devnet',
  }
  const fresh = {
    account: { address: OWNER },
    connect: stale.connect,
    sendTransactions: jest.fn(async (_instructions: readonly Instruction[]) => SIGNATURE),
    client: wallet.client,
    chain: 'solana:devnet',
  }
  ;(useMobileWallet as jest.Mock).mockImplementation(() => (session.connected ? fresh : stale))

  await renderPanel()

  fireEvent.press(await screen.findByText('Export'))
  fireEvent.press(await screen.findByText('Pay 37 SKR'))

  // The payment runs through the post-connect handle and completes…
  await waitFor(() => expect(fresh.sendTransactions).toHaveBeenCalledTimes(1))
  // …the render-time one was never asked to sign…
  expect(stale.sendTransactions).not.toHaveBeenCalled()
  // …and the export it paid for starts.
  await waitFor(() => expect(exportApi.start).toHaveBeenCalledTimes(1))
})
