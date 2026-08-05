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

当前一台 SoftEther Server 承载 6 个 Virtual Hub，每个房间限制 100 人，总容量 600：

- `we8-room-01` 至 `we8-room-06`

这些 Hub 目前都指向同一个 `SOFTETHER_CLIENT_HOST`。部署前需要在 SoftEther Server 创建全部同名 Virtual Hub；以后增加服务器时再给房间增加节点归属和独立连接地址。

每个房间使用独立 `/24` 网段，只分配 `.10` 至 `.109` 共 100 个地址。SecureNAT DHCP 只下发房间 IP 和 `255.255.255.0` 子网掩码，默认网关、DNS 和推送路由必须保持为空。这样只有同房间 `10.80.x.0/24` 流量进入 VPN，直播和普通上网继续使用玩家本地宽带。

后端使用 `vpncmd` 创建随机短期密码，并设置 SoftEther 用户过期时间。后台任务每分钟回收过期的数据库租约和 VPN 用户；用户正常退出房间时立即删除两者。

## Windows 客户端联调

Electron 客户端进入房间后会调用本机 SoftEther Client 的 `vpncmd.exe`：

1. 查找已有 SoftEther 虚拟网卡；没有时创建 `VPN`，如被占用则依次尝试 `VPN2` 到 `VPN127`。
2. 创建名为 `WEL-<平台分配用户名>` 的 VPN Client 连接。
3. 写入 API 返回的服务器地址、端口、Virtual Hub、临时用户名和密码。
4. 自动连接；退出房间时断开并删除该连接。

当前支持的默认安装路径：

```text
C:\Program Files\SoftEther VPN Client\vpncmd.exe
C:\Program Files (x86)\SoftEther VPN Client\vpncmd.exe
```

Windows 联调前请确认：

- 使用完整 WEL 安装包安装，并同意管理员授权；
- 安装阶段会安装 SoftEther VPN Client、创建 `VPN` 虚拟网卡并写入 Ping 防火墙规则；
- API 的 `SOFTETHER_MODE` 已设为 `vpncmd`，不能继续使用 `mock`；
- API 能访问 SoftEther Server 的管理端口，管理端口不要向全网开放。

Go 数据库中的 `virtual_ip` 当前用于租约容量和地址预留，不与 SecureNAT DHCP 租约做静态绑定。Electron 客户端连接后会等待 Windows 在房间网段内取得真实 IPv4，并以该地址作为页面显示和启动 WE8 的前置条件。

客户端同时通过 Windows WMI 检查实际 VPN 网卡的默认网关、DNS 和其他已启用虚拟网卡。检测失败不修改用户网卡；诊断结果可以从房间状态栏复制，用于定位旧 TAP 网卡冲突和未刷新 DHCP 配置。
