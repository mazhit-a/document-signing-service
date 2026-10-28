import type { InputHTMLAttributes } from 'react'
import type { UseFormRegisterReturn } from 'react-hook-form'

interface FormFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'name'> {
  label: string
  registration: UseFormRegisterReturn
  error?: string
}

export function FormField({ label, registration, error, id, ...rest }: FormFieldProps) {
  const inputId = id ?? `field-${registration.name}`
  const errorId = `${inputId}-error`
  return (
    <div>
      <label htmlFor={inputId} className="block text-sm font-medium text-slate-700">
        {label}
      </label>
      <input
        id={inputId}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errorId : undefined}
        className="mt-1 block w-full rounded border border-slate-300 px-3 py-2 text-sm focus:border-blue-600 focus:outline-none focus:ring-1 focus:ring-blue-600"
        {...registration}
        {...rest}
      />
      {error && (
        <p id={errorId} className="mt-1 text-sm text-red-700">
          {error}
        </p>
      )}
    </div>
  )
}
