import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native'
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
    exportApi: { ...actual.exportApi, start: jest.fn(), get: jest.fn() },
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
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
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
