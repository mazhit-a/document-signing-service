import { request } from './client'
import type { LoginRequest, LoginResponse, RegisterRequest, User } from './types'

export function login(body: LoginRequest): Promise<LoginResponse> {
  return request<LoginResponse>('/auth/login', { method: 'POST', body, auth: false })
}

export function register(body: RegisterRequest): Promise<User> {
  return request<User>('/auth/register', { method: 'POST', body, auth: false })
}
