/**
 * A UUID that works everywhere.
 *
 * crypto.randomUUID() exists only in secure contexts — HTTPS or localhost.
 * Browsing the dev server over plain HTTP from a LAN address (a phone, a
 * second machine, 192.168.x.x) is not a secure context: the function is
 * missing, and the first AIChat send threw "crypto.randomUUID is not a
 * function" before the request ever left the page. getRandomValues is
 * available in every context, secure or not, so the fallback formats the
 * same v4 shape from it.
 */
export function uuid(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  // v4: set version and variant bits, then hex with dashes.
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
