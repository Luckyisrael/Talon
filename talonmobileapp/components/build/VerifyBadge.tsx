import React from 'react'
import { useQuery } from '@tanstack/react-query'
import { Badge } from '@/components/ui'
import { verifyBadge, verifyState } from '@/lib/api'

// VerifyBadge is the project-level sweep claim — the web Topbar's "Build
// verified · {ago}" badge, mobile placement (build screen header, persistent
// chrome). It claims ONLY what GET /verify-state 200s: no row (404) → null,
// refused sweep → null, transient error → null. Absence is the signal; the
// badge never appears on hope (row 46 / web rows 44-45, same rule).
export function VerifyBadge({ projectId }: { projectId: number }) {
  const { data } = useQuery({
    queryKey: ['verify-state', projectId],
    queryFn: () => verifyState.get(projectId),
    retry: false,
  })
  const label = verifyBadge(data ?? null)
  if (!label) return null
  return <Badge label={`Build verified · ${label}`} tone="success" />
}
