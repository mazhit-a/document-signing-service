import { useId, useRef, useState, type DragEvent } from 'react'
import { formatBytes } from '../lib/format'

export const MAX_FILE_BYTES = 10 * 1024 * 1024
export const ALLOWED_EXTENSIONS = ['.pdf', '.docx', '.txt'] as const

export function validateFile(file: File): string | null {
  const name = file.name.toLowerCase()
  if (!ALLOWED_EXTENSIONS.some((ext) => name.endsWith(ext))) {
    return 'Only PDF, DOCX or TXT files are allowed'
  }
  if (file.size > MAX_FILE_BYTES) return 'File must be 10 MB or smaller'
  if (file.size === 0) return 'File is empty'
  return null
}

interface FileDropzoneProps {
  label: string
  file: File | null
  error?: string
  onChange: (file: File | null) => void
}

export function FileDropzone({ label, file, error, onChange }: FileDropzoneProps) {
  const inputId = useId()
  const errorId = `${inputId}-error`
  const inputRef = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    setDragging(false)
    const dropped = e.dataTransfer.files[0]
    if (dropped) onChange(dropped)
  }

  return (
    <div>
      <label htmlFor={inputId} className="block text-sm font-medium text-slate-700">
        {label}
      </label>
      <div
        onDragOver={(e) => {
          e.preventDefault()
          setDragging(true)
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
        className={`mt-1 rounded border-2 border-dashed p-4 text-center text-sm ${
          dragging ? 'border-blue-600 bg-blue-50' : 'border-slate-300'
        }`}
      >
        <input
          ref={inputRef}
          id={inputId}
          type="file"
          accept=".pdf,.docx,.txt"
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorId : undefined}
          className="block w-full text-sm"
          onChange={(e) => onChange(e.target.files?.[0] ?? null)}
        />
        <p className="mt-2 text-slate-600">Drop a file here. PDF, DOCX or TXT, up to 10 MB.</p>
        {file && (
          <p className="mt-2 break-all font-medium text-slate-900" data-testid="file-summary">
            {file.name} ({formatBytes(file.size)})
          </p>
        )}
      </div>
      {error && (
        <p id={errorId} className="mt-1 text-sm text-red-700">
          {error}
        </p>
      )}
    </div>
  )
}
