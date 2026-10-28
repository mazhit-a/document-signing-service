import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { pollInterval } from '../api/documents'
import { storeToken } from '../api/client'
import type {
  DocumentDetail,
  DocumentStatus,
  Signer,
  SignerStatus,
  VerifyReport,
} from '../api/types'
import { SignerTimeline } from '../components/SignerTimeline'
import { renderApp } from '../test/render'
import { server } from '../test/server'

const HASH = 'a'.repeat(64)

function makeDoc(status: DocumentStatus, signers?: Signer[]): DocumentDetail {
  return {
    id: 'd1',
    title: 'Contract',
    status,
    file: { name: 'contract.pdf', content_type: 'application/pdf', size_bytes: 2048, sha256: HASH },
    signers: signers ?? [
      { id: 's1', order: 1, full_name: 'Alice', email: 'alice@x.kz', status: 'pending' },
      { id: 's2', order: 2, full_name: 'Bob', email: 'bob@x.kz', status: 'waiting' },
    ],
    created_at: '2026-10-28T05:12:03Z',
  }
}

function mockDoc(getDoc: () => DocumentDetail) {
  server.use(
    http.get('*/api/v1/documents/d1', () => HttpResponse.json(getDoc())),
    http.get('*/api/v1/documents/d1/events', () =>
      HttpResponse.json({
        items: [
          {
            id: 1,
            type: 'document.created',
            actor: 'owner@x.kz',
            data: { title: 'Contract' },
            prev_hash: '',
            hash: 'h1',
            created_at: '2026-10-28T05:12:03Z',
          },
        ],
      }),
    ),
  )
}

const goodReport: VerifyReport = {
  valid: true,
  checked_at: '2026-10-28T06:00:00Z',
  file: { ok: true, expected_sha256: HASH, actual_sha256: HASH },
  signatures: [{ signer_id: 's1', order: 1, seal_ok: true, hash_matches_document: true }],
  audit_chain: { ok: true, events: 3, first_broken_event_id: null },
}

beforeEach(() => storeToken('jwt'))
afterEach(() => vi.useRealTimers())

describe('SignerTimeline', () => {
  it('renders every signer state with a text label', () => {
    const statuses: SignerStatus[] = ['signed', 'rejected', 'pending', 'waiting']
    const signers: Signer[] = statuses.map((status, i) => ({
      id: `s${i}`,
      order: i + 1,
      full_name: `Person ${i + 1}`,
      email: `p${i}@x.kz`,
      status,
      signed_at: status === 'signed' ? '2026-10-28T07:00:00Z' : null,
      rejected_at: status === 'rejected' ? '2026-10-28T08:00:00Z' : null,
      rejection_reason: status === 'rejected' ? 'Wrong amount' : null,
    }))
    render(<SignerTimeline signers={signers} />)

    const items = screen.getAllByRole('listitem')
    expect(items).toHaveLength(4)
    const [first, second, third, fourth] = items as [HTMLElement, HTMLElement, HTMLElement, HTMLElement]
    expect(within(first).getByText('Signed')).toBeInTheDocument()
    expect(within(second).getByText('Rejected')).toBeInTheDocument()
    expect(within(second).getByText(/Wrong amount/)).toBeInTheDocument()
    expect(within(third).getByText('Pending')).toBeInTheDocument()
    expect(within(fourth).getByText('Waiting')).toBeInTheDocument()
    expect(screen.getByText('p0@x.kz')).toBeInTheDocument()
  })
})

describe('pollInterval', () => {
  it('polls only while in_progress and the tab is visible', () => {
    expect(pollInterval('in_progress', 'visible')).toBe(5000)
    expect(pollInterval('in_progress', 'hidden')).toBe(false)
    const others: DocumentStatus[] = ['draft', 'completed', 'rejected', 'cancelled']
    for (const status of others) expect(pollInterval(status, 'visible')).toBe(false)
    expect(pollInterval(undefined, 'visible')).toBe(false)
  })
})

describe('document detail', () => {
  it('shows header, hash and signers', async () => {
    mockDoc(() => makeDoc('draft'))
    renderApp('/documents/d1')

    expect(await screen.findByRole('heading', { name: 'Contract' })).toBeInTheDocument()
    expect(screen.getByTestId('sha256')).toHaveTextContent(HASH)
    expect(screen.getByText(/contract\.pdf/)).toBeInTheDocument()
    expect(screen.getByText('alice@x.kz')).toBeInTheDocument()
    expect(await screen.findByText('document.created')).toBeInTheDocument()
  })

  it.each<[DocumentStatus, boolean, boolean]>([
    ['draft', true, true],
    ['in_progress', false, true],
    ['completed', false, false],
    ['rejected', false, false],
    ['cancelled', false, false],
  ])('shows actions for %s', async (status, canSend, canCancel) => {
    mockDoc(() => makeDoc(status))
    renderApp('/documents/d1')
    await screen.findByRole('heading', { name: 'Contract' })

    expect(!!screen.queryByRole('button', { name: 'Send for signing' })).toBe(canSend)
    expect(!!screen.queryByRole('button', { name: 'Cancel document' })).toBe(canCancel)
    expect(screen.getByRole('button', { name: 'Download file' })).toBeInTheDocument()
  })

  it('shows an error state with retry', async () => {
    server.use(
      http.get('*/api/v1/documents/d1', () =>
        HttpResponse.json({ error: { code: 'internal', message: 'x', fields: {} } }, { status: 500 }),
      ),
      http.get('*/api/v1/documents/d1/events', () => HttpResponse.json({ items: [] })),
    )
    renderApp('/documents/d1')
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load the document')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('send displays one-time links with a warning and closes on Escape', async () => {
    let status: DocumentStatus = 'draft'
    mockDoc(() => makeDoc(status))
    server.use(
      http.post('*/api/v1/documents/d1/send', () => {
        status = 'in_progress'
        return HttpResponse.json({
          status: 'in_progress',
          signing_links: [
            { signer_id: 's1', order: 1, url: 'http://app/sign/tok1', note: 'shown once' },
            { signer_id: 's2', order: 2, url: 'http://app/sign/tok2', note: 'shown once' },
          ],
        })
      }),
    )
    const user = userEvent.setup()
    renderApp('/documents/d1')
    await user.click(await screen.findByRole('button', { name: 'Send for signing' }))

    const dialog = await screen.findByRole('dialog', { name: 'Signing links' })
    expect(within(dialog).getByText('http://app/sign/tok1')).toBeInTheDocument()
    expect(within(dialog).getByText('http://app/sign/tok2')).toBeInTheDocument()
    expect(within(dialog).getByRole('alert')).toHaveTextContent(/only once/)
    expect(within(dialog).getByRole('button', { name: 'Copy link for Alice' })).toBeInTheDocument()

    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Send for signing' })).not.toBeInTheDocument(),
    )
  })

  it('traps focus inside the modal', async () => {
    mockDoc(() => makeDoc('draft'))
    server.use(
      http.post('*/api/v1/documents/d1/send', () =>
        HttpResponse.json({
          status: 'in_progress',
          signing_links: [{ signer_id: 's1', order: 1, url: 'http://app/sign/t', note: 'shown once' }],
        }),
      ),
    )
    const user = userEvent.setup()
    renderApp('/documents/d1')
    await user.click(await screen.findByRole('button', { name: 'Send for signing' }))
    const dialog = await screen.findByRole('dialog')

    for (let i = 0; i < 6; i++) {
      await user.tab()
      expect(dialog.contains(document.activeElement)).toBe(true)
    }
  })

  it('cancel asks for confirmation before calling the API', async () => {
    let cancelled = 0
    mockDoc(() => makeDoc('in_progress'))
    server.use(
      http.post('*/api/v1/documents/d1/cancel', () => {
        cancelled++
        return HttpResponse.json({ id: 'd1', status: 'cancelled', updated_at: '2026-10-28T09:00:00Z' })
      }),
    )
    const user = userEvent.setup()
    renderApp('/documents/d1')
    await user.click(await screen.findByRole('button', { name: 'Cancel document' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Keep document' }))
    expect(cancelled).toBe(0)

    await user.click(screen.getByRole('button', { name: 'Cancel document' }))
    await user.click(
      within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel document' }),
    )
    await waitFor(() => expect(cancelled).toBe(1))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('does not call verify until requested and shows the pass banner', async () => {
    let calls = 0
    mockDoc(() => makeDoc('in_progress'))
    server.use(
      http.get('*/api/v1/documents/d1/verify', () => {
        calls++
        return HttpResponse.json(goodReport)
      }),
    )
    const user = userEvent.setup()
    renderApp('/documents/d1')
    await screen.findByRole('heading', { name: 'Contract' })
    expect(calls).toBe(0)

    await user.click(screen.getByRole('button', { name: 'Verify integrity' }))
    expect(await screen.findByText(/Integrity verified/)).toBeInTheDocument()
    expect(screen.getByText(/File SHA-256 matches/)).toBeInTheDocument()
    expect(screen.getByText(/Signature seal of Alice/)).toBeInTheDocument()
    expect(screen.getByText(/Audit chain \(3 events\)/)).toBeInTheDocument()
    expect(calls).toBe(1)
  })

  it('shows the fail banner and the failing checks', async () => {
    const bad: VerifyReport = {
      ...goodReport,
      valid: false,
      file: { ok: false, expected_sha256: HASH, actual_sha256: 'b'.repeat(64) },
      audit_chain: { ok: false, events: 3, first_broken_event_id: 2 },
    }
    mockDoc(() => makeDoc('in_progress'))
    server.use(http.get('*/api/v1/documents/d1/verify', () => HttpResponse.json(bad)))
    const user = userEvent.setup()
    renderApp('/documents/d1')
    await user.click(await screen.findByRole('button', { name: 'Verify integrity' }))

    expect(await screen.findByText(/Integrity check failed/)).toBeInTheDocument()
    const failed = screen.getAllByText('failed')
    expect(failed).toHaveLength(2)
    expect(screen.getByText(/First broken audit event: #2/)).toBeInTheDocument()
  })

  it('collapses audit event data behind a details element', async () => {
    mockDoc(() => makeDoc('draft'))
    const user = userEvent.setup()
    renderApp('/documents/d1')
    const summary = await screen.findByText('Data for event #1')
    const details = summary.closest('details')
    expect(details).not.toHaveAttribute('open')
    await user.click(summary)
    expect(details).toHaveAttribute('open')
    expect(details).toHaveTextContent('"title": "Contract"')
  })
})

describe('polling', () => {
  it('polls while in_progress and stops once the status is terminal', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let status: DocumentStatus = 'in_progress'
    let fetches = 0
    server.use(
      http.get('*/api/v1/documents/d1', () => {
        fetches++
        return HttpResponse.json(makeDoc(status))
      }),
      http.get('*/api/v1/documents/d1/events', () => HttpResponse.json({ items: [] })),
    )
    renderApp('/documents/d1')
    await screen.findByRole('heading', { name: 'Contract' })
    expect(fetches).toBe(1)

    status = 'completed'
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    await waitFor(() => expect(fetches).toBe(2))
    expect(screen.getByText('Completed')).toBeInTheDocument()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(20000)
    })
    expect(fetches).toBe(2)
  })
})
