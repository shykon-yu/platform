# WE8 对战平台

面向 Windows 的实况足球 8 虚拟局域网对战平台。当前仓库主要包含 Go API、MySQL/Redis 开发环境和后端部署配置。Windows 客户端主线在 `welopenvpn/` 仓库。

项目进度和后续计划见：[PROJECT_STATUS.md](./PROJECT_STATUS.md)

## 当前阶段

- Laravel/Soccer 账号校验和平台 JWT 登录
- 固定对战房间
- 房间容量统计
- 进入房间后事务化分配房间租约
- 退出房间释放 IP
- 在线租约心跳续期和失联自动回收
- OpenVPN 房间地址和端口下发
- Windows 游戏路径选择和启动命令
- Windows 实际虚拟 IP 检测、旧虚拟网卡冲突诊断

Windows Electron 客户端进入房间后连接对应 OpenVPN TAP 房间，安装器负责安装联机组件、创建 `WEL TAP` 虚拟网卡并写入虚拟局域网 Ping 防火墙规则。

## macOS 开发环境

只要求 OrbStack、Node.js 和 pnpm。Go 在 Docker 容器中运行，本机无需安装 Go。

```bash
cp .env.example .env
docker compose up -d --build
cd frontend
pnpm install
pnpm dev
```

浏览器访问 `http://localhost:1420`。API 健康检查为 `http://localhost:8080/healthz`。

停止开发服务：

```bash
docker compose down
```

不要执行 `docker compose down -v`，除非明确希望删除本地 MySQL 和 Redis 数据。

## Windows 联调

Windows 测试机和 Mac 在同一局域网时，把客户端 API 地址改为 Mac 的局域网地址：

```env
VITE_API_BASE_URL=http://192.168.x.x:8080/api/v1
VITE_WS_BASE_URL=ws://192.168.x.x:8080/api/v1
```

macOS 防火墙需要允许端口 `8080`。真实 WE8 联机测试仍需连接阿里云 OpenVPN 房间服务，不能使用 macOS 浏览器预览代替。

Windows Electron 客户端在进入房间时会调用本机 OpenVPN 自动连接虚拟网络。浏览器预览只验证 API 和界面，不会连接 VPN 或启动本机游戏。

## 环境变量

首次运行前将 `.env.example` 复制为 `.env`，并修改所有密码。线上环境必须使用独立强密码，不能使用 Compose 中的开发默认值。
