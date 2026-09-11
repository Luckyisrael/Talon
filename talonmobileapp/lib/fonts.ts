import { useFonts, Inter_400Regular, Inter_500Medium, Inter_600SemiBold, Inter_700Bold } from '@expo-google-fonts/inter';
import { SpaceGrotesk_500Medium, SpaceGrotesk_700Bold } from '@expo-google-fonts/space-grotesk';
import { JetBrainsMono_400Regular } from '@expo-google-fonts/jetbrains-mono';

// useAppFonts loads the three families: Space Grotesk (display/headings),
// Inter (body/labels), JetBrains Mono (code-adjacent). Every weight maps to
// its true file — never faux-bold. Render is NEVER gated on fonts.
export function useAppFonts(): {
  loaded: boolean;
  inter: (weight: string | number) => string;
  display: (weight: string | number) => string;
  mono: string;
} {
  const [loaded] = useFonts({
    Inter_400Regular,
    Inter_500Medium,
    Inter_600SemiBold,
    Inter_700Bold,
    SpaceGrotesk_500Medium,
    SpaceGrotesk_700Bold,
    JetBrainsMono_400Regular,
  });
  const inter = (weight: string | number) => {
    if (!loaded) return 'System';
    const w = typeof weight === 'string' ? parseInt(weight, 10) : weight;
    if (w >= 700) return 'Inter_700Bold';
    if (w >= 600) return 'Inter_600SemiBold';
    if (w >= 500) return 'Inter_500Medium';
    return 'Inter_400Regular';
  };
  const display = (weight: string | number) => {
    if (!loaded) return 'System';
    const w = typeof weight === 'string' ? parseInt(weight, 10) : weight;
    return w >= 600 ? 'SpaceGrotesk_700Bold' : 'SpaceGrotesk_500Medium';
  };
  return { loaded, inter, display, mono: loaded ? 'JetBrainsMono_400Regular' : 'monospace' };
}
