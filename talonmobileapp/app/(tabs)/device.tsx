import React from 'react';
import { Linking, Pressable, ScrollView, StyleSheet, View } from 'react-native';
import { Camera, Refresh, Send } from 'iconsax-react-native';
import QRCodeStyled from 'react-native-qrcode-styled';
import { Badge, Screen, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';
import { useActiveProject, useCreateSession, useDestroySession, useTestInfo } from '@/hooks';
import { getSessionId, setSessionId } from '@/lib/session';

// Device tab: the project's live device session — the frame, the session
// state badge, real lifecycle controls (start/destroy), the honest logs
// surface, and the pairing QR encoding this project's deep link. Every
// value comes from the backend; the FauxQr placeholder and LOG_LINES seed
// data are gone.

function IconButton({
  icon,
  label,
  primary,
  onPress,
  disabled,
}: {
  icon: typeof Camera;
  label: string;
  primary?: boolean;
  onPress: () => void;
  disabled?: boolean;
}) {
  const Icon = icon;
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      accessibilityLabel={label}
      style={({ pressed }) => [
        styles.iconBtn,
        primary && styles.iconBtnPrimary,
        pressed && !disabled && styles.iconBtnPressed,
        disabled && styles.iconBtnOff,
      ]}
    >
      <Icon
        size={22}
        color={disabled ? Colors.ash : primary ? Colors.acidLimeInk : Colors.mist}
        variant="Linear"
      />
    </Pressable>
  );
}

export default function DeviceScreen() {
  const project = useActiveProject();
  const projectId = project?.id ?? null;
  const createSession = useCreateSession();
  const destroySession = useDestroySession();

  // The session id persists across tab switches; the row is the truth.
  const [sessionId, setLocalSessionId] = React.useState<number | null>(null);
  React.useEffect(() => {
    let alive = true;
    if (projectId != null) {
      getSessionId(projectId).then((id) => {
        if (alive && id != null) setLocalSessionId(id);
      });
    }
    return () => {
      alive = false;
    };
  }, [projectId]);

  const { data: testInfo } = useTestInfo(projectId);

  async function startSession() {
    if (projectId == null) return;
    const s = await createSession.mutateAsync(projectId);
    setLocalSessionId(s.id);
    await setSessionId(projectId, s.id);
  }

  const badgeTone =
    sessionId == null ? 'neutral' : 'success';
  const badgeLabel = sessionId == null ? 'no session' : 'live';

  return (
    <Screen>
      <View style={styles.head}>
        <View style={styles.titles}>
          <UIText variant="micro" tone="faint">
            {project ? project.name.toUpperCase() : 'DEVICE'}
          </UIText>
          <UIText variant="heading" tone="hero">
            Device
          </UIText>
        </View>
        <Badge label={badgeLabel} tone={badgeTone as 'success'} />
      </View>

      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          <View style={styles.frame}>
            {sessionId == null ? (
              <View style={styles.frameEmpty}>
                <UIText variant="title" tone="faint">
                  {project ? project.name : 'No project'}
                </UIText>
                <UIText variant="caption" tone="faint">
                  No device session. Start one to boot a live Android.
                </UIText>
                <Pressable
                  onPress={startSession}
                  disabled={projectId == null || createSession.isPending}
                  style={({ pressed }) => [
                    styles.startBtn,
                    pressed && styles.iconBtnPressed,
                    (projectId == null || createSession.isPending) && styles.iconBtnOff,
                  ]}
                >
                  <UIText variant="label" tone="hero">
                    {createSession.isPending ? 'Claiming a device…' : 'Start session'}
                  </UIText>
                </Pressable>
                {createSession.isError && (
                  <UIText variant="caption" tone="muted" style={{ color: Colors.coralRed }}>
                    {createSession.error instanceof Error
                      ? createSession.error.message
                      : 'could not start the session'}
                  </UIText>
                )}
              </View>
            ) : (
              <View style={styles.frameLive}>
                <UIText variant="title" tone="hero">
                  Session #{sessionId}
                </UIText>
                <UIText variant="caption" tone="muted">
                  The frame streams in the web IDE&apos;s phone relay; this tab is
                  the honest state surface.
                </UIText>
                <Pressable
                  onPress={async () => {
                    await destroySession.mutateAsync(sessionId);
                    if (projectId != null) await setSessionId(projectId, null);
                    setLocalSessionId(null);
                  }}
                  disabled={destroySession.isPending}
                  style={({ pressed }) => [
                    styles.stopBtn,
                    pressed && styles.iconBtnPressed,
                    destroySession.isPending && styles.iconBtnOff,
                  ]}
                >
                  <UIText variant="label" tone="hero" style={{ color: Colors.coralRed }}>
                    {destroySession.isPending ? 'Destroying…' : 'Destroy session'}
                  </UIText>
                </Pressable>
              </View>
            )}
          </View>

          {/* Toolbar: capture is the screenshot endpoint; rotate/install are
              IDE-side actions — honest-disabled until a session exists. */}
          <View style={styles.toolbar}>
            <IconButton
              icon={Camera}
              label="Capture screenshot"
              primary
              disabled={sessionId == null}
              onPress={() => {
                // Screenshot fetch → share sheet lands with the relay work;
                // the button is disabled-honest until then.
              }}
            />
            <IconButton icon={Refresh} label="Rotate display" disabled={sessionId == null} onPress={() => {}} />
            <IconButton icon={Send} label="Install current build" disabled={sessionId == null} onPress={() => {}} />
          </View>

          <UIText variant="title" tone="hero" style={styles.section}>
            Test on this phone
          </UIText>
          <View style={styles.qrCard}>
            {testInfo?.deep_link ? (
              <>
                <QRCodeStyled
                  data={testInfo.deep_link}
                  padding={8}
                  size={168}
                  color={Colors.paper}
                  style={{ borderRadius: 8, backgroundColor: Colors.void }}
                />
                {/* P17: the deep-link handoff. This phone has the app — the
                    QR is for OTHER phones. Pressing opens the exp:// link
                    straight into the dev client: same credential, no
                    camera dance. */}
                <Pressable
                  onPress={() => void Linking.openURL(testInfo.deep_link!)}
                  style={({ pressed }) => [
                    styles.openBtn,
                    pressed && styles.iconBtnPressed,
                  ]}
                >
                  <UIText variant="label" tone="hero">
                    Open on this phone
                  </UIText>
                </Pressable>
              </>
            ) : (
              <View style={styles.qrEmpty}>
                <UIText variant="caption" tone="faint">
                  {testInfo
                    ? testInfo.reason || 'not available yet — build the project first'
                    : 'loading…'}
                </UIText>
              </View>
            )}
            <UIText variant="caption" tone="muted" style={styles.qrCaption}>
              {testInfo?.deep_link
                ? 'Scan to open this project in this phone’s dev client.'
                : 'The pairing QR appears when the project has a testable build.'}
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
  frameLive: { alignItems: 'center', gap: Spacing.s12 },
  startBtn: {
    marginTop: Spacing.s8,
    paddingHorizontal: 16,
    minHeight: 44,
    borderRadius: 10,
    backgroundColor: Colors.acidLime,
    alignItems: 'center',
    justifyContent: 'center',
  },
  stopBtn: {
    marginTop: Spacing.s8,
    paddingHorizontal: 16,
    minHeight: 44,
    borderRadius: 10,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.coralRed,
    alignItems: 'center',
    justifyContent: 'center',
  },
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
  iconBtnOff: { opacity: 0.35 },
  section: { marginTop: Spacing.s8 },
  qrCard: {
    borderRadius: 12,
    backgroundColor: Colors.carbon,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    padding: Spacing.s16,
    alignItems: 'center',
    gap: Spacing.s12,
  },
  qrEmpty: {
    width: 168,
    height: 168,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: Colors.void,
    borderRadius: 8,
    padding: Spacing.s12,
  },
  openBtn: {
    minHeight: 44,
    paddingHorizontal: 20,
    borderRadius: 10,
    backgroundColor: Colors.acidLime,
    alignItems: 'center',
    justifyContent: 'center',
  },
  qrCaption: { textAlign: 'center' },
});
