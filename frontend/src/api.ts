const apiBase = import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080/api/v1'
const ACCESS_TOKEN_KEY = 'we8.access-token'
const LEGACY_ACCESS_TOKEN_KEY = 'pes8.access-token'

export type User = { id: number; username: string; nickname: string }
export type Room = { id: number; code: string; name: string; region: string; subnet_cidr: string; capacity: number; members: number; status: 'open' | 'maintenance' | 'closed' }
export type Lease = { room_id: number; virtual_ip: string; username: string; password?: string; hub_name: string; subnet_cidr: string; expires_at: string; server_host: string; server_port: number }

type SessionResponse = { token: string; user: User }
let token = localStorage.getItem(ACCESS_TOKEN_KEY) ?? localStorage.getItem(LEGACY_ACCESS_TOKEN_KEY) ?? ''

export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
    this.name = 'ApiError'
  }
}

export function setToken(value: string) {
  token = value
  localStorage.setItem(ACCESS_TOKEN_KEY, value)
  localStorage.removeItem(LEGACY_ACCESS_TOKEN_KEY)
}

export function clearToken() {
  token = ''
  localStorage.removeItem(ACCESS_TOKEN_KEY)
  localStorage.removeItem(LEGACY_ACCESS_TOKEN_KEY)
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  headers.set('Content-Type', 'application/json')
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const response = await fetch(`${apiBase}${path}`, { ...options, headers })
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new ApiError(body.error ?? '请求失败，请稍后重试', response.status)
  return body as T
}

export const authApi = {
  login: (payload: { username: string; password: string }) => request<SessionResponse>('/auth/login', { method: 'POST', body: JSON.stringify(payload) }),
  me: () => request<{ user: User }>('/me'),
  roomSession: () => request<{ lease: Lease | null }>('/me/room-session'),
}

export const roomApi = {
  list: () => request<{ rooms: Room[] }>('/rooms'),
  join: (roomID: number) => request<{ lease: Lease }>(`/rooms/${roomID}/join`, { method: 'POST' }),
  heartbeat: (roomID: number) => request<{ expires_at: string }>(`/rooms/${roomID}/heartbeat`, { method: 'POST' }),
  leave: (roomID: number) => request<{ ok: boolean }>(`/rooms/${roomID}/leave`, { method: 'POST' }),
}
