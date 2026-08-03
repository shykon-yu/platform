<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { invoke } from '@tauri-apps/api/core'
import { open } from '@tauri-apps/plugin-dialog'
import { Gamepad2, LogOut, MonitorCog, Play, RefreshCw, Router, ShieldCheck, Users } from 'lucide-vue-next'
import { ApiError, authApi, clearToken, roomApi, setToken, type Lease, type Room, type User } from './api'

const user = ref<User | null>(null)
const rooms = ref<Room[]>([])
const activeLease = ref<Lease | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const notice = ref('')
const form = ref({ username: '', password: '' })
const gamePath = ref(localStorage.getItem('pes8.game-path') ?? '')
const totalOnline = computed(() => rooms.value.reduce((total, room) => total + room.members, 0))
const isTauri = () => Boolean((window as unknown as Record<string, unknown>).__TAURI_INTERNALS__)
const heartbeatIntervalMs = 5 * 60 * 1000
let heartbeatTimer: number | undefined

function stopLeaseHeartbeat() {
  if (heartbeatTimer !== undefined) window.clearInterval(heartbeatTimer)
  heartbeatTimer = undefined
}

function startLeaseHeartbeat() {
  stopLeaseHeartbeat()
  if (!activeLease.value) return
  void renewLease()
  heartbeatTimer = window.setInterval(() => { void renewLease() }, heartbeatIntervalMs)
}

async function renewLease() {
  const lease = activeLease.value
  if (!lease) return
  try {
    const result = await roomApi.heartbeat(lease.room_id)
    if (activeLease.value?.room_id === lease.room_id) activeLease.value.expires_at = result.expires_at
  } catch (error) {
    if (error instanceof ApiError && (error.status === 404 || error.status === 409)) {
      stopLeaseHeartbeat()
      if (isTauri()) {
        try { await invoke('disconnect_vpn', { username: lease.username }) } catch { /* local connection may already be gone */ }
      }
      activeLease.value = null
      await loadRooms()
      errorMessage.value = error.message
      return
    }
    errorMessage.value = `房间连接续期失败，将自动重试：${messageOf(error)}`
  }
}

async function loadRooms() {
  loading.value = true
  errorMessage.value = ''
  try { rooms.value = (await roomApi.list()).rooms } catch (error) { errorMessage.value = messageOf(error) } finally { loading.value = false }
}

async function authenticate() {
  loading.value = true
  errorMessage.value = ''
  try {
    const session = await authApi.login({ username: form.value.username, password: form.value.password })
    setToken(session.token)
    user.value = session.user
    form.value.password = ''
    activeLease.value = (await authApi.roomSession()).lease
    startLeaseHeartbeat()
    await loadRooms()
  } catch (error) { errorMessage.value = messageOf(error) } finally { loading.value = false }
}

async function restoreSession() {
  try {
    user.value = (await authApi.me()).user
    activeLease.value = (await authApi.roomSession()).lease
    startLeaseHeartbeat()
    await loadRooms()
  } catch { clearToken() }
}

async function joinRoom(room: Room) {
  loading.value = true
  errorMessage.value = ''
  let lease: Lease | null = null
  try {
    lease = (await roomApi.join(room.id)).lease
    activeLease.value = lease
    if (isTauri()) {
      await invoke('connect_vpn', {
        host: lease.server_host,
        port: lease.server_port,
        hub: lease.hub_name,
        username: lease.username,
        password: lease.password,
        nicName: 'VPNWE8',
      })
    }
    startLeaseHeartbeat()
    notice.value = `已为你分配虚拟地址 ${activeLease.value.virtual_ip}`
    await loadRooms()
  } catch (error) {
    stopLeaseHeartbeat()
    if (lease && isTauri()) {
      try { await invoke('disconnect_vpn', { username: lease.username }) } catch { /* connection setup may be incomplete */ }
    }
    if (lease) try { await roomApi.leave(room.id) } catch { /* the lease reaper will clean it up */ }
    activeLease.value = null
    errorMessage.value = messageOf(error)
  } finally { loading.value = false }
}

async function releaseActiveLease() {
  const lease = activeLease.value
  if (!lease) return
  stopLeaseHeartbeat()
  let cleanupError: unknown
  if (isTauri()) {
    try { await invoke('disconnect_vpn', { username: lease.username }) } catch (error) { cleanupError = error }
  }
  try {
    await roomApi.leave(lease.room_id)
  } catch (error) {
    if (!(error instanceof ApiError && error.status === 404)) cleanupError ??= error
  }
  activeLease.value = null
  if (cleanupError) throw cleanupError
}

async function leaveRoom() {
  if (!activeLease.value) return
  loading.value = true
  try {
    await releaseActiveLease()
    notice.value = '已退出房间'
    await loadRooms()
  } catch (error) { errorMessage.value = messageOf(error) } finally { loading.value = false }
}

function saveGamePath() { localStorage.setItem('pes8.game-path', gamePath.value); notice.value = '游戏路径已保存' }
async function chooseGame() {
  if (!(window as unknown as Record<string, unknown>).__TAURI_INTERNALS__) { notice.value = '浏览器预览中请直接输入路径'; return }
  const selected = await open({ title: '选择 WE8 游戏程序', multiple: false, filters: [{ name: 'Windows 程序', extensions: ['exe'] }] })
  if (typeof selected === 'string') { gamePath.value = selected; saveGamePath() }
}
async function launchGame() {
  errorMessage.value = ''
  if (!activeLease.value) { errorMessage.value = '请先进入一个房间并连接虚拟网络'; return }
  if (!gamePath.value.trim()) { errorMessage.value = '请先选择 WE8 游戏程序路径'; return }
  if (!(window as unknown as Record<string, unknown>).__TAURI_INTERNALS__) { notice.value = '浏览器预览不会启动本机程序，请在 Windows Tauri 客户端测试'; return }
  try { await invoke('launch_game', { path: gamePath.value }); notice.value = '游戏已启动' } catch (error) { errorMessage.value = messageOf(error) }
}
async function logout() {
  loading.value = true
  errorMessage.value = ''
  try {
    await releaseActiveLease()
  } catch {
    errorMessage.value = '房间清理未完全成功，服务器将在超时后自动回收'
  } finally {
    stopLeaseHeartbeat()
    user.value = null
    clearToken()
    rooms.value = []
    loading.value = false
  }
}
function messageOf(error: unknown) { return error instanceof Error ? error.message : '发生未知错误' }

onMounted(restoreSession)
onBeforeUnmount(stopLeaseHeartbeat)
</script>

<template>
  <main v-if="!user" class="auth-shell">
    <section class="auth-panel">
      <div class="brand-mark"><Gamepad2 :size="28" /></div>
      <p class="eyebrow">WE8 ONLINE ARENA</p>
      <h1>WEL职业联盟对战平台</h1>
      <form @submit.prevent="authenticate">
        <label>账号<input v-model.trim="form.username" autocomplete="username" placeholder="3 至 32 位账号" required /></label>
        <label>密码<input v-model="form.password" type="password" autocomplete="current-password" placeholder="至少 6 位" minlength="6" required /></label>
        <p v-if="errorMessage" class="form-error">{{ errorMessage }}</p>
        <button class="primary-button" :disabled="loading">{{ loading ? '处理中...' : '登录平台' }}</button>
      </form>
    </section>
  </main>

  <main v-else class="app-shell">
    <aside class="sidebar">
      <div class="sidebar-brand"><span class="brand-mark"><Gamepad2 :size="22" /></span><span>WE8 Arena</span></div>
      <div class="user-row"><span class="avatar">{{ user.nickname.slice(0, 1) }}</span><span><strong>{{ user.nickname }}</strong><small>@{{ user.username }}</small></span></div>
      <nav><a class="active"><Users :size="18" /> 对战房间</a><a><MonitorCog :size="18" /> 游戏设置</a></nav>
      <button class="logout" @click="logout"><LogOut :size="17" /> 退出登录</button>
    </aside>

    <section class="content">
      <header class="topbar"><div><p class="eyebrow">游戏大厅</p><h2>选择一个对战房间</h2></div><div class="online"><span></span>{{ totalOnline }} 人在线</div></header>
      <p v-if="errorMessage" class="banner error">{{ errorMessage }}</p><p v-if="notice" class="banner notice">{{ notice }}</p>

      <section v-if="activeLease" class="connection-strip">
        <div><p class="eyebrow">当前已连接</p><h3>{{ activeLease.hub_name }}</h3><span><Router :size="15" /> {{ activeLease.virtual_ip }} / {{ activeLease.subnet_cidr }}</span></div>
        <div class="connection-actions"><span class="secure"><ShieldCheck :size="17" /> 虚拟局域网已分配</span><button class="secondary-button" @click="leaveRoom" :disabled="loading">退出房间</button></div>
      </section>

      <section class="room-section"><div class="section-heading"><h3>可用房间</h3><button class="icon-button" title="刷新房间" @click="loadRooms" :disabled="loading"><RefreshCw :size="18" :class="{ spinning: loading }" /></button></div>
        <div class="room-grid"><article v-for="room in rooms" :key="room.id" class="room-card" :class="{ unavailable: room.status !== 'open' }"><div class="room-card-top"><span class="region">{{ room.region }}</span><span :class="['room-state', room.status]">{{ room.status === 'open' ? '可进入' : '维护中' }}</span></div><h3>{{ room.name }}</h3><p>{{ room.subnet_cidr }}</p><div class="room-card-footer"><span><Users :size="16" /> {{ room.members }} / {{ room.capacity }}</span><button class="join-button" :disabled="loading || room.status !== 'open' || Boolean(activeLease)" @click="joinRoom(room)">进入</button></div></article></div>
      </section>

      <section class="game-settings"><div><p class="eyebrow">本机设置</p><h3>游戏程序</h3><p>选择 WE8 的主程序。Windows 客户端会在连接房间后从此路径启动游戏。</p></div><div class="path-field"><input v-model="gamePath" placeholder="例如 C:\\WE8\\we8.exe" /><button class="secondary-button" @click="chooseGame">选择</button><button class="primary-button launch" @click="launchGame"><Play :size="17" /> 启动游戏</button></div></section>
    </section>
  </main>
</template>
