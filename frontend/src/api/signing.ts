import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { request } from './client'
import type { DecisionResult, SigningView } from './types'

function signPath(token: string, suffix = ''): string {
  return `/sign/${encodeURIComponent(token)}${suffix}`
}

export function signingFileUrl(token: string): string {
  return `/api/v1${signPath(token, '/file')}`
}

export function useSigningView(token: string) {
  return useQuery({
    queryKey: ['sign', token],
    queryFn: ({ signal }) => request<SigningView>(signPath(token), { signal, auth: false }),
    retry: false,
  })
}

export function useApprove(token: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () =>
      request<DecisionResult>(signPath(token, '/approve'), { method: 'POST', auth: false }),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['sign', token] }),
  })
}

export function useReject(token: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (reason: string) =>
      request<DecisionResult>(signPath(token, '/reject'), {
        method: 'POST',
        body: { reason },
        auth: false,
      }),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['sign', token] }),
  })
}
