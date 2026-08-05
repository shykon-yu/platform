export type DesktopLease = {
  host: string
  port: number
  hub: string
  username: string
  password: string
  nicName?: string
  subnetCidr: string
}

export type DesktopLeaseStatus = {
  connected: boolean
  actualIp: string | null
  subnetCidr: string
  adapterName: string | null
  adapterDescription: string | null
  defaultGateways: string[]
  dnsServers: string[]
  conflictingAdapters: string[]
  warnings: string[]
  nicName?: string
}

export type DesktopStatus = {
  admin: boolean
  softetherInstalled: boolean
  vpncmdPath: string | null
  ready: boolean
  message: string
  serviceInstalled?: boolean
  serviceRunning?: boolean
  systemVersion?: string
  isWindows7?: boolean
}

declare global {
  interface Window {
    we8Desktop?: {
      connectVpn: (lease: DesktopLease) => Promise<DesktopLeaseStatus>
      restoreVpn: (lease: Pick<DesktopLease, 'username' | 'subnetCidr'>) => Promise<DesktopLeaseStatus>
      inspectVpn: (lease: Pick<DesktopLease, 'username' | 'subnetCidr'>) => Promise<DesktopLeaseStatus>
      copyVpnDiagnostics: (lease: Pick<DesktopLease, 'username' | 'subnetCidr'> & { hub: string }) => Promise<DesktopLeaseStatus>
      desktopStatus: () => Promise<DesktopStatus>
      prepareDesktop: () => Promise<DesktopStatus>
      disconnectVpn: (username: string) => Promise<void>
      chooseGame: () => Promise<string | null>
      launchGame: (gamePath: string) => Promise<void>
    }
  }
}

export {}
