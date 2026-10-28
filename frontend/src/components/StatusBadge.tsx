import type { DocumentStatus, SignerStatus } from '../api/types'

type Status = DocumentStatus | SignerStatus

const config: Record<Status, { label: string; className: string }> = {
  draft: { label: 'Draft', className: 'bg-gray-100 text-gray-800 ring-gray-400' },
  in_progress: { label: 'In progress', className: 'bg-blue-100 text-blue-900 ring-blue-400' },
  completed: { label: 'Completed', className: 'bg-green-100 text-green-900 ring-green-500' },
  rejected: { label: 'Rejected', className: 'bg-red-100 text-red-900 ring-red-400' },
  cancelled: { label: 'Cancelled', className: 'bg-slate-200 text-slate-800 ring-slate-500' },
  waiting: { label: 'Waiting', className: 'bg-gray-100 text-gray-800 ring-gray-400' },
  pending: { label: 'Pending', className: 'bg-amber-100 text-amber-900 ring-amber-400' },
  signed: { label: 'Signed', className: 'bg-green-100 text-green-900 ring-green-500' },
}

export function StatusBadge({ status }: { status: Status }) {
  const { label, className } = config[status]
  return (
    <span
      data-status={status}
      className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ring-1 ring-inset ${className}`}
    >
      {label}
    </span>
  )
}
