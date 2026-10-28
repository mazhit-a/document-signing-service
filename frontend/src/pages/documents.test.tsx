import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it } from 'vitest'
import { storeToken } from '../api/client'
import type { DocumentDetail, DocumentStatus } from '../api/types'
import { renderApp } from '../test/render'
import { server } from '../test/server'

function makeDoc(id: string, status: DocumentStatus, signed: number, total: number): DocumentDetail {
  return {
    id,
    title: `Doc ${id}`,
    status,
    file: { name: 'a.pdf', content_type: 'application/pdf', size_bytes: 10, sha256: 'ab' },
    signers: Array.from({ length: total }, (_, i) => ({
      id: `${id}-s${i}`,
      order: i + 1,
      full_name: `S${i}`,
      email: `s${i}@x.kz`,
      status: i < signed ? 'signed' : 'waiting',
    })),
    created_at: '2026-10-28T05:12:03Z',
  }
}

beforeEach(() => storeToken('jwt'))

describe('documents list', () => {
  it('shows progress and loads more via cursor', async () => {
    const seenCursors: (string | null)[] = []
    server.use(
      http.get('*/api/v1/documents', ({ request }) => {
        const cursor = new URL(request.url).searchParams.get('cursor')
        seenCursors.push(cursor)
        if (!cursor) {
          return HttpResponse.json({
            items: [makeDoc('1', 'in_progress', 2, 3)],
            next_cursor: 'c2',
            has_more: true,
          })
        }
        return HttpResponse.json({
          items: [makeDoc('2', 'completed', 1, 1)],
          next_cursor: null,
          has_more: false,
        })
      }),
    )
    const user = userEvent.setup()
    renderApp('/documents')

    expect(await screen.findByText('Doc 1')).toBeInTheDocument()
    expect(screen.getByText(/2 \/ 3 signed/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Load more' }))
    expect(await screen.findByText('Doc 2')).toBeInTheDocument()
    expect(screen.getByText('Doc 1')).toBeInTheDocument()
    expect(seenCursors).toEqual([null, 'c2'])
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
  })

  it('filters by status and keeps it in the URL', async () => {
    const statuses: (string | null)[] = []
    server.use(
      http.get('*/api/v1/documents', ({ request }) => {
        const status = new URL(request.url).searchParams.get('status')
        statuses.push(status)
        const items = status === 'draft' ? [makeDoc('d', 'draft', 0, 2)] : [makeDoc('a', 'completed', 1, 1)]
        return HttpResponse.json({ items, next_cursor: null, has_more: false })
      }),
    )
    const user = userEvent.setup()
    const { router } = renderApp('/documents')
    expect(await screen.findByText('Doc a')).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('Status'), 'draft')

    expect(await screen.findByText('Doc d')).toBeInTheDocument()
    expect(router.state.location.search).toBe('?status=draft')
    expect(statuses).toEqual([null, 'draft'])
  })

  it('reads the filter from the URL', async () => {
    const statuses: (string | null)[] = []
    server.use(
      http.get('*/api/v1/documents', ({ request }) => {
        statuses.push(new URL(request.url).searchParams.get('status'))
        return HttpResponse.json({ items: [], next_cursor: null, has_more: false })
      }),
    )
    renderApp('/documents?status=rejected')
    await screen.findByText('No documents with this status.')
    expect(statuses).toEqual(['rejected'])
    expect(screen.getByLabelText('Status')).toHaveValue('rejected')
  })

  it('shows the empty state with a create action', async () => {
    server.use(
      http.get('*/api/v1/documents', () =>
        HttpResponse.json({ items: [], next_cursor: null, has_more: false }),
      ),
    )
    renderApp('/documents')
    const link = await screen.findByRole('link', { name: 'Create your first document' })
    expect(link).toHaveAttribute('href', '/documents/new')
  })

  it('shows skeletons while loading and an error with retry', async () => {
    let calls = 0
    server.use(
      http.get('*/api/v1/documents', () => {
        calls++
        if (calls === 1) {
          return HttpResponse.json(
            { error: { code: 'internal', message: 'boom', fields: {} } },
            { status: 500 },
          )
        }
        return HttpResponse.json({ items: [makeDoc('r', 'draft', 0, 1)], next_cursor: null, has_more: false })
      }),
    )
    const user = userEvent.setup()
    renderApp('/documents')
    expect(screen.getAllByTestId('skeleton-row').length).toBeGreaterThan(0)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('boom')
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Doc r')).toBeInTheDocument()
  })
})

describe('new document', () => {
  const pdf = () => new File(['%PDF-1.4 hello'], 'contract.pdf', { type: 'application/pdf' })

  it('shows validation errors without calling the API', async () => {
    const user = userEvent.setup()
    renderApp('/documents/new')
    await user.click(screen.getByRole('button', { name: 'Create document' }))

    expect(screen.getByText('Title is required')).toBeInTheDocument()
    expect(screen.getByText('Choose a file to upload')).toBeInTheDocument()
    expect(screen.getByText('Name is required')).toBeInTheDocument()
    expect(screen.getByText('Email is required')).toBeInTheDocument()
    const title = screen.getByLabelText('Title')
    expect(title).toHaveAttribute('aria-invalid', 'true')
    expect(title).toHaveAttribute('aria-describedby', 'doc-title-error')
  })

  it('rejects wrong file types and oversized files', async () => {
    const user = userEvent.setup({ applyAccept: false })
    renderApp('/documents/new')
    const input = screen.getByLabelText('File')

    await user.upload(input, new File(['x'], 'evil.exe', { type: 'application/x-msdownload' }))
    await user.click(screen.getByRole('button', { name: 'Create document' }))
    expect(screen.getByText('Only PDF, DOCX or TXT files are allowed')).toBeInTheDocument()

    const big = new File(['x'], 'big.pdf', { type: 'application/pdf' })
    Object.defineProperty(big, 'size', { value: 10 * 1024 * 1024 + 1 })
    await user.upload(input, big)
    await user.click(screen.getByRole('button', { name: 'Create document' }))
    expect(screen.getByText('File must be 10 MB or smaller')).toBeInTheDocument()
  })

  it('flags duplicate and invalid emails per row', async () => {
    const user = userEvent.setup()
    renderApp('/documents/new')
    await user.click(screen.getByRole('button', { name: 'Add signer' }))
    await user.click(screen.getByRole('button', { name: 'Add signer' }))
    await user.type(screen.getByLabelText('Full name (signer 1)'), 'A')
    await user.type(screen.getByLabelText('Email (signer 1)'), 'a@x.kz')
    await user.type(screen.getByLabelText('Full name (signer 2)'), 'B')
    await user.type(screen.getByLabelText('Email (signer 2)'), 'A@x.kz')
    await user.type(screen.getByLabelText('Full name (signer 3)'), 'C')
    await user.type(screen.getByLabelText('Email (signer 3)'), 'nope')
    await user.click(screen.getByRole('button', { name: 'Create document' }))

    expect(screen.getByText('Duplicate email')).toBeInTheDocument()
    expect(screen.getByText('Enter a valid email address')).toBeInTheDocument()
    expect(screen.getByLabelText('Email (signer 2)')).toHaveAttribute('aria-invalid', 'true')
  })

  it('submits reordered signers as JSON in FormData and navigates to detail', async () => {
    let body = ''
    let contentType: string | null = null
    server.use(
      http.post('*/api/v1/documents', async ({ request }) => {
        contentType = request.headers.get('content-type')
        body = await request.text()
        return HttpResponse.json(makeDoc('new-1', 'draft', 0, 2), { status: 201 })
      }),
    )
    const user = userEvent.setup()
    const { router } = renderApp('/documents/new')

    await user.type(screen.getByLabelText('Title'), 'Contract')
    await user.upload(screen.getByLabelText('File'), pdf())
    expect(screen.getByTestId('file-summary')).toHaveTextContent('contract.pdf')
    await user.click(screen.getByRole('button', { name: 'Add signer' }))
    await user.type(screen.getByLabelText('Full name (signer 1)'), 'First')
    await user.type(screen.getByLabelText('Email (signer 1)'), 'first@x.kz')
    await user.type(screen.getByLabelText('Full name (signer 2)'), 'Second')
    await user.type(screen.getByLabelText('Email (signer 2)'), 'second@x.kz')

    await user.click(screen.getByRole('button', { name: 'Move signer 2 up' }))
    expect(screen.getByLabelText('Full name (signer 1)')).toHaveValue('Second')

    await user.click(screen.getByRole('button', { name: 'Create document' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/documents/new-1'))
    expect(contentType).toMatch(/^multipart\/form-data; boundary=/)
    const field = (name: string): string | null => {
      const m = new RegExp(`name="${name}"(?:; filename="[^"]*")?\\r\\n(?:Content-Type: [^\\r]*\\r\\n)?\\r\\n([\\s\\S]*?)\\r\\n--`).exec(body)
      return m?.[1] ?? null
    }
    expect(field('title')).toBe('Contract')
    expect(body).toContain('filename="contract.pdf"')
    const raw = field('signers')
    expect(raw).not.toBeNull()
    expect(JSON.parse(raw as string)).toEqual([
      { full_name: 'Second', email: 'second@x.kz' },
      { full_name: 'First', email: 'first@x.kz' },
    ])
  })

  it('maps validation_failed fields onto the form', async () => {
    server.use(
      http.post('*/api/v1/documents', () =>
        HttpResponse.json(
          {
            error: {
              code: 'validation_failed',
              message: 'title: too long',
              fields: { title: 'too long', 'signers[0].email': 'already used' },
            },
          },
          { status: 400 },
        ),
      ),
    )
    const user = userEvent.setup()
    renderApp('/documents/new')
    await user.type(screen.getByLabelText('Title'), 'T')
    await user.upload(screen.getByLabelText('File'), pdf())
    await user.type(screen.getByLabelText('Full name (signer 1)'), 'A')
    await user.type(screen.getByLabelText('Email (signer 1)'), 'a@x.kz')
    await user.click(screen.getByRole('button', { name: 'Create document' }))

    expect(await screen.findByText('too long')).toBeInTheDocument()
    expect(screen.getByText('already used')).toBeInTheDocument()
    expect(screen.getByLabelText('Title')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByLabelText('Email (signer 1)')).toHaveAttribute('aria-invalid', 'true')
  })
})
