export type DocumentStatus =
  | 'draft'
  | 'in_progress'
  | 'completed'
  | 'rejected'
  | 'cancelled'

export type SignerStatus = 'waiting' | 'pending' | 'signed' | 'rejected'

export const DOCUMENT_STATUSES: readonly DocumentStatus[] = [
  'draft',
  'in_progress',
  'completed',
  'rejected',
  'cancelled',
]

export interface User {
  id: string
  email: string
  full_name: string
  created_at: string
}

export interface LoginResponse {
  access_token: string
  token_type: string
  expires_in: number
}

export interface RegisterRequest {
  email: string
  password: string
  full_name: string
}

export interface LoginRequest {
  email: string
  password: string
}

export interface FileInfo {
  name: string
  content_type: string
  size_bytes: number
  sha256: string
}

export interface Signer {
  id: string
  order: number
  full_name: string
  email: string
  status: SignerStatus
  signed_at?: string | null
  rejected_at?: string | null
  rejection_reason?: string | null
}

export interface DocumentDetail {
  id: string
  title: string
  status: DocumentStatus
  file: FileInfo
  signers: Signer[]
  created_at: string
}

export interface ErrorEnvelope {
  error: {
    code: string
    message: string
    fields: Record<string, string>
  }
}

export interface DocumentListResponse {
  items: DocumentDetail[]
  next_cursor: string | null
  has_more: boolean
}

export interface SignerInput {
  full_name: string
  email: string
}

export interface SigningLink {
  signer_id: string
  order: number
  url: string
  note: string
}

export interface SendResponse {
  status: DocumentStatus
  signing_links: SigningLink[]
}

export interface CancelResponse {
  id: string
  status: DocumentStatus
  updated_at: string
}

export interface AuditEvent {
  id: number
  type: string
  actor: string
  data: Record<string, unknown>
  prev_hash: string
  hash: string
  created_at: string
}

export interface AuditEventList {
  items: AuditEvent[]
}

export interface VerifySignatureCheck {
  signer_id: string
  order: number
  seal_ok: boolean
  hash_matches_document: boolean
}

export interface VerifyReport {
  valid: boolean
  checked_at: string
  file: {
    ok: boolean
    expected_sha256: string
    actual_sha256: string
  }
  signatures: VerifySignatureCheck[]
  audit_chain: {
    ok: boolean
    events: number
    first_broken_event_id: number | null
  }
}

export interface SignDocumentInfo {
  title: string
  file_name: string
  size_bytes: number
  sha256: string
  status: DocumentStatus
}

export interface SignSignerInfo {
  full_name: string
  order: number
  total: number
  status: SignerStatus
}

export interface SigningView {
  document: SignDocumentInfo
  signer: SignSignerInfo
  can_act: boolean
}

export interface DecisionResult {
  signer_status: SignerStatus
  document_status: DocumentStatus
  signed_at: string
  seal?: string
}
