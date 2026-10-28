import { useAuth } from '../hooks/useAuth'

export function DocumentsPage() {
  const { logout } = useAuth()
  return (
    <main className="mx-auto max-w-3xl p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Documents</h1>
        <button
          type="button"
          onClick={logout}
          className="rounded border border-slate-300 px-3 py-1 text-sm hover:bg-slate-100"
        >
          Sign out
        </button>
      </div>
    </main>
  )
}
