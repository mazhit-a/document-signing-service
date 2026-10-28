import { Modal } from './Modal'

interface ConfirmDialogProps {
  title: string
  message: string
  confirmLabel: string
  pending: boolean
  onConfirm: () => void
  onClose: () => void
}

export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  pending,
  onConfirm,
  onClose,
}: ConfirmDialogProps) {
  return (
    <Modal title={title} onClose={onClose}>
      <p className="mt-2 text-sm text-slate-700">{message}</p>
      <div className="mt-4 flex justify-end gap-2">
        <button
          type="button"
          onClick={onClose}
          className="rounded border border-slate-300 px-3 py-1 text-sm hover:bg-slate-100"
        >
          Keep document
        </button>
        <button
          type="button"
          disabled={pending}
          onClick={onConfirm}
          className="rounded bg-red-700 px-3 py-1 text-sm font-medium text-white hover:bg-red-800 disabled:opacity-60"
        >
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}
