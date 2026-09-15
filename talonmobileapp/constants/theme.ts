// Talon midnight tokens for React Native — translated from DESIGN_MOBILE.md.
// Single source of truth for every color, type style, spacing step, and
// radius in the companion app. Values are identical to the web system;
// only the units changed (RN points, not px/em).
export const Colors = {
  void: '#08090a',
  carbon: '#0f1011',
  obsidian: '#161718',
  graphite: '#23252a',
  smoke: '#383b3f',
  ash: '#62666d',
  fog: '#8a8f98',
  mist: '#d0d6e0',
  bone: '#e5e5e6',
  paper: '#ffffff',
  acidLime: '#e4f222',
  // 1px bottom edge on the CTA — replaces the web inset-shadow stack,
  // which React Native cannot render.
  acidLimeEdge: '#c9d91e',
  acidLimeInk: '#08090a',
  pulseGreen: '#27a644',
  coralRed: '#eb5757',
  signalTeal: '#02b8cc',
  irisViolet: '#6366f1',
  lavender: '#8b5cf6',
  // Amber is not a palette color — it is the "partial" state (verified with
  // skipped gates), copy-matched to the web's partial-verified line.
  amber: '#f5a623',
} as const;

export type ColorName = keyof typeof Colors;

const inter400: '400' = '400';
const inter500: '500' = '500';
const inter600: '600' = '600';
const inter700: '700' = '700';

// Tracking converted from web em to RN points (em × fontSize), per
// DESIGN_MOBILE.md. Base family resolves in lib/fonts.ts (loaded Inter,
// else system fallback — never blocks render).
export const Type = {
  display: { fontSize: 40, lineHeight: 42, letterSpacing: -0.88, fontWeight: inter700 },
  heading: { fontSize: 32, lineHeight: 35, letterSpacing: -0.7, fontWeight: inter700 },
  subheading: { fontSize: 24, lineHeight: 30, letterSpacing: -0.29, fontWeight: inter600 },
  title: { fontSize: 20, lineHeight: 26, letterSpacing: -0.24, fontWeight: inter600 },
  bodyLg: { fontSize: 17, lineHeight: 26, fontWeight: inter400 },
  body: { fontSize: 16, lineHeight: 24, fontWeight: inter400 },
  bodySm: { fontSize: 15, lineHeight: 23, letterSpacing: -0.17, fontWeight: inter400 },
  caption: { fontSize: 13, lineHeight: 18, fontWeight: inter400 },
  label: { fontSize: 12, lineHeight: 17, fontWeight: inter400 },
  micro: { fontSize: 10, lineHeight: 15, letterSpacing: 0.4, fontWeight: inter500 },
  mono: { fontSize: 12, lineHeight: 18, fontFamily: 'JetBrainsMono_400Regular' },
  monoSm: { fontSize: 10, lineHeight: 15, fontFamily: 'JetBrainsMono_400Regular' },
} as const;

export type TextStyleName = keyof typeof Type;

export const Spacing = {
  s4: 4,
  s8: 8,
  s12: 12,
  s16: 16,
  s20: 20,
  s24: 24,
  s32: 32,
  s48: 48,
  s64: 64,
} as const;

export const Radii = {
  small: 2,
  badges: 4,
  buttons: 6,
  inputs: 6,
  cards: 12,
  pills: 9999,
} as const;

// Layout constants from DESIGN_MOBILE.md: 16px screen padding, 24–32px
// section gaps, 44pt minimum touch target, hairline structural borders.
export const Layout = {
  screenPadding: 16,
  sectionGap: 28,
  cardPadding: 16,
  elementGap: 8,
  minTouch: 44,
  primaryButtonHeight: 48,
} as const;
