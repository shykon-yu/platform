!macro customInstall
  ClearErrors
  IfFileExists "$PROGRAMFILES64\WEL\SoftEther\vpncmd_x64.exe" custom_ready
  IfFileExists "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe" official_ready

  SetOutPath "$PROGRAMFILES64\WEL\SoftEther"
  File "${BUILD_RESOURCES_DIR}\softether-runtime\vpnclient_x64.exe"
  File "${BUILD_RESOURCES_DIR}\softether-runtime\vpncmd_x64.exe"
  File "${BUILD_RESOURCES_DIR}\softether-runtime\hamcore.se2"

  DetailPrint "正在注册 WEL 联机服务..."
  ExecWait '"$SYSDIR\sc.exe" create SEVPNCLIENT binPath= "\"$PROGRAMFILES64\WEL\SoftEther\vpnclient_x64.exe\" /service" start= auto DisplayName= "WEL Virtual LAN Service"' $0
  ExecWait '"$SYSDIR\sc.exe" start SEVPNCLIENT' $1
  Sleep 2500
  WriteRegDWORD HKLM "Software\WEL\SoftEther" "ManagedByWel" 1
  StrCpy $2 "$PROGRAMFILES64\WEL\SoftEther\vpncmd_x64.exe"
  Goto vpncmd_found

custom_ready:
  StrCpy $2 "$PROGRAMFILES64\WEL\SoftEther\vpncmd_x64.exe"
  Goto vpncmd_found

official_ready:
  StrCpy $2 "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe"

vpncmd_found:
  DetailPrint "正在创建 SoftEther 虚拟网卡..."
  ExecWait '"$2" localhost /CLIENT /CMD NicCreate VPN' $3

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
