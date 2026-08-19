---
name: reanimated-v4
description: Build animations and gestures with Reanimated v4. Load when adding animations, transitions, shared values, or gesture-driven motion.
version: "react-native-reanimated@4.2.x, react-native-worklets@0.7.x"
autoAttach: true
triggers: [import:react-native-reanimated, import:react-native-gesture-handler]
---

# Animations with Reanimated v4

This project runs **Reanimated v4**, which is a real break from v2/v3. The most
common failure is writing v3-era code that silently no-ops or crashes.

## The v4 gotcha that breaks builds

Reanimated v4 moved worklets into a separate package, `react-native-worklets`
(installed). The Babel plugin changed accordingly. `babel.config.js` must use the
worklets plugin, listed **last**:

```js
// babel.config.js
module.exports = function (api) {
  api.cache(true);
  return {
    presets: ['babel-preset-expo'],
    plugins: ['react-native-worklets/plugin'], // v4: not 'react-native-reanimated/plugin'
  };
};
```

If animations do nothing at runtime or you see "Reanimated plugin" errors, this is
almost always why. Only touch `babel.config.js` if the task requires animation and
the plugin is missing — otherwise leave config alone (see the core rules).

## The core pattern

```tsx
import Animated, { useSharedValue, useAnimatedStyle, withTiming } from 'react-native-reanimated';
import { Pressable } from 'react-native';

const scale = useSharedValue(1);
const style = useAnimatedStyle(() => ({ transform: [{ scale: scale.value }] }));

// Read/write .value on the shared value; drive UI with an Animated.* component.
<Animated.View style={style} />
<Pressable onPressIn={() => { scale.value = withTiming(0.96); }}
           onPressOut={() => { scale.value = withTiming(1); }} />
```

## Rules

- Animate with `Animated.View` / `Animated.Text` / `Animated.ScrollView`, never a
  plain `View`, for animated styles.
- Read and write shared values through `.value`. Do not destructure `.value` outside
  a worklet and expect updates.
- Callbacks that run on the UI thread (`useAnimatedStyle`, `useAnimatedScrollHandler`)
  are worklets — keep them pure and do not call arbitrary JS-thread functions inside
  them; use `runOnJS` when you must.
- Gestures come from `react-native-gesture-handler` (installed). The app must be
  wrapped in `GestureHandlerRootView` — the root layout is the place for it.
- Confirm exact exports against `node_modules/react-native-reanimated` if unsure;
  v4 renamed and moved several APIs.
