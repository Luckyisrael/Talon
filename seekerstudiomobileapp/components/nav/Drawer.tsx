import React from 'react';
import { Pressable, StyleSheet, View } from 'react-native';
import Animated, { useAnimatedStyle, useSharedValue, withTiming } from 'react-native-reanimated';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Ionicons } from '@expo/vector-icons';
import { UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

const ITEMS: { icon: string; label: string }[] = [
  { icon: 'person-outline', label: 'Profile' },
  { icon: 'time-outline', label: 'History' },
  { icon: 'settings-outline', label: 'Settings' },
  { icon: 'help-circle-outline', label: 'Help & Support' },
];

// Push drawer: fixed half-screen sheet sliding in from the left while the
// content translates right by the same distance — shift, never squeeze.
// No scrim, no overlay: both panes stay full-width, the content simply
// slides half off-screen.
export function Drawer({
  open,
  width,
  onClose,
}: {
  open: boolean;
  width: number;
  onClose: () => void;
}) {
  const progress = useSharedValue(0);
  const insets = useSafeAreaInsets();
  React.useEffect(() => {
    progress.value = withTiming(open ? 1 : 0, { duration: 280 });
  }, [open, progress]);

  const sheet = useAnimatedStyle(() => ({
    transform: [{ translateX: (progress.value - 1) * width }],
  }));

  return (
    <Animated.View style={[styles.sheet, { width }, sheet]}>
      <View style={[styles.inner, { paddingTop: insets.top + Spacing.s16 }]}>
        <View style={styles.head}>
          <View style={styles.avatar}>
            <Ionicons name="person" size={24} color={Colors.mist} />
          </View>
          <UIText variant="heading" tone="hero" style={styles.name}>
            Guest
          </UIText>
          <UIText variant="bodySm" tone="body">
            Not signed in — explore freely, nothing syncs yet.
          </UIText>
        </View>
        {ITEMS.map((item) => (
          <Pressable key={item.label} style={styles.item} onPress={onClose}>
            <Ionicons name={item.icon as never} size={24} color={Colors.fog} />
            <UIText variant="bodyLg" tone="body">
              {item.label}
            </UIText>
          </Pressable>
        ))}
        <View style={styles.foot}>
          <Pressable style={styles.item} onPress={onClose}>
            <Ionicons name="log-out-outline" size={24} color={Colors.coralRed} />
            <UIText variant="bodyLg" tone="danger">
              Sign out
            </UIText>
          </Pressable>
        </View>
      </View>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  sheet: {
    position: 'absolute',
    left: 0,
    top: 0,
    bottom: 0,
    backgroundColor: Colors.carbon,
    borderRightWidth: StyleSheet.hairlineWidth,
    borderRightColor: Colors.graphite,
    zIndex: 10,
  },
  inner: {
    flex: 1,
    paddingHorizontal: Spacing.s16,
    gap: Spacing.s8,
  },
  head: { gap: Spacing.s8, marginBottom: Spacing.s16 },
  avatar: {
    width: 52,
    height: 52,
    borderRadius: 26,
    backgroundColor: Colors.obsidian,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
  },
  name: { fontWeight: '500' },
  item: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s16,
    minHeight: 52,
    paddingHorizontal: Spacing.s8,
  },
  foot: { marginTop: 'auto', paddingBottom: Spacing.s16 },
});
