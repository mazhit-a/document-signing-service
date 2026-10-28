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
    <Modal title="Signing links" onClose={onClose}>
      <p
        role="alert"
        className="mt-2 rounded border border-amber-500 bg-amber-50 p-2 text-sm text-amber-900"
      >
        Warning: these links are shown only once. Copy them now and send each to its signer.
        They cannot be shown again.
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
          I have copied the links
        </button>
      </div>
    </Modal>
  )
}
