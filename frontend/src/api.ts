const apiBase = import.meta.env.VITE_API_BASE_URL ?? 'http://localhost:8080/api/v1'

export type User = { id: number; username: string; nickname: string }
export type Room = { id: number; code: string; name: string; region: string; subnet_cidr: string; capacity: number; members: number; status: 'open' | 'maintenance' | 'closed' }
export type Lease = { room_id: number; virtual_ip: string; username: string; password?: string; hub_name: string; subnet_cidr: string; expires_at: string; server_host: string; server_port: number }

type SessionResponse = { token: string; user: User }
let token = localStorage.getItem('pes8.access-token') ?? ''

export function setToken(value: string) {
  token = value
  localStorage.setItem('pes8.access-token', value)
}

export function clearToken() {
  token = ''
  localStorage.removeItem('pes8.access-token')
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  headers.set('Content-Type', 'application/json')
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const response = await fetch(`${apiBase}${path}`, { ...options, headers })
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(body.error ?? '请求失败，请稍后重试')
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
  leave: (roomID: number) => request<{ ok: boolean }>(`/rooms/${roomID}/leave`, { method: 'POST' }),
}
