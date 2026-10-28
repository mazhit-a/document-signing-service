import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query'
import { ApiError, getStoredToken, request } from './client'
import type {
  AuditEventList,
  CancelResponse,
  DocumentDetail,
  DocumentListResponse,
  DocumentStatus,
  SendResponse,
  SignerInput,
  VerifyReport,
} from './types'

export const PAGE_SIZE = 20

export function listDocuments(
  status: DocumentStatus | null,
  cursor: string | null,
  signal?: AbortSignal,
): Promise<DocumentListResponse> {
  const params = new URLSearchParams({ limit: String(PAGE_SIZE) })
  if (status) params.set('status', status)
  if (cursor) params.set('cursor', cursor)
  return request<DocumentListResponse>(`/documents?${params.toString()}`, { signal })
}

export function useDocumentsList(status: DocumentStatus | null) {
  return useInfiniteQuery<
    DocumentListResponse,
    Error,
    InfiniteData<DocumentListResponse, string | null>,
    unknown[],
    string | null
  >({
    queryKey: ['documents', { status }],
    queryFn: ({ pageParam, signal }) => listDocuments(status, pageParam, signal),
    initialPageParam: null,
    getNextPageParam: (last) => (last.has_more ? last.next_cursor : undefined),
  })
}

export interface CreateDocumentInput {
  title: string
  file: File
  signers: SignerInput[]
}

export function createDocument(input: CreateDocumentInput): Promise<DocumentDetail> {
  const form = new FormData()
  form.append('title', input.title)
  form.append('file', input.file)
  form.append('signers', JSON.stringify(input.signers))
  return request<DocumentDetail>('/documents', { method: 'POST', body: form })
}

export function useCreateDocument() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: createDocument,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['documents'] }),
  })
}

export const POLL_INTERVAL_MS = 5000

export function pollInterval(
  status: DocumentStatus | undefined,
  visibility: DocumentVisibilityState,
): number | false {
  return status === 'in_progress' && visibility === 'visible' ? POLL_INTERVAL_MS : false
}

export function useDocument(id: string) {
  return useQuery({
    queryKey: ['document', id],
    queryFn: ({ signal }) => request<DocumentDetail>(`/documents/${id}`, { signal }),
    refetchInterval: (query) =>
      pollInterval(query.state.data?.status, document.visibilityState),
    refetchIntervalInBackground: false,
  })
}

export function useDocumentEvents(id: string) {
  return useQuery({
    queryKey: ['document', id, 'events'],
    queryFn: ({ signal }) => request<AuditEventList>(`/documents/${id}/events`, { signal }),
  })
}

export function useVerifyDocument(id: string) {
  return useQuery({
    queryKey: ['document', id, 'verify'],
    queryFn: ({ signal }) => request<VerifyReport>(`/documents/${id}/verify`, { signal }),
    enabled: false,
    gcTime: 0,
    staleTime: 0,
  })
}

function useInvalidateDocument(id: string) {
  const queryClient = useQueryClient()
  return async () => {
    queryClient.removeQueries({ queryKey: ['document', id, 'verify'] })
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['document', id], exact: true }),
      queryClient.invalidateQueries({ queryKey: ['document', id, 'events'] }),
      queryClient.invalidateQueries({ queryKey: ['documents'] }),
    ])
  }
}

export function useSendDocument(id: string) {
  const invalidate = useInvalidateDocument(id)
  return useMutation({
    mutationFn: () => request<SendResponse>(`/documents/${id}/send`, { method: 'POST' }),
    onSuccess: invalidate,
  })
}

export function useCancelDocument(id: string) {
  const invalidate = useInvalidateDocument(id)
  return useMutation({
    mutationFn: () => request<CancelResponse>(`/documents/${id}/cancel`, { method: 'POST' }),
    onSuccess: invalidate,
  })
}

export async function downloadDocumentFile(id: string, fileName: string): Promise<void> {
  const token = getStoredToken()
  let res: Response
  try {
    res = await fetch(`/api/v1/documents/${id}/file`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
  } catch {
    throw new ApiError(0, 'network_error', 'Cannot reach the server')
  }
  if (!res.ok) throw new ApiError(res.status, 'download_failed', 'Download failed')
  const url = URL.createObjectURL(await res.blob())
  const link = document.createElement('a')
  link.href = url
  link.download = fileName
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}
