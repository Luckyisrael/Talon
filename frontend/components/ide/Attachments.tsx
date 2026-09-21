"use client";

import { useEffect, useState } from "react";
import { attachments } from "@/lib/api";
import type { LocalAttachment } from "@/lib/attachments";

/**
 * Attachment chips — the composer's staging row and the user bubble's record.
 *
 * ComposerAttachments renders local files (object-URL thumbnails for images,
 * name chips for docs) with a remove button each. MessageAttachments renders
 * a sent turn: optimistic bubbles reuse their preview URLs, restored turns
 * load bytes through the authed endpoint. A failed thumbnail degrades to a
 * name chip — the turn's record never blanks on a missing file.
 */

export function ComposerAttachments({
  items,
  onRemove,
  error,
}: {
  items: LocalAttachment[];
  onRemove: (key: string) => void;
  error: string | null;
}) {
  if (items.length === 0 && !error) return null;
  return (
    <div className="att-row">
      {items.map((a) => (
        <span key={a.key} className="att">
          {a.kind === "image" && a.previewUrl ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={a.previewUrl} alt={a.name} />
          ) : (
            <span className="att-doc" aria-hidden>
              {a.kind === "image" ? "IMG" : "MD"}
            </span>
          )}
          <span className="meta">
            <b>{a.name}</b>
            {a.sizeLabel}
          </span>
          <button
            type="button"
            onClick={() => onRemove(a.key)}
            className="x"
            aria-label={`Remove ${a.name}`}
          >
            ×
          </button>
        </span>
      ))}
      {error ? (
        <span className="att-err" role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}

export interface MessageAttachment {
  id: number;
  name: string;
  mime: string;
  previewUrl?: string;
}

function RemoteThumb({ projectId, item }: { projectId: number; item: MessageAttachment }) {
  const [url, setUrl] = useState<string | null>(item.previewUrl ?? null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (item.previewUrl) return;
    let cancelled = false;
    let objectUrl: string | null = null;
    attachments
      .load(projectId, item.id)
      .then((u) => {
        if (cancelled) {
          URL.revokeObjectURL(u);
          return;
        }
        objectUrl = u;
        setUrl(u);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [projectId, item.id, item.previewUrl]);

  if (failed || !url) {
    return (
      <span className="att-doc" aria-hidden>
        IMG
      </span>
    );
  }
  // eslint-disable-next-line @next/next/no-img-element
  return <img src={url} alt={item.name} />;
}

export function MessageAttachments({
  projectId,
  items,
}: {
  projectId: number;
  items: MessageAttachment[];
}) {
  if (items.length === 0) return null;
  return (
    <div className="att-row">
      {items.map((a) => (
        <span key={a.id} className="att att-readonly" title={a.name}>
          {a.mime.startsWith("image/") ? (
            <RemoteThumb projectId={projectId} item={a} />
          ) : (
            <span className="att-doc" aria-hidden>
              MD
            </span>
          )}
          <span className="meta">
            <b>{a.name}</b>
          </span>
        </span>
      ))}
    </div>
  );
}
