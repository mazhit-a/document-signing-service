import { useForm } from 'react-hook-form'
import { Link, Navigate, useNavigate } from 'react-router-dom'
import * as authApi from '../api/auth'
import { useAuth } from '../hooks/useAuth'
import { useToast } from '../hooks/useToast'
import { FormField } from '../components/FormField'
import { applyApiError } from '../lib/formErrors'

interface RegisterForm {
  full_name: string
  email: string
  password: string
}

const FIELDS = ['full_name', 'email', 'password'] as const

export function RegisterPage() {
  const { isAuthenticated } = useAuth()
  const { notify } = useToast()
  const navigate = useNavigate()
  const {
    register,
    handleSubmit,
    setError,
    formState: { errors, isSubmitting },
  } = useForm<RegisterForm>()

  if (isAuthenticated) return <Navigate to="/documents" replace />

  const onSubmit = handleSubmit(async (values) => {
    try {
      await authApi.register(values)
      notify('Account created. Please sign in.', 'success')
      navigate('/login', { replace: true })
    } catch (err) {
      applyApiError(err, setError, FIELDS)
    }
  })

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col justify-center p-4">
      <h1 className="mb-6 text-2xl font-semibold">Create account</h1>
      <form onSubmit={onSubmit} noValidate className="space-y-4">
        <FormField
          label="Full name"
          autoComplete="name"
          registration={register('full_name', { required: 'Full name is required' })}
          error={errors.full_name?.message}
        />
        <FormField
          label="Email"
          type="email"
          autoComplete="email"
          registration={register('email', { required: 'Email is required' })}
          error={errors.email?.message}
        />
        <FormField
          label="Password"
          type="password"
          autoComplete="new-password"
          registration={register('password', {
            required: 'Password is required',
            minLength: { value: 8, message: 'Password must be at least 8 characters' },
          })}
          error={errors.password?.message}
        />
        {errors.root?.message && (
          <p role="alert" className="text-sm text-red-700">
            {errors.root.message}
          </p>
        )}
        <button
          type="submit"
          disabled={isSubmitting}
          className="w-full rounded bg-blue-700 px-4 py-2 text-sm font-medium text-white hover:bg-blue-800 disabled:opacity-60"
        >
          {isSubmitting ? 'Creating...' : 'Create account'}
        </button>
      </form>
      <p className="mt-4 text-sm">
        Already registered?{' '}
        <Link to="/login" className="text-blue-700 underline">
          Sign in
        </Link>
      </p>
    </main>
  )
}
