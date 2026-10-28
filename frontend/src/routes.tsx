import { Navigate, type RouteObject } from 'react-router-dom'
import { ProtectedRoute } from './components/ProtectedRoute'
import { RouteError } from './components/RouteError'
import { DocumentsPage } from './pages/DocumentsPage'
import { LoginPage } from './pages/LoginPage'
import { NotFoundPage } from './pages/NotFoundPage'
import { RegisterPage } from './pages/RegisterPage'

export const appRoutes: RouteObject[] = [
  {
    errorElement: <RouteError />,
    children: [
      { path: '/', element: <Navigate to="/documents" replace /> },
      { path: '/login', element: <LoginPage /> },
      { path: '/register', element: <RegisterPage /> },
      {
        element: <ProtectedRoute />,
        children: [{ path: '/documents', element: <DocumentsPage /> }],
      },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
]
