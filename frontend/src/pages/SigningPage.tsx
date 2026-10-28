import { useState, type FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { ApiError } from '../api/client'
import { signingFileUrl, useApprove, useReject, useSigningView } from '../api/signing'
import type { DecisionResult, SigningView } from '../api/types'
import { CopyButton } from '../components/CopyButton'
import { Modal } from '../components/Modal'
import { StatusBadge } from '../components/StatusBadge'
import { formatBytes } from '../lib/format'

const MAX_REASON = 500

type Override = 'waiting' | 'closed' | null

function Shell({ children }: { children: React.ReactNode }) {
  return <main className="mx-auto w-full max-w-md px-4 py-6">{children}</main>
}

function Notice({
  title,
  children,
  tone = 'neutral',
}: {
  title: string
  children?: React.ReactNode
  tone?: 'neutral' | 'success' | 'warning' | 'danger'
}) {
  const tones = {
    neutral: 'border-slate-300 bg-slate-50',
    success: 'border-green-500 bg-green-50',
    warning: 'border-amber-400 bg-amber-50',
    danger: 'border-red-400 bg-red-50',
  }
  return (
    <div role="status" className={`mt-4 rounded border p-3 ${tones[tone]}`}>
      <p className="font-medium">{title}</p>
      {children && <div className="mt-1 text-sm text-slate-700">{children}</div>}
    </div>
  )
}

function RejectDialog({
  pending,
  serverError,
  onSubmit,
  onClose,
}: {
  pending: boolean
  serverError: string | null
  onSubmit: (reason: string) => void
  onClose: () => void
}) {
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string | null>(null)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = reason.trim()
    if (trimmed.length === 0) {
      setError('Reason is required')
      return
    }
    if (trimmed.length > MAX_REASON) {
      setError(`Reason must be at most ${MAX_REASON} characters`)
      return
    }
    setError(null)
    onSubmit(trimmed)
  }

  const shown = error ?? serverError
  return (
    <Modal title="Reject document" onClose={onClose}>
      <form onSubmit={submit} noValidate className="mt-2">
        <label htmlFor="reject-reason" className="block text-sm font-medium text-slate-700">
          Reason for rejection
        </label>
        <textarea
          id="reject-reason"
          rows={4}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          aria-invalid={shown ? true : undefined}
          aria-describedby={shown ? 'reject-reason-error' : undefined}
          className="mt-1 block w-full rounded border border-slate-300 px-3 py-2 text-sm"
        />
        {shown && (
          <p id="reject-reason-error" className="mt-1 text-sm text-red-700">
            {shown}
          </p>
        )}
        <div className="mt-4 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <button
            type="button"
            onClick={onClose}
            className="rounded border border-slate-300 px-4 py-2 text-sm hover:bg-slate-100"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={pending}
            className="rounded bg-red-700 px-4 py-2 text-sm font-medium text-white hover:bg-red-800 disabled:opacity-60"
          >
            Confirm rejection
          </button>
        </div>
      </form>
    </Modal>
  )
}

function stateNotice(view: SigningView, override: Override, result: DecisionResult | null) {
  if (result) {
    return result.signer_status === 'signed' ? (
      <Notice title="Thank you, you approved this document" tone="success">
        Your approval was recorded. This is a simulated signature, not a legally binding
        electronic signature.
      </Notice>
    ) : (
      <Notice title="You rejected this document" tone="danger">
        Your decision was recorded.
      </Notice>
    )
  }
  if (view.signer.status === 'signed') {
    return <Notice title="You have already signed this document" tone="success" />
  }
  if (view.signer.status === 'rejected') {
    return <Notice title="You rejected this document" tone="danger" />
  }
  if (override === 'closed' || view.document.status !== 'in_progress') {
    return (
      <Notice title="This document is closed" tone="neutral">
        It is {view.document.status.replace('_', ' ')} and can no longer be signed.
      </Notice>
    )
  }
  if (override === 'waiting' || !view.can_act) {
    return (
      <Notice title="Waiting for earlier signers" tone="warning">
        It is not your turn yet. Open this link again after the previous signers have decided.
      </Notice>
    )
  }
  return null
}

export function SigningPage() {
  const { token = '' } = useParams()
  const query = useSigningView(token)
  const approve = useApprove(token)
  const reject = useReject(token)
  const [rejecting, setRejecting] = useState(false)
  const [override, setOverride] = useState<Override>(null)
  const [result, setResult] = useState<DecisionResult | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [reasonError, setReasonError] = useState<string | null>(null)

  if (query.isPending) {
    return (
      <Shell>
        <div role="status" aria-live="polite" className="animate-pulse space-y-3">
          <div className="h-6 w-2/3 rounded bg-slate-200" />
          <div className="h-4 w-full rounded bg-slate-200" />
          <div className="h-12 w-full rounded bg-slate-200" />
          <span className="sr-only">Loading document</span>
        </div>
      </Shell>
    )
  }

  if (query.isError) {
    const notFound = query.error instanceof ApiError && query.error.status === 404
    if (notFound) {
      return (
        <Shell>
          <h1 className="text-xl font-semibold">Invalid signing link</h1>
          <p className="mt-2 text-sm text-slate-700">
            This link is invalid, has expired or is no longer available. Ask the sender for a new
            one.
          </p>
        </Shell>
      )
    }
    return (
      <Shell>
        <div role="alert">
          <h1 className="text-xl font-semibold">Could not load the document</h1>
          <p className="mt-2 text-sm text-slate-700">{query.error.message}</p>
          <button
            type="button"
            onClick={() => void query.refetch()}
            className="mt-3 rounded border border-slate-300 px-4 py-2 text-sm hover:bg-slate-100"
          >
            Retry
          </button>
        </div>
      </Shell>
    )
  }

  const view = query.data
  const busy = approve.isPending || reject.isPending
  const showActions = !result && override === null && view.can_act && view.document.status === 'in_progress'

  const handleError = (error: unknown): boolean => {
    if (error instanceof ApiError && error.status === 409) {
      if (error.code === 'not_your_turn') setOverride('waiting')
      else if (error.code === 'document_closed') setOverride('closed')
      else setActionError(error.message)
      return true
    }
    if (error instanceof ApiError && error.status === 404) return false
    setActionError(error instanceof Error ? error.message : 'Request failed')
    return true
  }

  const onApprove = () => {
    setActionError(null)
    approve.mutate(undefined, {
      onSuccess: (res) => setResult(res),
      onError: handleError,
    })
  }

  const onReject = (reason: string) => {
    setReasonError(null)
    reject.mutate(reason, {
      onSuccess: (res) => {
        setRejecting(false)
        setResult(res)
      },
      onError: (error) => {
        if (error instanceof ApiError && error.status === 400 && error.fields.reason) {
          setReasonError(error.fields.reason)
          return
        }
        setRejecting(false)
        handleError(error)
      },
    })
  }

  return (
    <Shell>
      <p className="text-sm text-slate-700">
        Signer {view.signer.order} of {view.signer.total}: {view.signer.full_name}
      </p>
      <div className="mt-1 flex flex-wrap items-center gap-2">
        <h1 className="break-words text-xl font-semibold">{view.document.title}</h1>
        <StatusBadge status={view.document.status} />
      </div>
      <p className="mt-1 break-all text-sm text-slate-700">
        {view.document.file_name} ({formatBytes(view.document.size_bytes)})
      </p>
      <div className="mt-3">
        <span className="text-sm text-slate-700">SHA-256</span>
        <div className="mt-1 flex items-start gap-2">
          <code data-testid="sha256" className="min-w-0 flex-1 break-all font-mono text-xs">
            {view.document.sha256}
          </code>
          <CopyButton value={view.document.sha256} label="Copy SHA-256" />
        </div>
      </div>
      <a
        href={signingFileUrl(token)}
        download={view.document.file_name}
        className="mt-4 block rounded border border-slate-300 px-4 py-2 text-center text-sm hover:bg-slate-100"
      >
        Download file
      </a>

      {stateNotice(view, override, result)}

      {actionError && (
        <p role="alert" className="mt-3 text-sm text-red-700">
          {actionError}
        </p>
      )}

      {showActions && (
        <div className="mt-6 flex flex-col gap-2">
          <button
            type="button"
            disabled={busy}
            onClick={onApprove}
            className="w-full rounded bg-green-700 px-4 py-3 text-base font-medium text-white hover:bg-green-800 disabled:opacity-60"
          >
            {approve.isPending ? 'Approving...' : 'Approve'}
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => setRejecting(true)}
            className="w-full rounded border border-red-700 px-4 py-3 text-base font-medium text-red-800 hover:bg-red-50 disabled:opacity-60"
          >
            Reject
          </button>
          <p className="mt-2 text-xs text-slate-600">
            Approving creates a simulated signature for demonstration, not a legally binding
            electronic signature.
          </p>
        </div>
      )}

      {rejecting && (
        <RejectDialog
          pending={reject.isPending}
          serverError={reasonError}
          onSubmit={onReject}
          onClose={() => setRejecting(false)}
        />
      )}
    </Shell>
  )
}
