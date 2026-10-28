import { isRouteErrorResponse, Link, useRouteError } from 'react-router-dom'
import { ApiError } from '../api/client'

export function RouteError() {
  const error = useRouteError()
  let message = 'Something went wrong.'
  if (error instanceof ApiError) message = error.message
  else if (isRouteErrorResponse(error)) message = error.statusText || message

  return (
    <main role="alert" className="mx-auto max-w-md p-8 text-center">
      <h1 className="text-2xl font-semibold">Unexpected error</h1>
      <p className="mt-2 text-slate-600">{message}</p>
      <Link to="/documents" className="mt-4 inline-block text-blue-700 underline">
        Back to documents
      </Link>
    </main>
  )
}
