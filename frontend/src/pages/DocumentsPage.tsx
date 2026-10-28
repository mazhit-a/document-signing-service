import { Link, useSearchParams } from 'react-router-dom'
import { useDocumentsList } from '../api/documents'
import { DOCUMENT_STATUSES, type DocumentDetail, type DocumentStatus } from '../api/types'
import { StatusBadge } from '../components/StatusBadge'
import { useAuth } from '../hooks/useAuth'
import { formatDate } from '../lib/format'

function parseStatus(value: string | null): DocumentStatus | null {
  return DOCUMENT_STATUSES.find((s) => s === value) ?? null
}

function progress(doc: DocumentDetail): string {
  const signed = doc.signers.filter((s) => s.status === 'signed').length
  return `${signed} / ${doc.signers.length} signed`
}

function SkeletonRows() {
  return (
    <ul aria-label="Loading documents" className="space-y-2">
      {[0, 1, 2].map((i) => (
        <li key={i} className="h-16 animate-pulse rounded bg-slate-200" data-testid="skeleton-row" />
      ))}
    </ul>
  )
}

export function DocumentsPage() {
  const { logout } = useAuth()
  const [params, setParams] = useSearchParams()
  const status = parseStatus(params.get('status'))
  const query = useDocumentsList(status)
  const docs = query.data?.pages.flatMap((p) => p.items) ?? []

  const onFilter = (value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set('status', value)
    else next.delete('status')
    setParams(next)
  }

  return (
    <main className="mx-auto max-w-3xl p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-2xl font-semibold">Documents</h1>
        <div className="flex gap-2">
          <Link
            to="/documents/new"
            className="rounded bg-blue-700 px-3 py-1 text-sm font-medium text-white hover:bg-blue-800"
          >
            New document
          </Link>
          <button
            type="button"
            onClick={logout}
            className="rounded border border-slate-300 px-3 py-1 text-sm hover:bg-slate-100"
          >
            Sign out
          </button>
        </div>
      </div>

      <div className="mt-4">
        <label htmlFor="status-filter" className="block text-sm font-medium text-slate-700">
          Status
        </label>
        <select
          id="status-filter"
          value={status ?? ''}
          onChange={(e) => onFilter(e.target.value)}
          className="mt-1 block w-full rounded border border-slate-300 px-3 py-2 text-sm sm:w-56"
        >
          <option value="">All</option>
          {DOCUMENT_STATUSES.map((s) => (
            <option key={s} value={s}>
              {s.replace('_', ' ')}
            </option>
          ))}
        </select>
      </div>

      <div className="mt-4">
        {query.isPending && <SkeletonRows />}

        {query.isError && (
          <div role="alert" className="rounded border border-red-300 bg-red-50 p-4 text-sm text-red-900">
            <p>Could not load documents: {query.error.message}</p>
            <button
              type="button"
              onClick={() => void query.refetch()}
              className="mt-2 rounded border border-red-400 px-3 py-1 hover:bg-red-100"
            >
              Retry
            </button>
          </div>
        )}

        {query.isSuccess && docs.length === 0 && (
          <div className="rounded border border-dashed border-slate-300 p-8 text-center">
            <p className="text-slate-700">
              {status ? 'No documents with this status.' : 'You have no documents yet.'}
            </p>
            {!status && (
              <Link
                to="/documents/new"
                className="mt-3 inline-block rounded bg-blue-700 px-4 py-2 text-sm font-medium text-white hover:bg-blue-800"
              >
                Create your first document
              </Link>
            )}
          </div>
        )}

        {docs.length > 0 && (
          <ul className="space-y-2">
            {docs.map((doc) => (
              <li key={doc.id} className="rounded border border-slate-200 bg-white p-3">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <Link
                    to={`/documents/${doc.id}`}
                    className="break-words font-medium text-blue-800 underline"
                  >
                    {doc.title}
                  </Link>
                  <StatusBadge status={doc.status} />
                </div>
                <p className="mt-1 text-sm text-slate-600">
                  {progress(doc)} · {formatDate(doc.created_at)}
                </p>
              </li>
            ))}
          </ul>
        )}

        {query.hasNextPage && (
          <button
            type="button"
            onClick={() => void query.fetchNextPage()}
            disabled={query.isFetchingNextPage}
            className="mt-4 w-full rounded border border-slate-300 px-4 py-2 text-sm hover:bg-slate-100 disabled:opacity-60"
          >
            {query.isFetchingNextPage ? 'Loading...' : 'Load more'}
          </button>
        )}
      </div>
    </main>
  )
}
