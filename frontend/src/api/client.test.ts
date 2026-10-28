import { http, HttpResponse } from 'msw'
import { server } from '../test/server'
import {
  ApiError,
  request,
  setUnauthorizedHandler,
  storeToken,
} from './client'

describe('request', () => {
  afterEach(() => setUnauthorizedHandler(null))

  it('parses the error envelope into ApiError', async () => {
    server.use(
      http.post('*/api/v1/documents', () =>
        HttpResponse.json(
          {
            error: {
              code: 'validation_failed',
              message: 'title: required',
              fields: { title: 'required' },
            },
          },
          { status: 400 },
        ),
      ),
    )
    const err = await request('/documents', { method: 'POST', body: {} }).catch(
      (e: unknown) => e,
    )
    expect(err).toBeInstanceOf(ApiError)
    const apiErr = err as ApiError
    expect(apiErr.status).toBe(400)
    expect(apiErr.code).toBe('validation_failed')
    expect(apiErr.message).toBe('title: required')
    expect(apiErr.fields).toEqual({ title: 'required' })
  })

  it('falls back when the body is not an envelope', async () => {
    server.use(
      http.get('*/api/v1/me', () => new HttpResponse('boom', { status: 500 })),
    )
    const err = (await request('/me').catch((e: unknown) => e)) as ApiError
    expect(err.status).toBe(500)
    expect(err.code).toBe('unknown_error')
  })

  it('attaches the bearer token', async () => {
    storeToken('abc')
    let header: string | null = null
    server.use(
      http.get('*/api/v1/me', ({ request: req }) => {
        header = req.headers.get('authorization')
        return HttpResponse.json({ ok: true })
      }),
    )
    await request('/me')
    expect(header).toBe('Bearer abc')
  })

  it('does not set Content-Type for FormData', async () => {
    let contentType: string | null = null
    server.use(
      http.post('*/api/v1/documents', ({ request: req }) => {
        contentType = req.headers.get('content-type')
        return HttpResponse.json({ ok: true })
      }),
    )
    const form = new FormData()
    form.append('title', 'x')
    await request('/documents', { method: 'POST', body: form })
    expect(contentType).toMatch(/^multipart\/form-data; boundary=/)
  })

  it('calls the unauthorized handler on 401 with a token', async () => {
    storeToken('abc')
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    server.use(
      http.get('*/api/v1/me', () =>
        HttpResponse.json(
          { error: { code: 'unauthorized', message: 'no', fields: {} } },
          { status: 401 },
        ),
      ),
    )
    await expect(request('/me')).rejects.toBeInstanceOf(ApiError)
    expect(handler).toHaveBeenCalledOnce()
  })
})
