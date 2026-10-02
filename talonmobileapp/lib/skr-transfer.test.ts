import { type AccountMeta, AccountRole, address } from '@solana/kit'
import {
  ATA_PROGRAM_ADDRESS,
  SYSTEM_PROGRAM_ADDRESS,
  TOKEN_PROGRAM_ADDRESS,
  createAssociatedTokenAccountIdempotentInstruction,
  findAssociatedTokenAddress,
  packAmountInRawUnits,
  transferCheckedInstruction,
} from '@/lib/skr-transfer'

// Row L37 — FM-SKRb(iii)/(vi): the phone builds the payment itself, so the
// two things that cannot fail are pinned before anything is written.
//
// M1 the transfer must name the mint: Transfer (3) carries source/owner/dest
//    and nothing else, so a wrong destination derivation pays a valid account
//    of a *different* token and the loss is silent. TransferChecked (12)
//    carries the mint as account[1], so a mismatched destination fails
//    on-chain instead — which is the only defence a client-side derivation
//    has, and it costs one account.
// M2 the destination must exist first: a treasury that has never been paid
//    holds no token account, and a transfer to a missing account just fails.
//    It must be created **idempotently** (discriminant 1), never `Create` (0)
//    — `Create` fails when the account is already there, so the SECOND
//    purchase would break something the first one fixed.
// M3 the exponent must come from the chain: SKR happens to be 6 today, but a
//    constant baked into the binary sends 10× the price on a 9-decimal mint
//    and 1/10 of it on a 4-decimal one, and only a store release could fix
//    either. The pack is repriced from `getTokenSupply` every time.
// M4 the derivation must actually use both seeds: an ATA keyed on the owner
//    alone would send every user's payment to one account, and one keyed on
//    the mint alone would confuse two users holding the same token.
//
// The layouts asserted here are the published SPL Token / Associated Token
// Account ones — `[u8 discriminant][u64 little-endian]` over a fixed account
// list. They are checked field by field because a wrong discriminant or a
// dropped account does not fail on the phone: it fails on-chain, after the
// user has already tapped Pay.
//
// Harness note: these assertions compare BigInt, and jest's worker cannot
// JSON-serialize a BigInt — a FAILING case reports "Do not know how to
// serialize a BigInt" instead of the diff. Read it with
// `npm test -- skr-transfer --runInBand`.

const OWNER = address('7jAqQXSEWNoNon45Noctg692w9cQJPdmL3vzhxYdKPFP')
const SOURCE = address('315RTJYfgHANxzwrHFodmfi6NJ6ELBN1Z55fFxTEXCtx')
const DESTINATION = address('4Eznt46MdtGdqsCbX3wwjG8tbe7y4c3wjxmMf196gXg1')
const OTHER_OWNER = address('D9FdALxxRCueePMJP1c5SvVs7GkKDw4M2mvCeUqhfpp6')
const TREASURY = address('5xVjGqNDVgY5xEwRDdp7gdzdJguAnzv4xnYfup4XsMzP')
const OTHER_MINT = address('vyUryFPQZz7idWLb4FehEwXsPXXKewn2NmtXL2Fqa3k')
// The mint is a FIXTURE HERE, not an export of lib/skr-transfer: the module
// must not carry one, because the server quotes the mint (FM-SKRb(ii)). A
// constant shipped in the app could not be rotated without a store release.
const SKR_MINT = address('SKRbvo6Gf7GondiT3BbTfuRDPqLWei4j2Qy2NPGZhW3')

/** Reads a little-endian u64. `ArrayLike` so it accepts kit's ReadonlyUint8Array. */
function littleEndianU64(data: ArrayLike<number>, offset: number): bigint {
  let out = 0n
  for (let i = 7; i >= 0; i--) out = (out << 8n) | BigInt(data[offset + i])
  return out
}

function supplyRpc(decimals: number) {
  return {
    getTokenSupply: () => ({
      send: async () => ({
        value: { decimals, amount: '0', uiAmount: null, uiAmountString: '0' },
      }),
    }),
  }
}

it('transfer carries the mint and the amount, not just the pair (FM-SKRb(iii))', () => {
  const ix = transferCheckedInstruction({
    source: SOURCE,
    mint: SKR_MINT,
    owner: OWNER,
    destination: DESTINATION,
    amount: 100_000_000n,
  })

  expect(ix.programAddress).toBe(TOKEN_PROGRAM_ADDRESS)
  // kit types accounts/data as OPTIONAL — an instruction may legitimately carry
  // neither. These builders always carry both, and proving so is part of what
  // the assertions below check. The `!` silences the compiler only: a missing
  // field still throws on the very next line, so it cannot mask a failure.
  const data = ix.data!
  const accounts = ix.accounts! as readonly AccountMeta[]

  // 12 = TransferChecked. 3 = Transfer, which cannot catch a bad destination.
  // Pinned as the published literal, not read back from the module, so a
  // builder that emits the wrong discriminant cannot agree with its own constant.
  expect(data[0]).toBe(12)
  // The mint in position 1 is the whole reason for the choice.
  expect(accounts.map((a) => a.address)).toEqual([SOURCE, SKR_MINT, OWNER, DESTINATION])
  expect(littleEndianU64(data, 1)).toBe(100_000_000n)

  expect(accounts[0].role).toBe(AccountRole.WRITABLE) // source is debited
  expect(accounts[3].role).toBe(AccountRole.WRITABLE) // destination is credited
  expect(accounts[2].role).toBe(AccountRole.READONLY_SIGNER) // owner signs, is not written
})

it('the treasury account is created idempotently, so a second purchase cannot fail (FM-SKRb(iii))', () => {
  const ix = createAssociatedTokenAccountIdempotentInstruction({
    payer: OWNER,
    owner: TREASURY,
    mint: SKR_MINT,
    ata: DESTINATION,
  })

  expect(ix.programAddress).toBe(ATA_PROGRAM_ADDRESS)
  const data = ix.data!
  const accounts = ix.accounts! as readonly AccountMeta[]

  expect(data.length).toBe(1)
  // 1 = CreateIdempotent. 0 = Create would fail once the account exists —
  // i.e. the second payment would break what the first one just created.
  // The published literal, not the module's own constant.
  expect(data[0]).toBe(1)

  expect(accounts.map((a) => a.address)).toEqual([
    OWNER, // payer: signs and funds the rent
    DESTINATION, // the account being created
    TREASURY, // whose account it is
    SKR_MINT,
    SYSTEM_PROGRAM_ADDRESS,
    TOKEN_PROGRAM_ADDRESS,
  ])
  expect(accounts[0].role).toBe(AccountRole.WRITABLE_SIGNER)
  expect(accounts[1].role).toBe(AccountRole.WRITABLE)
  expect(accounts.slice(2).every((a) => a.role === AccountRole.READONLY)).toBe(true)
})

it('the pack amount comes from the mint decimals, never a constant (FM-SKRb(vi))', async () => {
  // One price, three different mints. A hardcoded 6 passes only the first.
  expect(await packAmountInRawUnits(100, SKR_MINT, supplyRpc(6))).toBe(100_000_000n)
  expect(await packAmountInRawUnits(100, SKR_MINT, supplyRpc(9))).toBe(100_000_000_000n)
  expect(await packAmountInRawUnits(100, SKR_MINT, supplyRpc(4))).toBe(1_000_000n)
  // The quantity is quoted by the server, not assumed to be 100.
  expect(await packAmountInRawUnits(37, SKR_MINT, supplyRpc(6))).toBe(37_000_000n)
})

it('the associated token address answers to both owner and mint (FM-SKRb(iii))', async () => {
  const theirs = await findAssociatedTokenAddress(TREASURY, SKR_MINT)

  // Deterministic — the same payment must land on the same account twice.
  expect(await findAssociatedTokenAddress(TREASURY, SKR_MINT)).toBe(theirs)
  // Both seeds are in play: dropping either one collapses unrelated accounts.
  expect(await findAssociatedTokenAddress(TREASURY, OTHER_MINT)).not.toBe(theirs)
  expect(await findAssociatedTokenAddress(OTHER_OWNER, SKR_MINT)).not.toBe(theirs)
  // A token account is a program-derived address, never the owner's own.
  expect(String(theirs)).not.toBe(String(TREASURY))
})
