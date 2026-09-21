import React from 'react';
import { Pressable, StyleSheet, View } from 'react-native';
import { usePathname, useRouter } from 'expo-router';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { User } from 'iconsax-react-native';
import { UIText } from '@/components/ui';
import { Colors, Radii, Spacing } from '@/constants/theme';

const TABS = [
  { segment: 'build', label: 'Build' },
  { segment: 'diff', label: 'Diff' },
  { segment: 'device', label: 'Device' },
  { segment: 'more', label: 'More' },
] as const;

// TopNav: independent nav items, nothing binding them. Each carries its own
// padding + radius + surface: idle is transparent with ash text; active is a
// graphite pill with paper text and the lime marker. The bar respects the
// notch via the top safe-area inset, content never slides under it.
export function TopNav({ onProfilePress }: { onProfilePress: () => void }) {
  const pathname = usePathname();
  const router = useRouter();
  const insets = useSafeAreaInsets();

  return (
    <View style={[styles.bar, { paddingTop: insets.top + Spacing.s8 }]}>
      <Pressable
        onPress={onProfilePress}
        accessibilityLabel="Open profile menu"
        style={({ pressed }) => [styles.avatar, pressed && styles.pressed]}
      >
        <User size={18} color={Colors.mist} variant="Bulk" />
      </Pressable>
      <View style={styles.items}>
        {TABS.map((t) => {
          // Match on the last path segment: usePathname() strips (group)
          // names, so '/(tabs)/build' never matches, 'build' always does.
          const active = pathname.split('/').pop() === t.segment;
          return (
            <Pressable
              key={t.segment}
              onPress={() => router.replace(`/(tabs)/${t.segment}`)}
              accessibilityState={{ selected: active }}
              style={({ pressed }) => [
                styles.item,
                active && styles.itemActive,
                pressed && !active && styles.itemPressed,
              ]}
            >
              <UIText variant="bodySm" tone={active ? 'hero' : 'faint'} style={[active && styles.activeText, active && styles.activeInk]}>
                {t.label}
              </UIText>
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  bar: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s12,
    backgroundColor: Colors.void,
    paddingHorizontal: Spacing.s16,
    paddingBottom: Spacing.s12,
  },
  avatar: {
    width: 40,
    height: 40,
    borderRadius: 20,
    backgroundColor: Colors.obsidian,
    alignItems: 'center',
    justifyContent: 'center',
  },
  pressed: { opacity: 0.6 },
  items: { flexDirection: 'row', gap: Spacing.s8, flex: 1 },
  item: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 6,
    minHeight: 40,
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: Radii.pills,
    backgroundColor: Colors.carbon,
  },
  itemActive: { backgroundColor: Colors.acidLime },
  itemPressed: { backgroundColor: 'rgba(255,255,255,0.04)' },
  activeText: { fontWeight: '500' },
  // Void ink on the lime fill, the one place nav text goes dark.
  activeInk: { color: Colors.acidLimeInk },
});
