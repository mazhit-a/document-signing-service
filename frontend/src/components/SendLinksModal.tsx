import type { Signer, SigningLink } from '../api/types'
import { CopyButton } from './CopyButton'
import { Modal } from './Modal'

interface SendLinksModalProps {
  links: SigningLink[]
  signers: Signer[]
  onClose: () => void
}

export function SendLinksModal({ links, signers, onClose }: SendLinksModalProps) {
  const nameFor = (link: SigningLink): string =>
    signers.find((s) => s.id === link.signer_id)?.full_name ?? `Signer ${link.order}`

  return (
    <Modal title="Signing link" onClose={onClose}>
      <p
        role="alert"
        className="mt-2 rounded border border-amber-500 bg-amber-50 p-2 text-sm text-amber-900"
      >
        Warning: this link is shown only once. Copy it now and send it to the first signer. It
        cannot be shown again.
      </p>
      <p className="mt-2 text-sm text-slate-700">
        Only the first signer&apos;s link is shown. Each following signer is emailed their link
        when it becomes their turn.
      </p>
      <ol className="mt-3 space-y-3">
        {links.map((link) => (
          <li key={link.signer_id} className="rounded border border-slate-200 p-2">
            <p className="text-sm font-medium">
              {link.order}. {nameFor(link)}
            </p>
            <div className="mt-1 flex items-center gap-2">
              <code className="min-w-0 flex-1 break-all text-xs">{link.url}</code>
              <CopyButton value={link.url} label={`Copy link for ${nameFor(link)}`} />
            </div>
          </li>
        ))}
      </ol>
      <div className="mt-4 flex justify-end">
        <button
          type="button"
          onClick={onClose}
          className="rounded bg-blue-700 px-3 py-1 text-sm font-medium text-white hover:bg-blue-800"
        >
          I have copied the link
        </button>
      </div>
    </Modal>
  )
}
