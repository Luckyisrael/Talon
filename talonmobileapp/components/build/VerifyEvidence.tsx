import React from 'react'
import { StyleSheet, View } from 'react-native'
import { UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import type { ConversationBlock, TurnEvidence } from '@/lib/api'

// VerifyEvidenceRow is the mobile twin of the web's TurnFooter: the line that
// says what a turn changed and how it was checked. Vocabulary is copied
// verbatim so both clients make the same claim in the same words (hooks law):
//   verified · skipped: a, b | verified (best effort) | verified
//   checks did not pass | N left | N file(s) | N round(s) | $X.XXX
//   detail: verify policy: p · did not run: g · left: t
// ABSENT keys claim nothing in either direction — `verified`'s PRESENCE is the
// flag (a legacy marker predating the verify payload renders rounds only; an
// empty-but-present payload means "finished, nothing to verify"). Web anchor:
// frontend/components/ide/AIChat.tsx TurnFooter (rows 44/45).

export function hasEvidence(e: TurnEvidence): boolean {
  return (
    e.verified !== undefined ||
    (e.filesTouched?.length ?? 0) > 0 ||
    (e.remainingTasks?.length ?? 0) > 0 ||
    e.costUsd !== undefined ||
    e.rounds !== undefined
  )
}

/** turn_end marker → rendered evidence, with the absence rules: the verify
 *  payload when present; rounds alone on a completed marker (legacy — claims
 *  nothing); nothing otherwise — a non-clean ending already says its rounds in
 *  the status row, so they are not repeated here (the web's footer would show
 *  them twice; mobile's status row owns that half of the sentence).
 *
 *  L54 / FM-VRFYa — a payload present but empty of work claims NOTHING:
 *  "finished, nothing to verify" is the documented meaning of an
 *  empty-but-present payload (the comment at the top), so the row does not
 *  mount on its rounds, and an old marker carrying `verified:false` with
 *  zero files and no policy — the exact shape every no-changes turn was
 *  written with — stops claiming a check that never ran. A false verdict
 *  WITH work (files, policy, skipped gates) stays claimable: that one is
 *  real (F3). The payload-less legacy branch is untouched (F2). */
export function evidenceFromMarker(block: ConversationBlock): TurnEvidence | undefined {
  const rounds = block.status === 'completed' ? block.rounds : undefined
  if (block.verify) {
    const files = block.verify.files_touched ?? []
    const skips = block.verify.skipped_gates ?? []
    const policy = block.verify.verify_policy
    const hasWork = files.length > 0 || skips.length > 0 || !!policy
    const verdict = hasWork || block.verify.verified === true ? block.verify.verified : undefined
    if (verdict === undefined && !hasWork) return undefined // finished, nothing to verify
    return {
      filesTouched: files,
      verified: verdict,
      verifiedBestEffort: verdict !== undefined ? block.verify.verified_best_effort : undefined,
      verifyPolicy: policy,
      skippedGates: skips,
      rounds,
    }
  }
  return rounds !== undefined ? { rounds } : undefined
}

export function VerifyEvidenceRow({ evidence }: { evidence: TurnEvidence }) {
  if (!hasEvidence(evidence)) return null
  const skips = evidence.skippedGates ?? []
  const remaining = evidence.remainingTasks ?? []

  const chips: string[] = []
  let tone: 'success' | 'accent' | 'danger' | 'muted' = 'muted'
  if (evidence.verified === true) {
    tone = evidence.verifiedBestEffort || skips.length > 0 ? 'accent' : 'success'
    chips.push(
      skips.length
        ? `verified · skipped: ${skips.join(', ')}`
        : evidence.verifiedBestEffort
          ? 'verified (best effort)'
          : 'verified',
    )
  } else if (evidence.verified === false) {
    tone = 'danger'
    chips.push('checks did not pass')
  }
  if (remaining.length) chips.push(`${remaining.length} left`)

  const parts: string[] = []
  const files = evidence.filesTouched?.length ?? 0
  if (files) parts.push(`${files} ${files === 1 ? 'file' : 'files'}`)
  if (evidence.rounds) parts.push(`${evidence.rounds} ${evidence.rounds === 1 ? 'round' : 'rounds'}`)
  if (evidence.costUsd) parts.push(`$${evidence.costUsd.toFixed(3)}`)

  const nodes = [...chips, ...parts]
  if (nodes.length === 0) return null

  // Web puts these qualifiers in the chip's hover title; touch has no hover,
  // so they sit on their own line — color never carries the claim alone.
  const bits: string[] = []
  if (evidence.verifyPolicy) bits.push(`verify policy: ${evidence.verifyPolicy}`)
  if (skips.length) bits.push(`did not run: ${skips.join(', ')}`)
  if (remaining.length) bits.push(`left: ${remaining.join(', ')}`)

  return (
    <View style={styles.row} accessibilityLabel="turn verify evidence">
      <UIText variant="caption" tone={tone}>
        {nodes.join(' · ')}
      </UIText>
      {bits.length ? (
        <UIText variant="micro" tone="faint">
          {bits.join(' · ')}
        </UIText>
      ) : null}
    </View>
  )
}

const styles = StyleSheet.create({
  row: {
    borderLeftWidth: 2,
    borderLeftColor: Colors.graphite,
    paddingLeft: Spacing.s8,
    paddingVertical: 2,
    gap: 2,
  },
})
