import {
  type Address,
  AccountRole,
  type Instruction,
  address,
  getAddressEncoder,
  getProgramDerivedAddress,
} from '@solana/kit'

// The three Solana programs this payment touches, by their published
// addresses. These are not configuration: they are the same on every cluster,
// so nothing here can be pointed at the wrong place by an environment variable.
//
// The mint and the treasury are deliberately ABSENT. Both are quoted by the
// server (GET /v1/skr/quote) per FM-SKRb(ii) — a mint baked into the binary
// cannot be rotated and a price baked into it cannot change without a store
// release, which is exactly why the backend quotes them instead of trusting a
// `skr_amount` on the request.
export const TOKEN_PROGRAM_ADDRESS = address('TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA')
export const ATA_PROGRAM_ADDRESS = address('ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL')
export const SYSTEM_PROGRAM_ADDRESS = address('11111111111111111111111111111111')

/**
 * The token account `owner` holds for `mint`.
 *
 * Seeds are `[owner, token program, mint]` under the Associated Token Account
 * program — the layout the program derives itself, and the same one Solana
 * Mobile's own skr-staking sample builds by hand. Both seeds have to be in
 * there: drop the owner and every user's payment lands on one account; drop
 * the mint and two different tokens collide onto the same address.
 */
export async function findAssociatedTokenAddress(owner: Address, mint: Address): Promise<Address> {
  const encoder = getAddressEncoder()
  const [ata] = await getProgramDerivedAddress({
    programAddress: ATA_PROGRAM_ADDRESS,
    seeds: [encoder.encode(owner), encoder.encode(TOKEN_PROGRAM_ADDRESS), encoder.encode(mint)],
  })
  return ata
}

/**
 * Creates the destination token account if it does not already exist.
 *
 * `CreateIdempotent` (discriminant 1) rather than `Create` (0), because the
 * treasury holds a token account only once something has paid it — and `Create`
 * fails when the account is already there. Using `Create` would fix the first
 * purchase and break every one after it, on a screen that reports success.
 */
export function createAssociatedTokenAccountIdempotentInstruction(args: {
  /** Pays the rent and signs. */
  payer: Address
  /** Whose token account it is once created. */
  owner: Address
  mint: Address
  /** The associated token address, already derived. */
  ata: Address
}): Instruction {
  const { payer, owner, mint, ata } = args
  return {
    programAddress: ATA_PROGRAM_ADDRESS,
    accounts: [
      { address: payer, role: AccountRole.WRITABLE_SIGNER },
      { address: ata, role: AccountRole.WRITABLE },
      { address: owner, role: AccountRole.READONLY },
      { address: mint, role: AccountRole.READONLY },
      { address: SYSTEM_PROGRAM_ADDRESS, role: AccountRole.READONLY },
      { address: TOKEN_PROGRAM_ADDRESS, role: AccountRole.READONLY },
    ],
    data: Uint8Array.of(1),
  }
}

/**
 * Moves `amount` base units from `source` to `destination`.
 *
 * `TransferChecked` (discriminant 12) rather than plain `Transfer` (3): it
 * names the mint as account[1], so the token program refuses to move anything
 * unless both accounts hold that mint. A plain transfer would happily pay a
 * perfectly valid token account belonging to some other token if the
 * destination derivation were ever wrong — the one failure mode a client-side
 * address computation cannot otherwise catch, and it costs a single account.
 *
 * Layout is the published SPL Token one: `[u8 discriminant][u64 little-endian]`
 * over `source, mint, owner, destination`.
 */
export function transferCheckedInstruction(args: {
  source: Address
  mint: Address
  /** The authority being debited; signs, and is not written. */
  owner: Address
  destination: Address
  amount: bigint
}): Instruction {
  const { source, mint, owner, destination, amount } = args

  const data = new Uint8Array(9)
  data[0] = 12
  let remaining = amount
  for (let i = 0; i < 8; i++) {
    data[1 + i] = Number(remaining & 0xffn)
    remaining >>= 8n
  }

  return {
    programAddress: TOKEN_PROGRAM_ADDRESS,
    accounts: [
      { address: source, role: AccountRole.WRITABLE },
      { address: mint, role: AccountRole.READONLY },
      { address: owner, role: AccountRole.READONLY_SIGNER },
      { address: destination, role: AccountRole.WRITABLE },
    ],
    data,
  }
}

/** The slice of kit's RPC the pack price needs. Injected so tests can pin it. */
export type SupplyReader = {
  getTokenSupply(mint: Address): { send(): Promise<{ value: { decimals: number } }> }
}

/**
 * The quoted pack price expressed in the base units the chain actually counts.
 *
 * The exponent comes from the mint account via `getTokenSupply`, never from a
 * constant: SKR happens to be 6 decimals today, but a `6` compiled into the
 * app would send ten times the price on a 9-decimal mint and a tenth of it on
 * a 4-decimal one, and only a store release could correct either (FM-SKRb(vi)).
 *
 * Throws rather than truncating if the server ever quotes a fractional amount
 * — a price the client cannot represent is not a price it should guess at.
 */
export async function packAmountInRawUnits(
  skrAmount: number,
  mint: Address,
  rpc: SupplyReader,
): Promise<bigint> {
  const { value } = await rpc.getTokenSupply(mint).send()
  return BigInt(skrAmount) * 10n ** BigInt(value.decimals)
}
