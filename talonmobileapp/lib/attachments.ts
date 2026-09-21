// Composer attachments, mobile side: images from the photo library and
// markdown docs from the file picker. Same contract as the web composer:
// validate here, upload at send time, reference by id. The server
// re-validates by sniffing bytes, this is UX, not security.

export type AttachmentKind = 'image' | 'doc'

export interface LocalAttachment {
  key: string
  uri: string
  mime: string
  kind: AttachmentKind
  name: string
  sizeLabel: string
}

export interface SentAttachment {
  id: number
  name: string
  mime: string
}

export const MAX_ATTACHMENTS = 10
export const MAX_ATTACHMENT_BYTES = 5 * 1024 * 1024

const IMAGE_MIMES = ['image/png', 'image/jpeg', 'image/webp']
const IMAGE_EXT = /\.(png|jpe?g|webp)$/i

export function describeSize(n: number): string {
  if (n < 1024) return `${n}b`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)}kb`
  return `${(n / (1024 * 1024)).toFixed(1)}mb`
}

export function classifyPicked(picked: {
  name?: string | null
  mimeType?: string | null
  size?: number | null
}): { kind: AttachmentKind } | { error: string } {
  const name = picked.name ?? 'file'
  const mime = (picked.mimeType ?? '').toLowerCase()
  const isImage = IMAGE_MIMES.includes(mime) || IMAGE_EXT.test(name)
  const isDoc = /\.md$/i.test(name)
  if (!isImage && !isDoc) {
    return { error: `${name} is not attachable, png, jpeg, webp images and .md files only` }
  }
  if (picked.size != null) {
    if (picked.size <= 0) return { error: `${name} is empty` }
    if (picked.size > MAX_ATTACHMENT_BYTES) {
      return { error: `${name} is ${describeSize(picked.size)}, the limit is 5MB` }
    }
  }
  return { kind: isImage ? 'image' : 'doc' }
}

/**
 * stripInlineDocs removes the server-inlined attachment sections from text
 * shown in the UI. The model replays the full prose; the person sees the
 * prompt plus chips. A doc file never shows its full content in the chat,
 * and the images marker line is redundant next to the thumbnails.
 */
export function stripInlineDocs(content: string): string {
  return content
    .replace(/\n\n\[Attached file [^\]\n]+\]\n[\s\S]*?(?=\n\n\[Attached (?:file|images):|\s*$)/g, '')
    .replace(/\n\n\[Attached images: [^\]\n]+\]/g, '')
    .trim()
}
