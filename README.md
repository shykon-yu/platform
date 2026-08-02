# PES8 对战平台

面向 Windows 的实况足球 8 虚拟局域网对战平台。当前仓库包含 Go API、MySQL/Redis 开发环境，以及 Vue 3 + Tauri 客户端骨架。

## 当前阶段

- 账号注册和登录
- 固定对战房间
- 房间容量统计
- 进入房间后事务化分配虚拟 IP
- 退出房间释放 IP
- SoftEther 连接凭据下发适配层
- Windows 游戏路径选择和启动命令

SoftEther `vpncmd` 模式已经可以真实下发临时账号；Windows Tauri 客户端会在进入房间后自动创建并连接 SoftEther Client 账号。单文件 Windows 安装器仍需在 Windows 机器上完成驱动、权限和杀毒软件联调。

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

macOS 防火墙需要允许端口 `8080`。真实 PES8 联机测试仍需连接阿里云 SoftEther Server，不能使用 macOS 浏览器预览代替。

Windows Tauri 客户端在进入房间时会调用本机 `vpncmd.exe` 自动连接虚拟网络。浏览器预览只验证 API 和界面，不会连接 VPN 或启动本机游戏。

## 环境变量

首次运行前将 `.env.example` 复制为 `.env`，并修改所有密码。线上环境必须使用独立强密码，不能使用 Compose 中的开发默认值。
