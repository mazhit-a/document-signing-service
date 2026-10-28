import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
import { server } from './server'

async function encodeMultipart(
  form: FormData,
): Promise<{ body: Uint8Array<ArrayBuffer>; contentType: string }> {
  const boundary = `----vitest${Math.random().toString(16).slice(2)}`
  const enc = new TextEncoder()
  const parts: Uint8Array[] = []
  for (const [name, value] of form.entries()) {
    if (typeof value === 'string') {
      parts.push(
        enc.encode(
          `--${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n${value}\r\n`,
        ),
      )
    } else {
      parts.push(
        enc.encode(
          `--${boundary}\r\nContent-Disposition: form-data; name="${name}"; filename="${value.name}"\r\nContent-Type: ${value.type || 'application/octet-stream'}\r\n\r\n`,
        ),
        new Uint8Array(await value.arrayBuffer()),
        enc.encode('\r\n'),
      )
    }
  }
  parts.push(enc.encode(`--${boundary}--\r\n`))
  const body = new Uint8Array(parts.reduce((n, p) => n + p.length, 0))
  let offset = 0
  for (const part of parts) {
    body.set(part, offset)
    offset += part.length
  }
  return { body, contentType: `multipart/form-data; boundary=${boundary}` }
}

beforeAll(() => {
  server.listen({ onUnhandledFrame: 'error' })
  const interceptedFetch = globalThis.fetch.bind(globalThis)
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const target =
      typeof input === 'string' && input.startsWith('/') ? new URL(input, 'http://localhost') : input
    if (init?.body instanceof FormData) {
      const { body, contentType } = await encodeMultipart(init.body)
      const headers = new Headers(init.headers)
      headers.set('Content-Type', contentType)
      return interceptedFetch(target, { ...init, headers, body })
    }
    return interceptedFetch(target, init)
  }
})
afterEach(() => {
  server.resetHandlers()
  cleanup()
  localStorage.clear()
})
afterAll(() => server.close())
