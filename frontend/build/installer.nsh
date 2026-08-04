!macro customWelcomePage
  !define MUI_WELCOMEPAGE_TITLE "安装 WEL 职业联盟对战平台"
  !define MUI_WELCOMEPAGE_TEXT "本安装包将安装平台主程序、SoftEther VPN Client、虚拟网卡和必要防火墙规则。$\r$\n$\r$\n这些组件用于 WE8 虚拟局域网联机。如果你拒绝管理员授权，平台将无法进行联机。"
  !insertMacro MUI_PAGE_WELCOME
!macroend

!macro customInstall
  ${ifNot} ${isUpdated}
    MessageBox MB_ICONINFORMATION|MB_OKCANCEL \
      "接下来会安装虚拟局域网组件并写入联机所需规则。$\r$\n$\r$\n如果你取消或拒绝管理员授权，平台可以打开，但不能进入房间联机。" \
      IDOK wel_continue IDCANCEL wel_cancel

    wel_cancel:
      Abort "你已取消联机组件安装。未完成管理员授权时无法联机。"

    wel_continue:
  ${endIf}

  ClearErrors
  IfFileExists "$PROGRAMFILES64\SoftEther VPN Client\vpncmd.exe" softether_ready
  IfFileExists "$PROGRAMFILES32\SoftEther VPN Client\vpncmd.exe" softether_ready

  File /oname=$PLUGINSDIR\softether-vpnclient.exe "${BUILD_RESOURCES_DIR}\softether-vpnclient-v4.42-9798-rtm-2023.06.30-windows-x86_x64-intel.exe"
  IfErrors softether_missing 0

  DetailPrint "正在安装 SoftEther VPN Client..."
  ExecWait '"$PLUGINSDIR\softether-vpnclient.exe"' $0
  ${If} $0 != 0
    MessageBox MB_ICONEXCLAMATION|MB_OK "SoftEther VPN Client 安装返回代码 $0。平台可以打开，但在联机组件安装完成前无法进入房间。"
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
  MessageBox MB_ICONEXCLAMATION|MB_OK "安装包内未找到 SoftEther VPN Client。请重新下载完整安装包，否则无法联机。"

installer_done:
!macroend
