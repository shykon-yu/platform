!macro customInstall
  ClearErrors
  IfFileExists "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe" softether_ready
  IfFileExists "$PROGRAMFILES32\SoftEther VPN Client\vpncmd.exe" softether_ready

  File /oname=$PLUGINSDIR\softether-vpnclient.exe "${BUILD_RESOURCES_DIR}\softether-vpnclient-v4.42-9798-rtm-2023.06.30-windows-x86_x64-intel.exe"
  IfErrors softether_missing 0

  DetailPrint "正在安装联机组件..."
  ; SoftEther's bootstrapper has no generic /S switch. Start it hidden so
  ; the WEL installer remains the only visible installer window.
  ExecShellWait "open" "$PLUGINSDIR\softether-vpnclient.exe" "/UAC:yes /HIDESTARTCOMMAND:yes" SW_HIDE
  ${If} $0 != 0
    Abort "联机组件安装失败（SoftEther 返回代码 $0）。请以管理员身份重新运行 WEL 安装包。"
  ${EndIf}

softether_ready:
  IfFileExists "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe" 0 try_x86_vpncmd
  StrCpy $1 "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe"
  Goto vpncmd_found

try_x86_vpncmd:
  IfFileExists "$PROGRAMFILES32\SoftEther VPN Client\vpncmd.exe" 0 softether_config_done
  StrCpy $1 "$PROGRAMFILES32\SoftEther VPN Client\vpncmd.exe"

vpncmd_found:
  DetailPrint "正在创建 SoftEther 虚拟网卡..."
  ExecWait '"$1" localhost /CLIENT /CMD NicCreate VPN' $2

softether_config_done:
  DetailPrint "正在写入防火墙规则..."
  ExecWait 'netsh advfirewall firewall delete rule name="WEL WE8 Virtual LAN ICMPv4"' $3
  ExecWait 'netsh advfirewall firewall add rule name="WEL WE8 Virtual LAN ICMPv4" dir=in action=allow protocol=icmpv4:8,any remoteip=10.80.0.0/16 profile=any enable=yes' $4
  Goto installer_done

softether_missing:
  Abort "安装包缺少联机组件。请重新下载完整 WEL 安装包。"

installer_done:
!macroend
