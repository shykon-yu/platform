# SoftEther 接入说明

## 开发模式

`docker-compose.yml` 默认设置 `SOFTETHER_MODE=mock`。进入房间会真实写入 MySQL 租约，但不会调用 VPN Server，适合 macOS 开发界面和 API。

## 阿里云测试模式

测试服务器准备好后，将 API 环境变量切换为：

```env
SOFTETHER_MODE=vpncmd
SOFTETHER_CLIENT_HOST=vpn.example.com
SOFTETHER_CLIENT_PORT=443
SOFTETHER_ADMIN_ENDPOINT=softether-private-address:5555
SOFTETHER_ADMIN_PASSWORD=strong-admin-password
SOFTETHER_VPNCMD_PATH=/usr/local/bin/vpncmd
```

API 容器需要挂载与 VPN Server 版本一致的 `vpncmd` Linux 可执行文件。管理端口只允许 API 所在的私网地址访问，不能暴露给公网。

当前一台 SoftEther Server 承载 12 个 Virtual Hub，每个房间限制 100 人，总容量 1200：

- `we8-room-01` 至 `we8-room-12`

这些 Hub 目前都指向同一个 `SOFTETHER_CLIENT_HOST`。部署前需要在 SoftEther Server 创建全部同名 Virtual Hub；以后增加服务器时再给房间增加节点归属和独立连接地址。

每个房间使用独立 `/24` 网段，只分配 `.10` 至 `.109` 共 100 个地址。第一版由客户端按后端分配结果静态配置虚拟 IP，因此 Hub 不需要给玩家提供默认网关或 DNS，也不应把玩家正常上网流量转入 VPN。

后端使用 `vpncmd` 创建随机短期密码，并设置 SoftEther 用户过期时间。后台任务每分钟回收过期的数据库租约和 VPN 用户；用户正常退出房间时立即删除两者。

## Windows 客户端联调

Tauri 客户端进入房间后会调用本机 SoftEther Client 的 `vpncmd.exe`：

1. 查找或创建名为 `VPNWE8` 的虚拟网卡。
2. 创建名为 `WEL-<平台分配用户名>` 的 VPN Client 连接。
3. 写入 API 返回的服务器地址、端口、Virtual Hub、临时用户名和密码。
4. 自动连接；退出房间时断开并删除该连接。

当前支持的默认安装路径：

```text
C:\Program Files\SoftEther VPN Client\vpncmd.exe
C:\Program Files (x86)\SoftEther VPN Client\vpncmd.exe
```

Windows 联调前请确认：

- SoftEther VPN Client 已安装并能手工连接一次；
- 虚拟网卡驱动已经安装；
- Tauri 客户端以管理员权限运行，以便创建虚拟网卡；
- API 的 `SOFTETHER_MODE` 已设为 `vpncmd`，不能继续使用 `mock`；
- API 能访问 SoftEther Server 的管理端口，管理端口不要向全网开放。

目前 SoftEther `SecureNAT` 分配的实际地址可能是 `192.168.30.x`，而数据库房间预留地址是 `10.80.x.10` 至 `10.80.x.109`。在静态地址配置功能完成前，客户端界面中的地址属于平台租约地址，不代表 Windows 当前网卡实际拿到的 DHCP 地址。WE8 联机验证以两台 Windows 的虚拟网卡实际地址和游戏内搜索结果为准。
