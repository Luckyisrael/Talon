// Shared native-module seams for suites that render app screens on a machine
// with no device attached. Each export is a FACTORY: jest.mock() factories are
// hoisted above every import, so the work has to happen when jest calls the
// factory, not when this module is first evaluated.
//
// Currently: `react-native-reanimated`. It boots native Worklets at import
// time (build.tsx → Transcript → TraceRow), and even reanimated's own mock.js
// transitively re-requires src/index, which boots worklets and throws
// "Native part of Worklets doesn't seem to be initialized". Breaking the chain
// here gives chainable layout animations, RN primitives for Animated.View and
// no-op hooks — the surface the app actually uses (Animated.View,
// FadeInDown.duration, useSharedValue-style hooks).

export function reanimated() {
  // `require` is the only escape from a hoisted factory: a static import is
  // re-ordered, and an outer-scope reference is forbidden by jest's transform.
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const RN = require('react-native')

  // Every layout-animation expression (FadeInDown.duration(200).springify())
  // collapses to the same chainable value.
  const chain: any = new Proxy(
    function () {
      return chain
    },
    { get: () => () => chain, apply: () => chain },
  )

  const Animated = {
    View: RN.View,
    Text: RN.Text,
    Image: RN.Image,
    ScrollView: RN.ScrollView,
    createAnimatedComponent: (c: unknown) => c,
  }

  return {
    __esModule: true,
    default: Animated,
    FadeIn: chain,
    FadeInDown: chain,
    FadeInUp: chain,
    FadeOut: chain,
    FadeOutUp: chain,
    FadeOutDown: chain,
    Layout: chain,
    SlideInRight: chain,
    SlideOutRight: chain,
    useSharedValue: (v: unknown) => ({ value: v }),
    useAnimatedStyle: (fn: () => unknown) => {
      try {
        return fn()
      } catch {
        return {}
      }
    },
    withTiming: (v: unknown) => v,
    withSpring: (v: unknown) => v,
    useReducedMotion: () => false,
    runOnJS: (fn: unknown) => fn,
    runOnUI: (fn: unknown) => fn,
  }
}
