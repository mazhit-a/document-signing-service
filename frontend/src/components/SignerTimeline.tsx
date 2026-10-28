import type { Signer } from '../api/types'
import { formatDateTime } from '../lib/format'
import { StatusBadge } from './StatusBadge'

export function SignerTimeline({ signers }: { signers: Signer[] }) {
  const ordered = [...signers].sort((a, b) => a.order - b.order)
  return (
    <ol aria-label="Signers" className="space-y-3">
      {ordered.map((signer) => {
        const when = signer.signed_at ?? signer.rejected_at
        return (
          <li
            key={signer.id}
            data-signer-status={signer.status}
            className="flex gap-3 rounded border border-slate-200 p-3"
          >
            <span
              aria-hidden="true"
              className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-slate-200 text-sm font-medium"
            >
              {signer.order}
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{signer.full_name}</span>
                <StatusBadge status={signer.status} />
              </div>
              <p className="break-all text-sm text-slate-600">{signer.email}</p>
              {when && <p className="text-sm text-slate-600">{formatDateTime(when)}</p>}
              {signer.status === 'rejected' && signer.rejection_reason && (
                <p className="mt-1 text-sm text-red-900">Reason: {signer.rejection_reason}</p>
              )}
            </div>
          </li>
        )
      })}
    </ol>
  )
}
