!macro customInstall
  ClearErrors
  IfFileExists "$PROGRAMFILES64\WEL\SoftEther\vpncmd_x64.exe" custom_ready
  IfFileExists "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe" official_ready

  SetOutPath "$PROGRAMFILES64\WEL\SoftEther"
  File "${BUILD_RESOURCES_DIR}\softether-runtime\vpnclient_x64.exe"
  File "${BUILD_RESOURCES_DIR}\softether-runtime\vpncmd_x64.exe"
  File "${BUILD_RESOURCES_DIR}\softether-runtime\hamcore.se2"

  DetailPrint "正在注册 WEL 联机服务..."
  nsExec::ExecToLog '"$SYSDIR\sc.exe" create SEVPNCLIENT binPath= "\"$PROGRAMFILES64\WEL\SoftEther\vpnclient_x64.exe\" /service" start= auto DisplayName= "WEL Virtual LAN Service"'
  Pop $0
  nsExec::ExecToLog '"$SYSDIR\sc.exe" start SEVPNCLIENT'
  Pop $1
  WriteRegDWORD HKLM "Software\WEL\SoftEther" "ManagedByWel" 1
  Goto firewall_rules

custom_ready:
  Goto firewall_rules

official_ready:
  Goto firewall_rules

firewall_rules:
  DetailPrint "正在写入防火墙规则..."
  nsExec::ExecToLog 'netsh advfirewall firewall delete rule name="WEL WE8 Virtual LAN ICMPv4"'
  Pop $4
  nsExec::ExecToLog 'netsh advfirewall firewall add rule name="WEL WE8 Virtual LAN ICMPv4" dir=in action=allow protocol=icmpv4:8,any remoteip=10.80.0.0/16 profile=any enable=yes'
  Pop $5
  Goto installer_done

installer_done:
!macroend

!macro customUnInstall
  ReadRegDWORD $0 HKLM "Software\WEL\SoftEther" "ManagedByWel"
  ${If} $0 == 1
    nsExec::ExecToLog '"$SYSDIR\sc.exe" stop SEVPNCLIENT'
    Pop $1
    nsExec::ExecToLog '"$SYSDIR\sc.exe" delete SEVPNCLIENT'
    Pop $2
    DeleteRegKey HKLM "Software\WEL\SoftEther"
  ${EndIf}
  nsExec::ExecToLog 'netsh advfirewall firewall delete rule name="WEL WE8 Virtual LAN ICMPv4"'
  Pop $3
!macroend
