import { useDocumentEvents } from '../api/documents'
import { formatDateTime } from '../lib/format'

export function AuditLogPanel({ documentId }: { documentId: string }) {
  const query = useDocumentEvents(documentId)

  return (
    <section aria-labelledby="audit-heading" className="mt-6">
      <h2 id="audit-heading" className="text-lg font-semibold">
        Audit log
      </h2>
      {query.isPending && <p className="mt-2 text-sm text-slate-600">Loading audit log...</p>}
      {query.isError && (
        <div role="alert" className="mt-2 text-sm text-red-900">
          Could not load the audit log.{' '}
          <button type="button" onClick={() => void query.refetch()} className="underline">
            Retry
          </button>
        </div>
      )}
      {query.data && query.data.items.length === 0 && (
        <p className="mt-2 text-sm text-slate-600">No events yet.</p>
      )}
      {query.data && query.data.items.length > 0 && (
        <ol className="mt-2 space-y-2">
          {query.data.items.map((event) => (
            <li key={event.id} className="rounded border border-slate-200 p-2 text-sm">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <span className="font-mono font-medium">{event.type}</span>
                <span className="text-xs text-slate-600">{formatDateTime(event.created_at)}</span>
              </div>
              <p className="text-slate-700">Actor: {event.actor}</p>
              <details className="mt-1">
                <summary className="cursor-pointer text-xs text-slate-700">
                  Data for event #{event.id}
                </summary>
                <pre className="mt-1 overflow-x-auto rounded bg-slate-100 p-2 text-xs">
                  {JSON.stringify(event.data, null, 2)}
                </pre>
              </details>
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}
