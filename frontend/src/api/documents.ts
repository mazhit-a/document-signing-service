import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
  type InfiniteData,
} from '@tanstack/react-query'
import { request } from './client'
import type {
  DocumentDetail,
  DocumentListResponse,
  DocumentStatus,
  SignerInput,
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
