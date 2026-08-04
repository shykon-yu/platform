export type DesktopLease = {
  host: string
  port: number
  hub: string
  username: string
  password: string
  nicName?: string
}

export type DesktopStatus = {
  admin: boolean
  softetherInstalled: boolean
  vpncmdPath: string | null
  ready: boolean
  message: string
}

declare global {
  interface Window {
    we8Desktop?: {
      connectVpn: (lease: DesktopLease) => Promise<void>
      desktopStatus: () => Promise<DesktopStatus>
      prepareDesktop: () => Promise<DesktopStatus>
      disconnectVpn: (username: string) => Promise<void>
      chooseGame: () => Promise<string | null>
      launchGame: (gamePath: string) => Promise<void>
    }
  }
}

export {}
