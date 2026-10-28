import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '../test/server'
import { renderApp } from '../test/render'
import { request, storeToken } from '../api/client'

describe('login flow', () => {
  it('stores the token and redirects to documents', async () => {
    server.use(
      http.post('*/api/v1/auth/login', () =>
        HttpResponse.json({ access_token: 'jwt-123', token_type: 'Bearer', expires_in: 3600 }),
      ),
    )
    const user = userEvent.setup()
    const { router } = renderApp('/login')

    await user.type(screen.getByLabelText('Email'), 'a@x.kz')
    await user.type(screen.getByLabelText('Password'), 'secret-pass')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/documents'))
    expect(localStorage.getItem('kedo_token')).toBe('jwt-123')
    expect(await screen.findByRole('heading', { name: 'Documents' })).toBeInTheDocument()
  })

  it('shows the server message on bad credentials', async () => {
    server.use(
      http.post('*/api/v1/auth/login', () =>
        HttpResponse.json(
          { error: { code: 'unauthorized', message: 'invalid credentials', fields: {} } },
          { status: 401 },
        ),
      ),
    )
    const user = userEvent.setup()
    renderApp('/login')
    await user.type(screen.getByLabelText('Email'), 'a@x.kz')
    await user.type(screen.getByLabelText('Password'), 'wrong')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid credentials')
    expect(localStorage.getItem('kedo_token')).toBeNull()
  })

  it('redirects unauthenticated users to login', async () => {
    const { router } = renderApp('/documents')
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
  })
})

describe('401 handling', () => {
  it('logs out and returns to login', async () => {
    storeToken('stale')
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json(
          { error: { code: 'unauthorized', message: 'expired', fields: {} } },
          { status: 401 },
        ),
      ),
    )
    const { router } = renderApp('/documents')
    expect(await screen.findByRole('heading', { name: 'Documents' })).toBeInTheDocument()

    await request('/auth/me').catch(() => undefined)

    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(localStorage.getItem('kedo_token')).toBeNull()
  })
})

describe('register flow', () => {
  it('maps validation fields onto inputs', async () => {
    server.use(
      http.post('*/api/v1/auth/register', () =>
        HttpResponse.json(
          {
            error: {
              code: 'validation_failed',
              message: 'email: already registered',
              fields: { email: 'already registered' },
            },
          },
          { status: 400 },
        ),
      ),
    )
    const user = userEvent.setup()
    renderApp('/register')
    await user.type(screen.getByLabelText('Full name'), 'A Seitov')
    await user.type(screen.getByLabelText('Email'), 'a@x.kz')
    await user.type(screen.getByLabelText('Password'), 'long-enough-pw')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    const input = screen.getByLabelText('Email')
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
    expect(screen.getByText('already registered')).toBeInTheDocument()
  })
})
