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
