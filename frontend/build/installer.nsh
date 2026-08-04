!macro customInstall
  ClearErrors
  IfFileExists "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe" official_ready

  DetailPrint "正在安装 WEL 联机驱动..."
  ExecWait '"$SYSDIR\sc.exe" stop SEVPNCLIENT' $0
  ExecWait '"$SYSDIR\sc.exe" delete SEVPNCLIENT' $1
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "/oname=softether-client-setup.exe" "${BUILD_RESOURCES_DIR}\softether-vpnclient-v4.42-9798-rtm-2023.06.30-windows-x86_x64-intel.exe"
  ExecWait '"$PLUGINSDIR\softether-client-setup.exe" /install /silent /HIDESTARTCOMMAND' $2
  Sleep 5000
  WriteRegDWORD HKLM "Software\WEL\SoftEther" "ManagedOfficialClientByWel" 1
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
