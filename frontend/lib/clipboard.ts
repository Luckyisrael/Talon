/**
 * Copy text that works in insecure contexts too.
 *
 * navigator.clipboard exists only in secure contexts (HTTPS or localhost).
 * On the dev server over plain HTTP from a LAN address it is undefined, and
 * a copy button silently did nothing. The legacy path — a transient
 * textarea + execCommand("copy") — works everywhere and is the standard
 * fallback. Returns false when neither path managed the copy, so callers
 * can keep the text selectable as their honest fallback.
 */
export async function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // fall through to the legacy path
    }
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
