import React, { useEffect, useState } from 'react';
import { Linking, StyleSheet, View } from 'react-native';
import { useQuery } from '@tanstack/react-query';
import { address } from '@solana/kit';
import { useMobileWallet } from '@wallet-ui/react-native-kit';
import { Card, Field, GhostButton, LimeButton, MarkdownView, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';
import { useExportJob, useStartExport } from '@/hooks';
import { exportApi, skrApi, type SkrQuote, type SkrVerdict, type SubmissionBundle } from '@/lib/api';
import {
  createAssociatedTokenAccountIdempotentInstruction,
  findAssociatedTokenAddress,
  packAmountInRawUnits,
  systemTransferInstruction,
  transferCheckedInstruction,
} from '@/lib/skr-transfer';

/** How many lamports make one SOL — SOL's fixed exponent, a chain constant. */
const LAMPORTS_PER_SOL = 1_000_000_000n;

/** The quoted SOL price as a human number ("0.1"), never more decimals than lamports carry. */
function solDisplay(lamports: bigint): string {
  const whole = lamports / LAMPORTS_PER_SOL
  const frac = lamports % LAMPORTS_PER_SOL
  if (frac === 0n) return whole.toString()
  return `${whole}.${frac.toString().padStart(9, '0').replace(/0+$/, '')}`
}

// The kit's own account shape ({ address, addressBase64, icon, label }) —
// what both connect() and the hook's account carry (FM-WALLc(v)).
type WalletAccount = NonNullable<ReturnType<typeof useMobileWallet>['account']>;

// FM-SKRb — the phone's side of the release-export payment.
//
// The order of operations IS the failure mode list, so it is worth reading as
// a spec:
//
//   quote FIRST, before any affordance promises anything (ii) — the amount,
//   the mint and the treasury are read from GET /v1/skr/quote, never from
//   constants. Nothing about the offer can be baked into the binary.
//
//   spend a credit only when the account has none (ii) — `spare_credits >= 1`
//   skips the sheet and the wallet entirely. A user who already holds a
//   credit must never be asked to pay, and a user who holds none must be
//   given the offer instead of a failed request.
//
//   send ONCE, then only re-verify (v) — `sendTransactions` resolves on
//   SUBMISSION, not confirmation, so the first verify almost always answers
//   `pending`. Retrying the send would build a second transfer for one
//   purchase; the signature is kept and reused instead, which is exactly what
//   the ledger's UNIQUE tx_signature assumes a client will do.
//
//   start the build only on `verified` (iv) — a 422 carries the reason the
//   user must see and nothing is queued behind it: a credit spent on a
//   payment that did not happen is a credit stolen, and a release started
//   without one ships something the ledger never paid for.

/** How many times a submitted payment is re-checked before giving up. */
const VERIFY_ATTEMPTS = 12;

/**
 * Poll a submitted signature until the chain has a verdict.
 *
 * Each attempt is a round trip to the backend, which itself does a chain
 * lookup — so the request is its own throttle; no separate delay is needed,
 * and the bound stops a transaction that never lands from spinning forever.
 *
 * The three outcomes map onto the backend's answers:
 *   200 verified        → done
 *   200 verified:false  → not landed yet, ask again (this is `pending`, and
 *                         reading it as failure abandons a payment in flight)
 *   422                 → a COMPLETED verdict: the transaction exists and is
 *                         not this payment. `reason` on `.detail` is the whole
 *                         answer, and there is nothing to retry.
 */
async function settlePayment(signature: string): Promise<{ ok: boolean; reason?: string }> {
  for (let attempt = 0; attempt < VERIFY_ATTEMPTS; attempt++) {
    try {
      const verdict: SkrVerdict = await skrApi.verify(signature);
      if (verdict.verified) return { ok: true };
    } catch (err) {
      const e = err as { status?: number; detail?: { reason?: string } };
      if (e?.status === 422) return { ok: false, reason: e?.detail?.reason ?? 'rejected' };
      // Transport or oracle trouble proves nothing, so it must not pay:
      // fail closed by asking again until the bound runs out.
    }
  }
  return { ok: false, reason: 'not_confirmed' };
}

function reasonText(reason: string): string {
  switch (reason) {
    case 'wrong_amount':
      return 'the amount did not match the pack price';
    case 'wrong_mint':
      return 'that was not the SKR mint';
    case 'wrong_recipient':
      return 'it did not reach the export treasury';
    case 'tx_failed':
      return 'the transaction failed on chain';
    case 'incomplete':
      return 'the transaction never completed';
    case 'not_confirmed':
      return 'the network has not confirmed it yet — try again in a moment';
    default:
      return reason;
  }
}

/**
 * A 402 that escaped the pre-gate: the offer the server states in the
 * refusal itself (parseResponse parks the body on `.detail`). Its numbers
 * are fresher than the quote — the balance raced between the two — so the
 * sheet prefers them over what the panel opened with.
 */
type CreditRefusal = {
  spare_credits?: number;
  skr_amount_per_pack?: number;
  credits_per_pack?: number;
  sol_lamports_per_pack?: number;
};

/**
 * FM-SKRd(c): the reason the wire actually carried. The canned server
 * phrase adds nothing actionable, so it degrades to the status code —
 * the raw cause is on the server log since L62's logging.
 */
function quoteFailureText(error: unknown): string {
  const message =
    error instanceof Error && error.message ? error.message : 'the server was unreachable';
  const status = (error as { status?: number } | null)?.status;
  return /^could not read your export credits$/i.test(message)
    ? `Could not read your export credits (server ${status ?? 'error'}) — the raw cause is in the server log.`
    : `Could not read your export credits: ${message}`;
}

export function ExportPanel({ projectId }: { projectId: number }) {
  const [notes, setNotes] = useState('');
  const [sheetOpen, setSheetOpen] = useState(false);
  const [refusal, setRefusal] = useState<CreditRefusal | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [paying, setPaying] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);
  // FM-WALLc(v)/L73(e): what connect() returned for a first-time payer,
  // carried to the render where the charge actually runs.
  const [connectAccount, setConnectAccount] = useState<WalletAccount | null>(null);
  const [payAfterConnect, setPayAfterConnect] = useState(false);

  const { account, connect, sendTransactions, client } = useMobileWallet();
  const quote = useQuery<SkrQuote>({ queryKey: ['skr-quote'], queryFn: () => skrApi.quote() });
  const startExport = useStartExport(projectId);
  const job = useExportJob(startExport.data?.job_id ?? null);

  const offer = quote.data;

  // FM-SKRd(iii): the sheet speaks from the freshest server numbers — a
  // 402's own detail (it arrived later and IS the refusal being answered)
  // beats the quote the panel opened with; without a refusal the quote is
  // the whole story, as before.
  const live = {
    skr: refusal?.skr_amount_per_pack ?? offer?.skr_amount,
    packs: refusal?.credits_per_pack ?? offer?.credits,
    spare: refusal?.spare_credits ?? offer?.spare_credits,
    sol: refusal?.sol_lamports_per_pack ?? offer?.sol_lamports,
  };

  // The rail the SERVER quoted — never a constant here. Devnet SOL is the
  // judge-facing test rail; SKR is production billing. An absent chain means
  // an older server, which is always SKR.
  const solRail = offer?.chain === 'devnet-sol';

  // FM-SKRd(ii): a refusal must land as the offer, never as silence. This
  // is the mutation's onError — a 402 that escaped the pre-gate opens the
  // same sheet with the server's own numbers; any other failure is stated
  // on the card instead of vanishing with the pending state.
  function queueExport() {
    startExport.mutate(notes, {
      onError: (err) => {
        const e = err as { status?: number; detail?: CreditRefusal };
        if (e?.status === 402 && e.detail) {
          setRefusal(e.detail);
          setSheetOpen(true);
          return;
        }
        setFailure(err instanceof Error ? err.message : 'Could not start the export.');
      },
    });
  }

  /** The gate: existing credits skip the whole payment path. */
  function exportRelease() {
    setFailure(null);
    setRefusal(null);
    if (!offer) return;
    if (offer.spare_credits >= 1) {
      queueExport();
      return;
    }
    setSheetOpen(true);
  }

  /**
   * Take delivery of the artifact (FM-APKa). The endpoint needs the session
   * token, so this goes through the API seam rather than a link, and the
   * file lands in the share sheet instead of a sandbox nobody can browse.
   */
  async function downloadApk() {
    if (!job.data || job.data.status !== 'done') return;
    setDownloading(true);
    setDownloadError(null);
    try {
      await exportApi.downloadApk(job.data.id, `project-${projectId}-release.apk`);
    } catch (err) {
      setDownloadError(err instanceof Error ? err.message : 'Could not download the APK.');
    } finally {
      setDownloading(false);
    }
  }

  async function pay() {
    if (!offer || paying) return;
    setPaying(true);
    setFailure(null);
    try {
      // FM-WALLc(v)/L73(e): before any account is chosen, the render-time
      // `sendTransactions` is BOUND TO NULL — the kit throws "No account
      // selected" through it no matter what connect() returns. So the
      // first pay only connects, and the charge runs in the re-render
      // that connect produces (the effect below) — the handle a payment
      // signs through is always acquired AFTER the connect that made it.
      const who = account ?? connectAccount;
      if (!who) {
        const connected = await connect();
        setConnectAccount(connected);
        setPayAfterConnect(true);
        return;
      }
      await charge(who, offer);
    } catch (err) {
      const e = err as { message?: string };
      setFailure(e?.message ?? 'the wallet could not send it');
    } finally {
      setPaying(false);
    }
  }

  // The whole charge, parametrized by the account it pays from and the
  // offer it quotes — every value still read from the freshest render
  // (live.skr, sendTransactions, client), so this IS the post-connect
  // handle once it runs from the effect.
  async function charge(who: WalletAccount, offer: SkrQuote) {
    // Every one of these values is quoted; none is ours to pick. A
    // refusal's own price beats the opening quote — it arrived later.
    const treasury = address(offer.treasury);
    const payer = who.address;

    let instructions;
    if (offer.chain === 'devnet-sol') {
      // The test rail: one System transfer of the quoted lamports. The
      // price arrives in base units, so nothing is scaled here.
      const lamports = BigInt(offer.sol_lamports ?? 0);
      if (lamports <= 0n) {
        throw new Error('the server quoted no SOL price');
      }
      instructions = [
        systemTransferInstruction({ source: payer, destination: treasury, lamports }),
      ];
    } else {
      const mint = address(offer.mint);
      const raw = await packAmountInRawUnits(live.skr ?? offer.skr_amount, mint, client.rpc);
      const [treasuryAccount, payerAccount] = await Promise.all([
        findAssociatedTokenAddress(treasury, mint),
        findAssociatedTokenAddress(payer, mint),
      ]);

      // Two instructions, one transaction: the treasury's token account may
      // not exist yet, and a transfer to a missing account just fails.
      instructions = [
        createAssociatedTokenAccountIdempotentInstruction({
          payer,
          owner: treasury,
          mint,
          ata: treasuryAccount,
        }),
        transferCheckedInstruction({
          source: payerAccount,
          mint,
          owner: payer,
          destination: treasuryAccount,
          amount: raw,
        }),
      ];
    }

    const signature = await sendTransactions(instructions);

    const outcome = await settlePayment(signature);
    if (outcome.ok) {
      setSheetOpen(false);
      queueExport();
    } else {
      setFailure(outcome.reason ?? 'rejected');
    }
  }

  // The resume: this runs on the render the connect produced, so the
  // handle it closes over is the fresh one — the render-time closure that
  // opened the sheet never signs anything.
  useEffect(() => {
    if (!payAfterConnect) return;
    setPayAfterConnect(false);
    const who = account ?? connectAccount;
    if (!who || !offer) {
      setFailure('the wallet returned no account — connect and try again.');
      return;
    }
    setPaying(true);
    charge(who, offer)
      .catch((err) => {
        const e = err as { message?: string };
        setFailure(e?.message ?? 'the wallet could not send it');
      })
      .finally(() => setPaying(false));
    // The deps are the trigger states; `charge` is the render-local
    // closure BY DESIGN — the effect must sign through this render's
    // handle, not a memoized one from before the connect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [payAfterConnect, account, connectAccount, offer]);

  if (!offer) {
    return (
      <View style={styles.wrap}>
        <UIText variant="body" tone={quote.isError ? 'danger' : 'muted'}>
          {quote.isError
            ? quoteFailureText(quote.error)
            : 'Checking your export credits…'}
        </UIText>
      </View>
    );
  }

  return (
    <View style={styles.wrap}>
      <Card style={{ gap: Spacing.s12 }}>
        <UIText variant="title">Release export</UIText>
        <UIText variant="body" tone="muted">
          {offer.spare_credits} of {offer.credits} export credits left
        </UIText>
        <Field
          value={notes}
          onChangeText={setNotes}
          placeholder="What's new in this release?"
          multiline
        />
        <LimeButton title="Export" onPress={exportRelease} disabled={startExport.isPending} />
        {startExport.isPending && (
          <UIText variant="bodySm" tone="muted">
            Queueing the release build…
          </UIText>
        )}
        {job.data && (
          <UIText variant="bodySm" tone="muted">
            Release build {job.data.status}
          </UIText>
        )}
        {/* FM-SKRd(ii): a start failure that isn't a 402 still has to be
            said out loud — pending state ends silently otherwise. */}
        {failure && !sheetOpen && (
          <UIText variant="bodySm" tone="danger">{failure}</UIText>
        )}
        {/* FM-APKa(i): a paid export must be *takeable* — the file itself,
            not only the instructions for what to do with it. The web panel
            has always had this button; the phone is the surface that could
            not hand the artifact over at all. */}
        {job.data?.status === 'done' && (
          <View style={styles.download}>
            <LimeButton
              title={downloading ? 'Preparing APK…' : 'Download signed APK'}
              disabled={downloading}
              onPress={() => void downloadApk()}
            />
            {downloadError && (
              <UIText variant="bodySm" tone="danger">
                {downloadError}
              </UIText>
            )}
          </View>
        )}
        {/* FM-SUBa(i): a finished export hands over how to submit it — the
            server's own README, the package it was built under, and the
            portal to submit at. Nothing on this side is authored. */}
        {job.data?.status === 'done' && <SubmissionAssist jobId={job.data.id} />}
      </Card>

      {sheetOpen && (
        <Card style={styles.sheet}>
          <UIText variant="title">No export credits left</UIText>
          {/* FM-SKRd(iii): the system, not only the price — the balance at
              this refusal's moment, the pack it offers, what a credit
              covers, and why the payment is SKR. */}
          <UIText variant="body" tone="muted">
            Balance: {live.spare ?? 0} export credits
          </UIText>
          <UIText variant="body">
            {solRail
              ? `${solDisplay(BigInt(live.sol ?? offer.sol_lamports ?? 0))} devnet SOL unlocks ${live.packs} exports`
              : `${live.skr} SKR unlocks ${live.packs} exports`}
          </UIText>
          <UIText variant="bodySm" tone="muted">
            {solRail ? (
              <>
                One credit covers one release export (the signed APK + store
                bundle). This is the devnet test rail — it spends devnet SOL,
                which is free from any faucet, so the whole flow can be tried
                end to end. Production billing runs on SKR; a server with
                SKR configured quotes that rail instead.
              </>
            ) : (
              <>
                One credit covers one release export (the signed APK + store
                bundle). Credits are paid in SKR, Seeker&apos;s token buy a pack
                with your wallet right here in the app.
              </>
            )}
          </UIText>
          {/* Prose for the human, the raw code for verification: a payment
              refusal has to be checkable against what the chain actually
              said, and `reasonText` cannot know a reason it has never seen. */}
          {failure && (
            <UIText variant="body" tone="danger">
              The payment did not go through: {reasonText(failure)} ({failure}).
            </UIText>
          )}
          <LimeButton
            title={
              solRail
                ? `Pay ${solDisplay(BigInt(live.sol ?? offer.sol_lamports ?? 0))} devnet SOL`
                : `Pay ${live.skr ?? offer.skr_amount} SKR`
            }
            onPress={pay}
            disabled={paying}
          />
          <GhostButton title="Not now" onPress={() => setSheetOpen(false)} disabled={paying} />
        </Card>
      )}
    </View>
  );
}

/**
 * What a finished export hands over.
 *
 * FM-SUBa(i)/(ii): the bundle is written to the server's disk and served
 * verbatim, so this panel shows what the server committed to rather than
 * re-authoring it. Two things are deliberately NOT authored here:
 *
 *   the portal address — Solana Mobile documents no deep-link scheme for the
 *   Publisher Portal (publish.solanamobile.com takes no path and no query;
 *   you find the app in the sidebar after logging in), so there is nothing
 *   to deep-link with and the address must travel on the wire;
 *
 *   the package name — `solanadappstore://details?id=` exists but opens the
 *   LISTING page, which does not exist until the app is approved, so it is
 *   useless at this moment. The portal matches by package name, which is why
 *   the name is printed beside the button: it is what the user carries there.
 */
function SubmissionAssist({ jobId }: { jobId: number }) {
  const bundle = useQuery<SubmissionBundle>({
    queryKey: ['export-bundle', jobId],
    queryFn: () => exportApi.bundle(jobId),
    gcTime: 0,
  });

  if (bundle.isError) {
    return (
      <UIText variant="bodySm" tone="danger">
        Could not load the submission instructions for this release.
      </UIText>
    );
  }
  if (!bundle.data) {
    return <UIText variant="bodySm" tone="muted">Preparing the submission instructions…</UIText>;
  }

  const { manifest } = bundle.data;
  return (
    <View style={styles.assist}>
      <UIText variant="bodySm" tone="muted">
        Your package
      </UIText>
      <UIText variant="monoSm">{manifest.android_package}</UIText>
      <LimeButton
        title="Open Publisher Portal"
        onPress={() => void Linking.openURL(manifest.portal_url)}
      />
      {/* SUBMIT.md verbatim: the steps, the CLI command and the honest costs
          note the server wrote. Rendering them keeps every surface on the
          wording the server actually committed to. */}
      <MarkdownView>{bundle.data.submit_md}</MarkdownView>
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: { gap: Spacing.s12 },
  assist: { gap: Spacing.s8 },
  download: { gap: Spacing.s8 },
  sheet: { gap: Spacing.s12, borderColor: Colors.graphite, borderWidth: StyleSheet.hairlineWidth },
});
