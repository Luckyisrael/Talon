/**
 * Build-time feature flags (Expo inlines EXPO_PUBLIC_* at bundle time).
 *
 * SHOW_TRIAGE gates the Play Billing cutover triage form: it is an internal
 * QA tool, not a user feature, so it stays out of the app unless a build
 * explicitly opts in with EXPO_PUBLIC_SHOW_TRIAGE=1.
 */
export const SHOW_TRIAGE = process.env.EXPO_PUBLIC_SHOW_TRIAGE === '1'
