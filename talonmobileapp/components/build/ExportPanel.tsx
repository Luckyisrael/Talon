import React, { useState } from 'react';
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
  transferCheckedInstruction,
} from '@/lib/skr-transfer';

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

export function ExportPanel({ projectId }: { projectId: number }) {
  const [notes, setNotes] = useState('');
  const [sheetOpen, setSheetOpen] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [paying, setPaying] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<string | null>(null);

  const { account, connect, sendTransactions, client } = useMobileWallet();
  const quote = useQuery<SkrQuote>({ queryKey: ['skr-quote'], queryFn: () => skrApi.quote() });
  const startExport = useStartExport(projectId);
  const job = useExportJob(startExport.data?.job_id ?? null);

  const offer = quote.data;

  /** The gate: existing credits skip the whole payment path. */
  function exportRelease() {
    setFailure(null);
    if (!offer) return;
    if (offer.spare_credits >= 1) {
      startExport.mutate(notes);
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
      // Every one of these values is quoted; none is ours to pick.
      const mint = address(offer.mint);
      const treasury = address(offer.treasury);
      const who = account ?? (await connect());
      const payer = who.address;

      const raw = await packAmountInRawUnits(offer.skr_amount, mint, client.rpc);
      const [treasuryAccount, payerAccount] = await Promise.all([
        findAssociatedTokenAddress(treasury, mint),
        findAssociatedTokenAddress(payer, mint),
      ]);

      // Two instructions, one transaction: the treasury's token account may
      // not exist yet, and a transfer to a missing account just fails.
      const signature = await sendTransactions([
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
      ]);

      const outcome = await settlePayment(signature);
      if (outcome.ok) {
        setSheetOpen(false);
        startExport.mutate(notes);
      } else {
        setFailure(outcome.reason ?? 'rejected');
      }
    } catch (err) {
      const e = err as { message?: string };
      setFailure(e?.message ?? 'the wallet could not send it');
    } finally {
      setPaying(false);
    }
  }

  if (!offer) {
    return (
      <View style={styles.wrap}>
        <UIText variant="body" tone="muted">
          {quote.isError
            ? 'Could not read your export credits — try again.'
            : 'Checking your export credits…'}
        </UIText>
      </View>
    );
  }

  return (
    <View style={styles.wrap}>
      <Card>
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
          <UIText variant="body">
            {offer.skr_amount} SKR unlocks {offer.credits} exports
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
            title={`Pay ${offer.skr_amount} SKR`}
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
