import React from 'react'
import { StyleSheet, View } from 'react-native'
import { LinearGradient } from 'expo-linear-gradient'
import { Image } from 'expo-image'
import { Image as ImageIcon } from 'iconsax-react-native'
import { UIText } from './Text'
import { Colors, Spacing } from '@/constants/theme'

// ShotSlot frames a product screenshot at half height, dissolving into the
// void at its base. Without a source it renders the labeled placement with
// its expected size, so screenshot drops land without layout changes.
// Captions sit below the frame, never overlaid on the image.
// Swap the source in when the app-in-action screenshots land; the frame,
// fade, and caption already fit them. Bundled art loads via require,
// remote art via uri string.
export function ShotSlot({
  shot,
  size,
  source,
  height = 300,
  fill = false,
}: {
  shot: string
  size: string
  source?: string | number
  height?: number
  /** Fill the parent instead of the fixed height: the middle zone image. */
  fill?: boolean
}) {
  return (
    <View style={fill && styles.fillWrap}>
      <View
        style={[styles.slot, fill ? styles.fillSlot : { height }]}
        accessibilityLabel={`Screenshot placeholder: ${shot}`}
      >
        {source ? (
          <Image source={source} style={styles.shot} contentFit="cover" />
        ) : (
          <View style={styles.empty}>
            <ImageIcon size={28} color={Colors.ash} variant="Linear" />
            <UIText variant="caption" tone="faint" style={styles.label}>
              Screenshot goes here
            </UIText>
          </View>
        )}
        <LinearGradient
          colors={['transparent', Colors.void]}
          locations={[0.35, 1]}
          style={styles.fade}
          pointerEvents="none"
        />
      </View>
      <UIText variant="monoSm" tone="faint" style={styles.caption}>
        {shot} · {size}
      </UIText>
    </View>
  )
}

const styles = StyleSheet.create({
  fillWrap: { flex: 1 },
  slot: {
    borderRadius: 12,
    backgroundColor: Colors.obsidian,
    overflow: 'hidden',
  },
  fillSlot: { flex: 1, minHeight: 120 },
  shot: { ...StyleSheet.absoluteFillObject },
  empty: { flex: 1, alignItems: 'center', justifyContent: 'center', gap: Spacing.s8 },
  label: { textAlign: 'center' },
  fade: { position: 'absolute', left: 0, right: 0, bottom: 0, height: 140 },
  caption: { marginTop: Spacing.s8 },
})
