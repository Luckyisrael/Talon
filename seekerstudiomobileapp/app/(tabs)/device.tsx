import React from 'react';
import { Pressable, ScrollView, StyleSheet, View } from 'react-native';
import { Camera, Refresh, Send } from 'iconsax-react-native';
import { Screen, UIText } from '@/components/ui';
import { Badge } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

// FauxQr is a deterministic placeholder grid (finder squares + seeded
// modules) standing in for the device-pairing QR until the real code
// (expo-camera scan / share sheet) lands. Clearly dummy, same footprint.
function FauxQr() {
  const N = 21;
  const cells: boolean[] = [];
  for (let i = 0; i < N * N; i++) {
    const r = Math.floor(i / N);
    const c = i % N;
    const inFinder =
      (r < 7 && c < 7) || (r < 7 && c >= N - 7) || (r >= N - 7 && c < 7);
    if (inFinder) {
      const lr = r >= N - 7 ? r - (N - 7) : r;
      const lc = c >= N - 7 ? c - (N - 7) : c;
      const edge = lr === 0 || lr === 6 || lc === 0 || lc === 6;
      const core = lr >= 2 && lr <= 4 && lc >= 2 && lc <= 4;
      cells.push(edge || core);
    } else {
      cells.push(((i * 2654435761) >>> 0) % 100 < 42);
    }
  }
  return (
    <View style={styles.qr}>
      {cells.map((on, i) => (
        <View
          key={i}
          style={[styles.qrCell, { backgroundColor: on ? Colors.paper : 'transparent' }]}
        />
      ))}
    </View>
  );
}

const LOG_LINES = [
  '09:24:58 boot complete · Redroid 14',
  '09:25:02 demodapp installed · verified',
  '09:25:57 am start · pid 2780 alive',
  '09:26:03 first log after 1.1s · render ok',
];

function IconButton({
  icon,
  label,
  primary,
  onPress,
}: {
  icon: typeof Camera;
  label: string;
  primary?: boolean;
  onPress: () => void;
}) {
  const Icon = icon;
  return (
    <Pressable
      onPress={onPress}
      style={({ pressed }) => [
        styles.iconBtn,
        primary && styles.iconBtnPrimary,
        pressed && styles.iconBtnPressed,
      ]}
      accessibilityLabel={label}
    >
      <Icon size={22} color={primary ? Colors.acidLimeInk : Colors.mist} variant="Linear" />
    </Pressable>
  );
}

// Device tab (dummy): phone frame, icon toolbar (capture / rotate / power),
// live-log card, pairing QR. Verdict rows live in the Build transcript —
// this tab is device state: logs, pairing, controls.
export default function DeviceScreen() {
  const [shot, setShot] = React.useState<string | null>(null);

  return (
    <Screen>
      <View style={styles.head}>
        <View style={styles.titles}>
          <UIText variant="micro" tone="faint">
            SESSION 24 · LOCALHOST:15555
          </UIText>
          <UIText variant="heading" tone="hero">
            Device
          </UIText>
        </View>
        <Badge label="booted" tone="success" />
      </View>

      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          <View style={styles.frame}>
            {shot ? (
              <UIText variant="bodySm" tone="muted">
                {shot}
              </UIText>
            ) : (
              <View style={styles.frameEmpty}>
                <UIText variant="title" tone="faint">
                  Demo App
                </UIText>
                <UIText variant="caption" tone="faint">
                  No frame captured yet.{'\n'}Screenshots render on demand.
                </UIText>
              </View>
            )}
          </View>

          {/* Icon toolbar: capture is primary, rotate + install secondary. */}
          <View style={styles.toolbar}>
            <IconButton
              icon={Camera}
              label="Capture screenshot"
              primary
              onPress={() => setShot('frame · 1080×2400 · just now (dummy)')}
            />
            <IconButton icon={Refresh} label="Rotate display" onPress={() => {}} />
            <IconButton icon={Send} label="Install current build" onPress={() => {}} />
          </View>

          <UIText variant="title" tone="hero" style={styles.section}>
            Logs
          </UIText>
          <View style={styles.logCard}>
            {LOG_LINES.map((line, i) => (
              <UIText key={i} variant="monoSm" tone="muted" numberOfLines={1}>
                {line}
              </UIText>
            ))}
          </View>

          <UIText variant="title" tone="hero" style={styles.section}>
            Pairing
          </UIText>
          <View style={styles.qrCard}>
            <FauxQr />
            <UIText variant="caption" tone="muted" style={styles.qrCaption}>
              Scan to attach this phone as the build device. QR encodes the session —
              placeholder pattern until camera pairing lands.
            </UIText>
          </View>
        </View>
      </ScrollView>
    </Screen>
  );
}

const styles = StyleSheet.create({
  head: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    gap: Spacing.s12,
    marginBottom: Spacing.s4,
  },
  titles: { gap: 8, flex: 1 },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s16, paddingBottom: Spacing.s32 },
  frame: {
    aspectRatio: 9 / 16,
    borderRadius: 12,
    backgroundColor: Colors.carbon,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
    padding: Spacing.s16,
  },
  frameEmpty: { alignItems: 'center', gap: Spacing.s8 },
  toolbar: { flexDirection: 'row', gap: Spacing.s12, alignItems: 'center' },
  iconBtn: {
    width: 52,
    height: 52,
    borderRadius: 26,
    backgroundColor: Colors.carbon,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
  },
  iconBtnPrimary: { backgroundColor: Colors.acidLime, borderColor: Colors.acidLime },
  iconBtnPressed: { opacity: 0.7 },
  section: { marginTop: Spacing.s8 },
  logCard: {
    borderRadius: 12,
    backgroundColor: Colors.carbon,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    padding: Spacing.s12,
    gap: 6,
  },
  qrCard: {
    borderRadius: 12,
    backgroundColor: Colors.carbon,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    padding: Spacing.s16,
    alignItems: 'center',
    gap: Spacing.s12,
  },
  qr: {
    width: 168,
    flexDirection: 'row',
    flexWrap: 'wrap',
    backgroundColor: Colors.void,
    borderRadius: 8,
    padding: 8,
  },
  qrCell: { width: 6.5, height: 6.5, margin: 0.35 },
  qrCaption: { textAlign: 'center' },
});
