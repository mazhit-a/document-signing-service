import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { readFileSync } from 'node:fs'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { SigningView } from '../api/types'
import { renderApp } from '../test/render'
import { server } from '../test/server'

const HASH = 'b'.repeat(64)

function makeView(overrides: Partial<SigningView> = {}): SigningView {
  return {
    document: {
      title: 'Lease agreement',
      file_name: 'lease.pdf',
      size_bytes: 2048,
      sha256: HASH,
      status: 'in_progress',
    },
    signer: { full_name: 'Alice', order: 2, total: 3, status: 'pending' },
    can_act: true,
    ...overrides,
  }
}

function envelope(code: string, message: string, status: number, fields: Record<string, string> = {}) {
  return HttpResponse.json({ error: { code, message, fields } }, { status })
}

describe('signing page', () => {
  it('renders document info and actions when the signer can act', async () => {
    server.use(http.get('*/api/v1/sign/tok', () => HttpResponse.json(makeView())))
    renderApp('/sign/tok')
    expect(await screen.findByRole('heading', { name: 'Lease agreement' })).toBeInTheDocument()
    expect(screen.getByText(/Signer 2 of 3/)).toBeInTheDocument()
    expect(screen.getByTestId('sha256')).toHaveTextContent(HASH)
    expect(screen.getByRole('link', { name: 'Download file' })).toHaveAttribute(
      'href',
      '/api/v1/sign/tok/file',
    )
    expect(screen.getByRole('button', { name: 'Approve' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Reject' })).toBeEnabled()
  })

  it('approves, disables buttons while pending and shows the confirmation', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    let approved = false
    server.use(
      http.get('*/api/v1/sign/tok', () =>
        HttpResponse.json(
          approved ? makeView({ can_act: false, signer: { full_name: 'Alice', order: 2, total: 3, status: 'signed' } }) : makeView(),
        ),
      ),
      http.post('*/api/v1/sign/tok/approve', async () => {
        await gate
        approved = true
        return HttpResponse.json({
          signer_status: 'signed',
          document_status: 'in_progress',
          signed_at: '2026-10-28T10:00:00Z',
          seal: 'x',
        })
      }),
    )
    renderApp('/sign/tok')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Approve' }))
    expect(await screen.findByRole('button', { name: 'Approving...' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Reject' })).toBeDisabled()
    release()
    expect(await screen.findByText('Thank you, you approved this document')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
  })

  it('shows the waiting state when approve returns 409 not_your_turn', async () => {
    server.use(
      http.get('*/api/v1/sign/tok', () => HttpResponse.json(makeView())),
      http.post('*/api/v1/sign/tok/approve', () => envelope('not_your_turn', 'Not your turn', 409)),
    )
    renderApp('/sign/tok')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Approve' }))
    expect(await screen.findByText('Waiting for earlier signers')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
  })

  it('shows the waiting state when the view says it is not the signer turn', async () => {
    server.use(
      http.get('*/api/v1/sign/tok', () =>
        HttpResponse.json(
          makeView({ can_act: false, signer: { full_name: 'Alice', order: 2, total: 3, status: 'waiting' } }),
        ),
      ),
    )
    renderApp('/sign/tok')
    expect(await screen.findByText('Waiting for earlier signers')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reject' })).not.toBeInTheDocument()
  })

  it('shows already signed and closed states', async () => {
    server.use(
      http.get('*/api/v1/sign/signed', () =>
        HttpResponse.json(
          makeView({ can_act: false, signer: { full_name: 'Alice', order: 1, total: 3, status: 'signed' } }),
        ),
      ),
      http.get('*/api/v1/sign/closed', () =>
        HttpResponse.json(
          makeView({
            can_act: false,
            document: { ...makeView().document, status: 'rejected' },
            signer: { full_name: 'Alice', order: 3, total: 3, status: 'waiting' },
          }),
        ),
      ),
    )
    const first = renderApp('/sign/signed')
    expect(await screen.findByText('You have already signed this document')).toBeInTheDocument()
    first.unmount()
    renderApp('/sign/closed')
    expect(await screen.findByText('This document is closed')).toBeInTheDocument()
  })

  it('shows the invalid link page on 404 without retrying', async () => {
    let calls = 0
    server.use(
      http.get('*/api/v1/sign/bad', () => {
        calls++
        return envelope('not_found', 'Not found', 404)
      }),
    )
    renderApp('/sign/bad')
    expect(await screen.findByRole('heading', { name: 'Invalid signing link' })).toBeInTheDocument()
    expect(calls).toBe(1)
  })

  it('requires a reason before rejecting', async () => {
    let body: unknown = null
    let rejected = false
    server.use(
      http.get('*/api/v1/sign/tok', () =>
        HttpResponse.json(
          rejected
            ? makeView({ can_act: false, signer: { full_name: 'Alice', order: 2, total: 3, status: 'rejected' } })
            : makeView(),
        ),
      ),
      http.post('*/api/v1/sign/tok/reject', async ({ request }) => {
        body = await request.json()
        rejected = true
        return HttpResponse.json({
          signer_status: 'rejected',
          document_status: 'rejected',
          signed_at: '2026-10-28T10:00:00Z',
        })
      }),
    )
    renderApp('/sign/tok')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Reject' }))
    await user.click(screen.getByRole('button', { name: 'Confirm rejection' }))
    expect(await screen.findByText('Reason is required')).toBeInTheDocument()
    expect(body).toBeNull()

    await user.type(screen.getByLabelText('Reason for rejection'), 'Wrong amount')
    await user.click(screen.getByRole('button', { name: 'Confirm rejection' }))
    await waitFor(() => expect(body).toEqual({ reason: 'Wrong amount' }))
    expect(await screen.findByText('You rejected this document')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('uses mobile-first layout classes without fixed widths', () => {
    const src = readFileSync('src/pages/SigningPage.tsx', 'utf8')
    expect(src).toContain('w-full max-w-md')
    expect(src).toContain('break-all')
    expect(src).not.toMatch(/\b(?:min-)?w-\[\d+px\]/)
    expect(src).not.toMatch(/\bw-(?:96|80|screen)\b/)
  })
})
