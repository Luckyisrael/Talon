import React from 'react'
import { Dimensions, Pressable, StyleSheet, Text, View } from 'react-native'
import Animated, { useAnimatedStyle, useSharedValue, withTiming } from 'react-native-reanimated'
import { Stack } from 'expo-router'
import { Colors } from '@/constants/theme'
import { TopNav } from '@/components/nav/TopNav'
import { Drawer } from '@/components/nav/Drawer'

// A crashed tab offers the way back instead of a dead app: the message names
// what happened, retry remounts the segment. Boundaries catch render errors;
// network failures already surface inline per screen.
export function ErrorBoundary({ error, retry }: { error: Error; retry: () => void }) {
  return (
    <View style={boundaryStyles.root}>
      <Text style={boundaryStyles.title}>This tab crashed.</Text>
      <Text style={boundaryStyles.detail} numberOfLines={4}>
        {error.message || 'Something broke while rendering.'}
      </Text>
      <Pressable onPress={retry} accessibilityRole="button" style={boundaryStyles.btn}>
        <Text style={boundaryStyles.btnText}>Try again</Text>
      </Pressable>
    </View>
  )
}

const boundaryStyles = StyleSheet.create({
  root: { flex: 1, backgroundColor: Colors.void, alignItems: 'center', justifyContent: 'center', gap: 12, padding: 24 },
  title: { color: Colors.paper, fontSize: 16, fontWeight: '600' },
  detail: { color: Colors.fog, fontSize: 13, textAlign: 'center' },
  btn: {
    minHeight: 44,
    paddingHorizontal: 20,
    borderRadius: 10,
    backgroundColor: Colors.acidLime,
    alignItems: 'center',
    justifyContent: 'center',
  },
  btnText: { color: Colors.acidLimeInk, fontSize: 14, fontWeight: '600' },
})

// Tabs group shell: the content translates right by half the screen while
// the drawer slides in from the left, shift, never squeeze, never overlay.
// Both panes keep full width; the tab screen simply slides half off-screen.
export default function TabsGroupLayout() {
  const [drawerOpen, setDrawerOpen] = React.useState(false)
  const halfWidth = Dimensions.get('window').width / 2
  const shift = useSharedValue(0)
  React.useEffect(() => {
    shift.value = withTiming(drawerOpen ? halfWidth : 0, { duration: 280 })
  }, [drawerOpen, halfWidth, shift])
  const content = useAnimatedStyle(() => ({ transform: [{ translateX: shift.value }] }))

  return (
    <View style={styles.root}>
      <Drawer open={drawerOpen} width={halfWidth} onClose={() => setDrawerOpen(false)} />
      <Animated.View style={[styles.content, content]}>
        <TopNav onProfilePress={() => setDrawerOpen((v) => !v)} />
        <View style={styles.body}>
          <Stack screenOptions={{ headerShown: false, contentStyle: styles.screen }}>
            <Stack.Screen name="build" />
            <Stack.Screen name="diff" />
            <Stack.Screen name="device" />
            <Stack.Screen name="more" />
          </Stack>
        </View>
      </Animated.View>
    </View>
  )
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: Colors.void },
  content: { flex: 1 },
  body: { flex: 1 },
  screen: { backgroundColor: Colors.void },
})
