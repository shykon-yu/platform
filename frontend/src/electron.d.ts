export type DesktopLease = {
  host: string
  port: number
  hub: string
  username: string
  password: string
  nicName?: string
}

declare global {
  interface Window {
    we8Desktop?: {
      connectVpn: (lease: DesktopLease) => Promise<void>
      disconnectVpn: (username: string) => Promise<void>
      launchGame: (gamePath: string) => Promise<void>
    }
  }
}

export {}
