/**
 * Composer attachments: images and markdown docs a turn carries.
 *
 * Files are validated here, downscaled if they are large images, uploaded at
 * send time, and referenced by id. Nothing here touches the network — upload
 * lives in lib/api so this file stays unit-testable (no canvas in jsdom).
 */

export type AttachmentKind = "image" | "doc";

export interface LocalAttachment {
  /** Stable client key for list rendering. */
  key: string;
  file: File;
  kind: AttachmentKind;
  name: string;
  sizeLabel: string;
  /** Object URL for image chips. Created by previewFor, revoked by the owner. */
  previewUrl?: string;
}

export const MAX_ATTACHMENTS = 10;
export const MAX_ATTACHMENT_BYTES = 5 * 1024 * 1024;

/** Downscaled so pixel bytes stay context-sized: the model bills images by
 *  the token, and a 12MP camera roll original would eat a turn's budget. */
export const MAX_IMAGE_DIM = 1600;

const IMAGE_MIMES = ["image/png", "image/jpeg", "image/webp"];
const IMAGE_EXT = /\.(png|jpe?g|webp)$/i;

export function describeSize(n: number): string {
  if (n < 1024) return `${n}b`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)}kb`;
  return `${(n / (1024 * 1024)).toFixed(1)}mb`;
}

/** classifyFile is the composer's gate: kind or a human reason. The server
 *  re-validates by sniffing bytes — this is UX, not security. */
export function classifyFile(file: File): { kind: AttachmentKind } | { error: string } {
  const isImage = IMAGE_MIMES.includes(file.type) || IMAGE_EXT.test(file.name);
  const isDoc = /\.md$/i.test(file.name);
  if (!isImage && !isDoc) {
    return { error: `${file.name || "that file"} is not attachable — png, jpeg, webp images and .md files only` };
  }
  if (file.size === 0) {
    return { error: `${file.name} is empty` };
  }
  if (file.size > MAX_ATTACHMENT_BYTES) {
    return { error: `${file.name} is ${describeSize(file.size)} — the limit is 5MB` };
  }
  return { kind: isImage ? "image" : "doc" };
}

/** previewFor makes an image chip thumbnail. Null outside browsers and for
 *  non-images — the caller renders a name chip instead. */
export function previewFor(file: File, kind: AttachmentKind): string | undefined {
  if (kind !== "image") return undefined;
  try {
    if (typeof URL !== "undefined" && typeof URL.createObjectURL === "function") {
      return URL.createObjectURL(file);
    }
  } catch {
    // Non-browser runtimes (tests, SSR): chips fall back to names.
  }
  return undefined;
}

/** downscaleImage shrinks oversized images before upload. Failures and
 *  small-enough files return the original — upload never blocks on this. */export async function downscaleImage(file: File): Promise<File> {
  if (!file.type.startsWith("image/")) return file;
  try {
    if (typeof createImageBitmap !== "function") return file;
    const bitmap = await createImageBitmap(file);
    const longest = Math.max(bitmap.width, bitmap.height);
    if (longest <= MAX_IMAGE_DIM) {
      if (typeof bitmap.close === "function") bitmap.close();
      return file;
    }
    const scale = MAX_IMAGE_DIM / longest;
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(bitmap.width * scale);
    canvas.height = Math.round(bitmap.height * scale);
    const ctx = canvas.getContext("2d");
    if (!ctx) return file;
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    if (typeof bitmap.close === "function") bitmap.close();
    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, file.type, 0.85),
    );
    if (!blob) return file;
    return new File([blob], file.name, { type: blob.type || file.type });
  } catch {
    return file;
  }
}

/**
 * stripInlineDocs removes the server-inlined attachment sections from text
 * shown in the UI. The model replays the full prose; the person sees the
 * prompt plus chips. A doc file never shows its full content in the chat,
 * and the images marker line is redundant next to the thumbnails.
 */
export function stripInlineDocs(content: string): string {
  return content
    .replace(/\n\n\[Attached file [^\]\n]+\]\n[\s\S]*?(?=\n\n\[Attached (?:file|images):|\s*$)/g, "")
    .replace(/\n\n\[Attached images: [^\]\n]+\]/g, "")
    .trim();
}
