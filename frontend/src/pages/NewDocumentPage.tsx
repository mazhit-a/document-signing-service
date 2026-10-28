import { useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useCreateDocument } from '../api/documents'
import { ApiError } from '../api/client'
import { FileDropzone, validateFile } from '../components/FileDropzone'

const MAX_SIGNERS = 10
const MAX_TITLE = 200
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

interface SignerRow {
  key: number
  full_name: string
  email: string
}

interface RowErrors {
  full_name?: string
  email?: string
}

interface FormErrors {
  title?: string
  file?: string
  signers?: string
  rows: Record<number, RowErrors>
  root?: string
}

const emptyErrors = (): FormErrors => ({ rows: {} })

function validate(title: string, file: File | null, rows: SignerRow[]): FormErrors {
  const errors = emptyErrors()
  const trimmed = title.trim()
  if (!trimmed) errors.title = 'Title is required'
  else if (trimmed.length > MAX_TITLE) errors.title = `Title must be at most ${MAX_TITLE} characters`

  if (!file) errors.file = 'Choose a file to upload'
  else {
    const fileError = validateFile(file)
    if (fileError) errors.file = fileError
  }

  if (rows.length === 0) errors.signers = 'Add at least one signer'

  const seen = new Map<string, number>()
  for (const row of rows) {
    const rowErrors: RowErrors = {}
    if (!row.full_name.trim()) rowErrors.full_name = 'Name is required'
    const email = row.email.trim().toLowerCase()
    if (!email) rowErrors.email = 'Email is required'
    else if (!EMAIL_RE.test(email)) rowErrors.email = 'Enter a valid email address'
    else if (seen.has(email)) rowErrors.email = 'Duplicate email'
    else seen.set(email, row.key)
    if (rowErrors.full_name || rowErrors.email) errors.rows[row.key] = rowErrors
  }
  return errors
}

function hasErrors(e: FormErrors): boolean {
  return Boolean(e.title || e.file || e.signers || Object.keys(e.rows).length > 0)
}

const inputClass =
  'mt-1 block w-full rounded border border-slate-300 px-3 py-2 text-sm focus:border-blue-600 focus:outline-none focus:ring-1 focus:ring-blue-600'
const smallButton =
  'rounded border border-slate-300 px-2 py-1 text-sm hover:bg-slate-100 disabled:opacity-40'

export function NewDocumentPage() {
  const navigate = useNavigate()
  const create = useCreateDocument()
  const nextKey = useRef(2)
  const [title, setTitle] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [rows, setRows] = useState<SignerRow[]>([{ key: 1, full_name: '', email: '' }])
  const [errors, setErrors] = useState<FormErrors>(emptyErrors())

  const updateRow = (key: number, patch: Partial<SignerRow>) =>
    setRows((prev) => prev.map((r) => (r.key === key ? { ...r, ...patch } : r)))

  const addRow = () =>
    setRows((prev) => [...prev, { key: nextKey.current++, full_name: '', email: '' }])

  const removeRow = (key: number) => setRows((prev) => prev.filter((r) => r.key !== key))

  const moveRow = (index: number, delta: -1 | 1) =>
    setRows((prev) => {
      const target = index + delta
      if (target < 0 || target >= prev.length) return prev
      const a = prev[index]
      const b = prev[target]
      if (!a || !b) return prev
      const next = [...prev]
      next[index] = b
      next[target] = a
      return next
    })

  const applyServerError = (err: unknown) => {
    const next = emptyErrors()
    if (!(err instanceof ApiError)) {
      next.root = 'Something went wrong. Please try again.'
      setErrors(next)
      return
    }
    let mapped = 0
    for (const [field, message] of Object.entries(err.fields)) {
      if (field === 'title' || field === 'file' || field === 'signers') {
        next[field] = message
        mapped++
        continue
      }
      const m = /^signers\[(\d+)\]\.?(full_name|email)?$/.exec(field)
      const row = m ? rows[Number(m[1])] : undefined
      if (m && row) {
        const prop = m[2] === 'full_name' ? 'full_name' : 'email'
        next.rows[row.key] = { ...next.rows[row.key], [prop]: message }
        mapped++
      }
    }
    if (mapped === 0) next.root = err.message
    setErrors(next)
  }

  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    const found = validate(title, file, rows)
    setErrors(found)
    if (hasErrors(found) || !file) return
    create.mutate(
      {
        title: title.trim(),
        file,
        signers: rows.map((r) => ({ full_name: r.full_name.trim(), email: r.email.trim() })),
      },
      {
        onSuccess: (doc) => navigate(`/documents/${doc.id}`),
        onError: applyServerError,
      },
    )
  }

  return (
    <main className="mx-auto max-w-2xl p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">New document</h1>
        <Link to="/documents" className="text-sm text-blue-800 underline">
          Back to documents
        </Link>
      </div>

      <form onSubmit={onSubmit} noValidate className="mt-4 space-y-6">
        <div>
          <label htmlFor="doc-title" className="block text-sm font-medium text-slate-700">
            Title
          </label>
          <input
            id="doc-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            aria-invalid={errors.title ? true : undefined}
            aria-describedby={errors.title ? 'doc-title-error' : undefined}
            className={inputClass}
          />
          {errors.title && (
            <p id="doc-title-error" className="mt-1 text-sm text-red-700">
              {errors.title}
            </p>
          )}
        </div>

        <FileDropzone label="File" file={file} error={errors.file} onChange={setFile} />

        <fieldset>
          <legend className="text-sm font-medium text-slate-700">
            Signers (in signing order)
          </legend>
          {errors.signers && (
            <p role="alert" className="mt-1 text-sm text-red-700">
              {errors.signers}
            </p>
          )}
          <ol className="mt-2 space-y-3">
            {rows.map((row, index) => {
              const rowErr = errors.rows[row.key] ?? {}
              const n = index + 1
              const nameErrId = `signer-${row.key}-name-error`
              const emailErrId = `signer-${row.key}-email-error`
              return (
                <li
                  key={row.key}
                  aria-label={`Signer ${n}`}
                  className="rounded border border-slate-200 bg-white p-3"
                >
                  <p className="text-sm font-medium">Signer {n}</p>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div>
                      <label
                        htmlFor={`signer-${row.key}-name`}
                        className="block text-sm text-slate-700"
                      >
                        Full name (signer {n})
                      </label>
                      <input
                        id={`signer-${row.key}-name`}
                        value={row.full_name}
                        onChange={(e) => updateRow(row.key, { full_name: e.target.value })}
                        aria-invalid={rowErr.full_name ? true : undefined}
                        aria-describedby={rowErr.full_name ? nameErrId : undefined}
                        className={inputClass}
                      />
                      {rowErr.full_name && (
                        <p id={nameErrId} className="mt-1 text-sm text-red-700">
                          {rowErr.full_name}
                        </p>
                      )}
                    </div>
                    <div>
                      <label
                        htmlFor={`signer-${row.key}-email`}
                        className="block text-sm text-slate-700"
                      >
                        Email (signer {n})
                      </label>
                      <input
                        id={`signer-${row.key}-email`}
                        type="email"
                        value={row.email}
                        onChange={(e) => updateRow(row.key, { email: e.target.value })}
                        aria-invalid={rowErr.email ? true : undefined}
                        aria-describedby={rowErr.email ? emailErrId : undefined}
                        className={inputClass}
                      />
                      {rowErr.email && (
                        <p id={emailErrId} className="mt-1 text-sm text-red-700">
                          {rowErr.email}
                        </p>
                      )}
                    </div>
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2">
                    <button
                      type="button"
                      className={smallButton}
                      disabled={index === 0}
                      onClick={() => moveRow(index, -1)}
                      aria-label={`Move signer ${n} up`}
                    >
                      Move up
                    </button>
                    <button
                      type="button"
                      className={smallButton}
                      disabled={index === rows.length - 1}
                      onClick={() => moveRow(index, 1)}
                      aria-label={`Move signer ${n} down`}
                    >
                      Move down
                    </button>
                    <button
                      type="button"
                      className={smallButton}
                      disabled={rows.length === 1}
                      onClick={() => removeRow(row.key)}
                      aria-label={`Remove signer ${n}`}
                    >
                      Remove
                    </button>
                  </div>
                </li>
              )
            })}
          </ol>
          <button
            type="button"
            className={`${smallButton} mt-3`}
            disabled={rows.length >= MAX_SIGNERS}
            onClick={addRow}
          >
            Add signer
          </button>
        </fieldset>

        {errors.root && (
          <p role="alert" className="text-sm text-red-700">
            {errors.root}
          </p>
        )}

        <button
          type="submit"
          disabled={create.isPending}
          className="w-full rounded bg-blue-700 px-4 py-2 text-sm font-medium text-white hover:bg-blue-800 disabled:opacity-60 sm:w-auto"
        >
          {create.isPending ? 'Creating...' : 'Create document'}
        </button>
      </form>
    </main>
  )
}
