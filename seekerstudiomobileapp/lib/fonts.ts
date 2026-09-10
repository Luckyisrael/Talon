import { useFonts, Inter_400Regular, Inter_500Medium } from '@expo-google-fonts/inter';
import { JetBrainsMono_400Regular } from '@expo-google-fonts/jetbrains-mono';

// useAppFonts loads Inter 400/500 + JetBrains Mono 400. Returns resolved
// family names: the loaded font when ready, system fallbacks otherwise.
// Render is NEVER gated on fonts — text shows immediately in the fallback
// and swaps when the files arrive.
export function useAppFonts(): { loaded: boolean; inter: string; mono: string } {
  const [loaded] = useFonts({
    Inter_400Regular,
    Inter_500Medium,
    JetBrainsMono_400Regular,
  });
  return {
    loaded,
    inter: loaded ? 'Inter_400Regular' : 'System',
    mono: loaded ? 'JetBrainsMono_400Regular' : 'monospace',
  };
}
