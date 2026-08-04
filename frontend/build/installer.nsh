!macro customInstall
  ClearErrors
  IfFileExists "$PROGRAMFILES64\WEL\SoftEther\vpncmd_x64.exe" custom_service
  IfFileExists "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe" official_ready

  SetOutPath "$PROGRAMFILES64\WEL\SoftEther"
  File /r "${BUILD_RESOURCES_DIR}\softether-runtime\*.*"

custom_service:
  DetailPrint "正在注册 WEL 联机服务..."
  ExecWait '"$SYSDIR\sc.exe" create SEVPNCLIENT binPath= "\"$PROGRAMFILES64\WEL\SoftEther\vpnclient_x64.exe\" /service" start= auto DisplayName= "WEL Virtual LAN Service"' $0
  ExecWait '"$SYSDIR\sc.exe" start SEVPNCLIENT' $1
  Sleep 5000
  DetailPrint "正在安装 WEL 虚拟网卡..."
  SetOutPath "$PROGRAMFILES64\WEL\SoftEther"
  ExecWait '"$PROGRAMFILES64\WEL\SoftEther\driver_installer_x64.exe" instvlan VPN' $2
  Sleep 3000
  WriteRegDWORD HKLM "Software\WEL\SoftEther" "ManagedByWel" 1
  Goto firewall_rules

official_ready:
  Goto firewall_rules

firewall_rules:
  DetailPrint "正在写入防火墙规则..."
  ExecWait 'netsh advfirewall firewall delete rule name="WEL WE8 Virtual LAN ICMPv4"' $4
  ExecWait 'netsh advfirewall firewall add rule name="WEL WE8 Virtual LAN ICMPv4" dir=in action=allow protocol=icmpv4:8,any remoteip=10.80.0.0/16 profile=any enable=yes' $5
  Goto installer_done

installer_done:
!macroend

!macro customUnInstall
  ReadRegDWORD $0 HKLM "Software\WEL\SoftEther" "ManagedByWel"
  ${If} $0 == 1
    ExecWait '"$SYSDIR\sc.exe" stop SEVPNCLIENT' $1
    ExecWait '"$SYSDIR\sc.exe" delete SEVPNCLIENT' $2
    DeleteRegKey HKLM "Software\WEL\SoftEther"
  ${EndIf}
  ExecWait 'netsh advfirewall firewall delete rule name="WEL WE8 Virtual LAN ICMPv4"' $3
!macroend
