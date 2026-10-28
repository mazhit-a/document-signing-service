import type { FieldValues, Path, UseFormSetError } from 'react-hook-form'
import { ApiError } from '../api/client'

export function applyApiError<T extends FieldValues>(
  err: unknown,
  setError: UseFormSetError<T>,
  knownFields: readonly Path<T>[],
): void {
  if (!(err instanceof ApiError)) {
    setError('root', { message: 'Something went wrong. Please try again.' })
    return
  }
  let mapped = 0
  for (const [field, message] of Object.entries(err.fields)) {
    const match = knownFields.find((f) => f === field)
    if (match) {
      setError(match, { message })
      mapped++
    }
  }
  if (mapped === 0) setError('root', { message: err.message })
}
