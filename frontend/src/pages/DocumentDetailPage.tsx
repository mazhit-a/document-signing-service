import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  downloadDocumentFile,
  useCancelDocument,
  useDocument,
  useSendDocument,
  useVerifyDocument,
} from '../api/documents'
import { ApiError } from '../api/client'
import type { SigningLink } from '../api/types'
import { AuditLogPanel } from '../components/AuditLogPanel'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { CopyButton } from '../components/CopyButton'
import { SendLinksModal } from '../components/SendLinksModal'
import { SignerTimeline } from '../components/SignerTimeline'
import { StatusBadge } from '../components/StatusBadge'
import { VerifyPanel } from '../components/VerifyPanel'
import { useToast } from '../hooks/useToast'
import { formatBytes, formatDateTime } from '../lib/format'

function errorMessage(error: unknown): string {
  return error instanceof ApiError ? error.message : 'Something went wrong'
}

export function DocumentDetailPage() {
  const { id = '' } = useParams()
  const { notify } = useToast()
  const query = useDocument(id)
  const send = useSendDocument(id)
  const cancel = useCancelDocument(id)
  const verify = useVerifyDocument(id)
  const [links, setLinks] = useState<SigningLink[] | null>(null)
  const [confirmingCancel, setConfirmingCancel] = useState(false)

  if (query.isPending) {
    return (
      <main className="mx-auto max-w-3xl p-4">
        <div aria-label="Loading document" className="h-32 animate-pulse rounded bg-slate-200" />
      </main>
    )
  }

  if (query.isError) {
    const notFound = query.error instanceof ApiError && query.error.status === 404
    return (
      <main className="mx-auto max-w-3xl p-4">
        <div role="alert" className="rounded border border-red-600 bg-red-50 p-3 text-red-900">
          {notFound ? 'Document not found.' : 'Could not load the document.'}
          {!notFound && (
            <button type="button" onClick={() => void query.refetch()} className="ml-2 underline">
              Retry
            </button>
          )}
        </div>
        <Link to="/documents" className="mt-3 inline-block text-sm text-blue-800 underline">
          Back to documents
        </Link>
      </main>
    )
  }

  const doc = query.data

  const onSend = () => {
    send.mutate(undefined, {
      onSuccess: (res) => setLinks(res.signing_links),
      onError: (error) => notify(errorMessage(error), 'error'),
    })
  }

  const onConfirmCancel = () => {
    cancel.mutate(undefined, {
      onSuccess: () => {
        setConfirmingCancel(false)
        notify('Document cancelled', 'success')
      },
      onError: (error) => {
        setConfirmingCancel(false)
        notify(errorMessage(error), 'error')
      },
    })
  }

  const onDownload = () => {
    downloadDocumentFile(doc.id, doc.file.name).catch((error: unknown) =>
      notify(errorMessage(error), 'error'),
    )
  }

  const canSend = doc.status === 'draft'
  const canCancel = doc.status === 'draft' || doc.status === 'in_progress'

  return (
    <main className="mx-auto max-w-3xl p-4">
      <Link to="/documents" className="text-sm text-blue-800 underline">
        Back to documents
      </Link>
      <header className="mt-2">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-2xl font-semibold">{doc.title}</h1>
          <StatusBadge status={doc.status} />
        </div>
        <p className="mt-1 text-sm text-slate-700">
          {doc.file.name} ({formatBytes(doc.file.size_bytes)}) - created{' '}
          {formatDateTime(doc.created_at)}
        </p>
        <div className="mt-2 flex items-center gap-2">
          <span className="text-sm text-slate-700">SHA-256</span>
          <code data-testid="sha256" className="min-w-0 flex-1 break-all font-mono text-xs">
            {doc.file.sha256}
          </code>
          <CopyButton value={doc.file.sha256} label="Copy SHA-256" />
        </div>
        <div className="mt-3 flex flex-wrap gap-2">
          <button
            type="button"
            onClick={onDownload}
            className="rounded border border-slate-300 px-3 py-1 text-sm hover:bg-slate-100"
          >
            Download file
          </button>
          {canSend && (
            <button
              type="button"
              disabled={send.isPending}
              onClick={onSend}
              className="rounded bg-blue-700 px-3 py-1 text-sm font-medium text-white hover:bg-blue-800 disabled:opacity-60"
            >
              Send for signing
            </button>
          )}
          {canCancel && (
            <button
              type="button"
              onClick={() => setConfirmingCancel(true)}
              className="rounded border border-red-700 px-3 py-1 text-sm text-red-800 hover:bg-red-50"
            >
              Cancel document
            </button>
          )}
        </div>
      </header>

      <section aria-labelledby="signers-heading" className="mt-6">
        <h2 id="signers-heading" className="mb-2 text-lg font-semibold">
          Signers
        </h2>
        <SignerTimeline signers={doc.signers} />
      </section>

      <section aria-labelledby="verify-heading" className="mt-6">
        <h2 id="verify-heading" className="text-lg font-semibold">
          Integrity
        </h2>
        <button
          type="button"
          disabled={verify.isFetching}
          onClick={() => void verify.refetch()}
          className="mt-2 rounded border border-slate-300 px-3 py-1 text-sm hover:bg-slate-100 disabled:opacity-60"
        >
          Verify integrity
        </button>
        {verify.isFetching && <p className="mt-2 text-sm text-slate-600">Verifying...</p>}
        {verify.isError && !verify.isFetching && (
          <p role="alert" className="mt-2 text-sm text-red-900">
            Verification could not be completed: {errorMessage(verify.error)}
          </p>
        )}
        {verify.data && !verify.isFetching && (
          <VerifyPanel report={verify.data} signers={doc.signers} />
        )}
      </section>

      <AuditLogPanel documentId={doc.id} />

      {links && (
        <SendLinksModal links={links} signers={doc.signers} onClose={() => setLinks(null)} />
      )}
      {confirmingCancel && (
        <ConfirmDialog
          title="Cancel this document?"
          message="Signing links will stop working and no further signatures can be added. This cannot be undone."
          confirmLabel="Cancel document"
          pending={cancel.isPending}
          onConfirm={onConfirmCancel}
          onClose={() => setConfirmingCancel(false)}
        />
      )}
    </main>
  )
}
