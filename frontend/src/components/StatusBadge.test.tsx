import { render, screen } from '@testing-library/react'
import { StatusBadge } from './StatusBadge'
import type { DocumentStatus, SignerStatus } from '../api/types'

const cases: [DocumentStatus | SignerStatus, string][] = [
  ['draft', 'Draft'],
  ['in_progress', 'In progress'],
  ['completed', 'Completed'],
  ['rejected', 'Rejected'],
  ['cancelled', 'Cancelled'],
  ['waiting', 'Waiting'],
  ['pending', 'Pending'],
  ['signed', 'Signed'],
]

describe('StatusBadge', () => {
  it.each(cases)('renders text label for %s', (status, label) => {
    render(<StatusBadge status={status} />)
    expect(screen.getByText(label)).toBeInTheDocument()
  })
})
