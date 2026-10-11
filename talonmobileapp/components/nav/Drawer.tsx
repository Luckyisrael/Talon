import React from 'react';
import { Pressable, StyleSheet, View } from 'react-native';
import Animated, { useAnimatedStyle, useSharedValue, withTiming } from 'react-native-reanimated';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { useRouter } from 'expo-router';
import { useMobileWallet } from '@wallet-ui/react-native-kit';
import { Book, Clock, Crown, Folder, Logout, MessageQuestion, Setting, User, Wallet } from 'iconsax-react-native';
import { UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';
import { authApi, meApi } from '@/lib/api';
import { clearToken, getToken } from '@/lib/auth';
import { latestWallet, linkWalletNow } from '@/lib/walletlink';

const ITEMS = [
  { icon: Folder, label: 'Projects', route: '/menu/projects' },
  { icon: Crown, label: 'Upgrade', route: '/menu/upgrade' },
  { icon: User, label: 'Profile', route: '/menu/profile' },
  { icon: Clock, label: 'History', route: '/menu/history' },
  { icon: Setting, label: 'Settings', route: '/menu/settings' },
  { icon: MessageQuestion, label: 'Help & Support', route: '/menu/help' },
  { icon: Book, label: 'Documentation', route: '/menu/docs' },
] as const;

// FM-WALLc(ii)/L73(c): first 4 … last 4 — the address exactly as the
// wallet showed it to approve, short enough for a 300pt drawer row.
function shortWalletAddress(address: string): string {
  return address.length > 12 ? `${address.slice(0, 4)}…${address.slice(-4)}` : address;
}

// Push drawer: fixed half-screen sheet sliding in from the left while the
// content translates right by the same distance, shift, never squeeze.
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
  const router = useRouter();
  React.useEffect(() => {
    progress.value = withTiming(open ? 1 : 0, { duration: 280 });
  }, [open, progress]);

  const sheet = useAnimatedStyle(() => ({
    transform: [{ translateX: (progress.value - 1) * width }],
  }));

  const [me, setMe] = React.useState<{ email: string; tier?: string; wallet_linked?: boolean } | null>(null);
  React.useEffect(() => {
    if (!open) return;
    let cancelled = false;
    void (async () => {
      const t = await getToken().catch(() => null);
      if (!t) {
        if (!cancelled) setMe(null);
        return;
      }
      try {
        const u = await meApi.get();
        if (!cancelled) setMe({ email: u.email, tier: u.tier, wallet_linked: u.wallet_linked });
      } catch {
        if (!cancelled) setMe(null);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open]);

  // L65/FM-WALLb — the bold reminder, off the same server fact the web
  // rail reads. Its press RUNS the L63 connect flow (session challenge →
  // MWA signature → verify) and the row vanishes the moment the link
  // lands — never latched. A cancel or refusal leaves it up: the reminder
  // survives its own failure so the retry is obvious.
  //
  // L73/FM-WALLc — and the drawer now says what HAPPENED, not only what
  // is missing: a positive Connected row carrying the approved address
  // (ii), recorded locally the moment the link lands so a null `me` can
  // no longer drop the fact (iii — the old `setMe(prev => prev ? … :
  // prev)` was a no-op exactly then), and a refusal that states its
  // reason instead of swallowing it (i). Local-only trust is bounded by
  // the server's own word: an explicit `wallet_linked: false` keeps the
  // reminder (L65's retry) no matter what the session holds.
  const wallet = useMobileWallet();
  // L78/FM-WALLd(v): same latest-ref binding as the onboarding screen —
  // the link flow's stale-token retry must land on the post-clear
  // render's closures, not the ones this render captured.
  const walletRef = React.useRef(wallet);
  React.useEffect(() => {
    walletRef.current = wallet;
  });
  const [connecting, setConnecting] = React.useState(false);
  const [linkError, setLinkError] = React.useState<string | null>(null);
  const [linkedLocal, setLinkedLocal] = React.useState(false);
  const account = wallet.account;
  const linked =
    linkedLocal ||
    me?.wallet_linked === true ||
    (!!account && !linkError && me?.wallet_linked !== false);
  const shortAddress = account ? shortWalletAddress(account.address) : null;
  async function connectWallet() {
    if (connecting) return;
    setConnecting(true);
    setLinkError(null);
    try {
      await linkWalletNow(latestWallet(walletRef));
      setLinkedLocal(true);
      setMe((prev) => (prev ? { ...prev, wallet_linked: true } : prev));
    } catch (err) {
      // Cancelled or refused — the reminder stays, now with the reason.
      setLinkError(err instanceof Error && err.message ? err.message : 'the wallet declined');
    } finally {
      setConnecting(false);
    }
  }

  return (
    <Animated.View style={[styles.sheet, { width }, sheet]}>
      <View style={[styles.inner, { paddingTop: insets.top + Spacing.s16 }]}>
        <View style={styles.head}>
          <View style={styles.avatar}>
            <User size={24} color={Colors.mist} variant="Bulk" />
          </View>
          <UIText variant="heading" tone="hero" style={styles.name} numberOfLines={1}>
            {me?.email ?? 'Guest'}
          </UIText>
          <UIText variant="bodySm" tone="body" numberOfLines={1}>
            {me ? `${me.email} · ${me.tier ?? 'free'}` : 'Not signed in. Explore freely, nothing syncs yet.'}
          </UIText>
        </View>
        {linked && (
          <View style={[styles.item, styles.reminder]} accessibilityLabel="Wallet connected">
            <Wallet size={24} color={Colors.acidLime} variant="Bulk" />
            <View style={styles.connected}>
              <UIText variant="bodyLg" tone="accent">
                Wallet connected
              </UIText>
              {shortAddress && (
                <UIText variant="bodySm" tone="muted" numberOfLines={1}>
                  {shortAddress}
                </UIText>
              )}
            </View>
          </View>
        )}
        {!linked && me?.wallet_linked === false && (
          <Pressable
            style={[styles.item, styles.reminder]}
            accessibilityRole="button"
            accessibilityLabel="Connect your wallet"
            onPress={connectWallet}
            disabled={connecting}
          >
            <Wallet size={24} color={Colors.acidLime} variant="Bulk" />
            <UIText variant="bodyLg" tone="accent" style={styles.reminderText}>
              {connecting ? 'Connecting…' : 'Connect your wallet'}
            </UIText>
          </Pressable>
        )}
        {!linked && linkError && (
          <UIText variant="caption" tone="danger">
            Connect failed: {linkError}
          </UIText>
        )}
        {ITEMS.map((item) => {
          const Icon = item.icon;
          return (
            <Pressable
              key={item.label}
              style={styles.item}
              accessibilityRole="button"
              onPress={() => {
                onClose();
                router.push(item.route);
              }}
            >
              <Icon size={24} color={Colors.fog} variant="Bulk" />
              <UIText variant="bodyLg" tone="body">
                {item.label}
              </UIText>
            </Pressable>
          );
        })}
        <View style={styles.foot}>
          <Pressable
            style={styles.item}
            accessibilityRole="button"
            onPress={async () => {
              onClose();
              await authApi.revoke().catch(() => {});
              await clearToken();
              router.replace('/auth');
            }}
          >
            <Logout size={24} color={Colors.coralRed} variant="Bulk" />
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
  reminder: {
    borderRadius: 12,
    backgroundColor: Colors.obsidian,
    marginBottom: Spacing.s8,
  },
  reminderText: { fontWeight: '700', flexShrink: 1 },
  connected: { flexShrink: 1 },
  foot: { marginTop: 'auto', paddingBottom: Spacing.s16 },
});
