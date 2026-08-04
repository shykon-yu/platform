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
      ensureSoftEther: () => Promise<boolean>
      disconnectVpn: (username: string) => Promise<void>
      chooseGame: () => Promise<string | null>
      launchGame: (gamePath: string) => Promise<void>
    }
  }
}

export {}
