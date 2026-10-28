import type { Signer, VerifyReport } from '../api/types'
import { formatDateTime } from '../lib/format'

function Check({ ok, label }: { ok: boolean; label: string }) {
  return (
    <li data-ok={ok} className="flex items-start gap-2 text-sm">
      <span aria-hidden="true">{ok ? '✓' : '✗'}</span>
      <span>
        {label}: <strong>{ok ? 'passed' : 'failed'}</strong>
      </span>
    </li>
  )
}

export function VerifyPanel({ report, signers }: { report: VerifyReport; signers: Signer[] }) {
  const nameFor = (id: string, order: number): string =>
    signers.find((s) => s.id === id)?.full_name ?? `signer ${order}`

  return (
    <div className="mt-3">
      <p
        role="status"
        data-valid={report.valid}
        className={`rounded border p-2 text-sm font-medium ${
          report.valid
            ? 'border-green-600 bg-green-50 text-green-900'
            : 'border-red-600 bg-red-50 text-red-900'
        }`}
      >
        {report.valid
          ? 'Integrity verified: all checks passed'
          : 'Integrity check failed: the document or its history may have been altered'}
      </p>
      <ul className="mt-2 space-y-1">
        <Check ok={report.file.ok} label="File SHA-256 matches the stored hash" />
        {report.signatures.map((s) => (
          <Check
            key={s.signer_id}
            ok={s.seal_ok && s.hash_matches_document}
            label={`Signature seal of ${nameFor(s.signer_id, s.order)}`}
          />
        ))}
        <Check
          ok={report.audit_chain.ok}
          label={`Audit chain (${report.audit_chain.events} events)`}
        />
      </ul>
      {report.audit_chain.first_broken_event_id !== null && (
        <p className="mt-1 text-sm text-red-900">
          First broken audit event: #{report.audit_chain.first_broken_event_id}
        </p>
      )}
      <p className="mt-2 text-xs text-slate-600">
        Checked at {formatDateTime(report.checked_at)}
      </p>
    </div>
  )
}
