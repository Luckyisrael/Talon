/**
 * Build-time feature flags (Next inlines NEXT_PUBLIC_* at build time).
 *
 * SHOW_TRIAGE gates the Play Billing cutover triage panel: it is an internal
 * QA tool, not a user feature, so it stays out of the dock unless a build
 * explicitly opts in with NEXT_PUBLIC_SHOW_TRIAGE=1.
 */
export const SHOW_TRIAGE = process.env.NEXT_PUBLIC_SHOW_TRIAGE === "1";
