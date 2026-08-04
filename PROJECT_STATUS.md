# WEL 职业联盟对战平台项目状态

更新时间：2026 年 8 月 3 日

## 一、项目定位

WEL 职业联盟对战平台面向 Windows 上的 WE8（Winning Eleven 8）局域网联机。玩家通过 SoftEther Virtual Hub 进入同一个二层虚拟局域网，继续使用游戏原有的局域网搜索和对战机制。

```text
Windows WEL 客户端
  -> Go 对战平台 API
  -> SoftEther VPN Client
  -> SoftEther Server / Virtual Hub
  -> WE8 局域网对战
```

## 二、服务边界

### Soccer / Laravel

Laravel 是统一用户中心和业务管理服务，负责：

- 用户账号、密码和账号状态
- 队员、战队、联盟、赛事和荣誉
- 角色权限和后台管理
- 登录凭据校验

### Platform / Go

Go 是对战平台服务，负责：

- 调用 Laravel 校验账号密码
- 签发和校验对战平台自己的 JWT
- 保存 Soccer 用户与平台用户的映射
- 房间、在线状态和 IP 租约
- SoftEther 临时账号的创建、续期和删除
- 向 Windows 客户端下发 VPN 连接参数

Go 不保存用户密码，也不使用 Laravel JWT 访问平台接口。

### Windows 客户端

- Vue 3 + TypeScript + Electron 22
- Electron 22 是客户端主线，用于兼容 Windows 7
- 登录、房间列表、进入和退出房间
- 调用本机 SoftEther `vpncmd.exe`
- 连接 VPN 后启动 WE8

## 三、登录链路

```text
Windows 客户端
  -> POST Go /api/v1/auth/login
  -> POST Laravel /api/v1/auth/login
  -> Laravel 查询 Soccer users 表并校验密码、状态
  -> Go 按 soccer_user_id 同步 platform_users 映射
  -> Go 签发 issuer=pes8-platform 的 24 小时 JWT
  -> Windows 后续仅携带 Go JWT 调用平台接口
```

数据原则：

- `soccer.users` 是账号唯一真源。
- `platform.platform_users` 只保存 `soccer_user_id`、用户名/昵称快照和平台状态。
- 两个服务各自维护数据库，不跨库建立外键。
- Soccer 用户修改用户名或昵称后，下次平台登录会更新快照。
- Laravel 不可用时 Go 返回 `502`，不会降级到旧平台密码登录。

登录错误映射：

| 场景 | Go HTTP 状态 |
| --- | ---: |
| 参数缺失 | 400 |
| 账号或密码错误 | 401 |
| Soccer 或平台账号禁用 | 403 |
| 登录过于频繁 | 429 |
| Laravel 暂时不可用 | 502 |

Laravel 登录限流按“用户名 + 请求来源 IP”计算，避免 Go 容器统一转发时不同账号互相占用限流额度。

## 四、平台数据库迁移

Go 启动时自动执行幂等迁移 `20260803_soccer_identity`：

- 创建 `platform_schema_migrations`。
- 创建 `platform_users`。
- 保留旧 `users` 数据，但旧账号设为 `disabled` 且不再允许密码登录。
- 保留已有房间租约。
- 将 `room_ip_leases.user_id` 外键改为指向 `platform_users.id`。

新环境由 `deploy/mysql/init/001_schema.sql` 直接创建新结构；已有环境不需要删除数据卷。

## 五、SoftEther 状态

已完成：

- Go `mock` 和真实 `vpncmd` 两种适配模式
- 容器内 `vpncmd` 与 `hamcore.se2` 挂载
- Go 连接宿主机 SoftEther 管理端口
- 房间临时用户名和密码生成
- 进入房间创建临时账号，退出或租约过期后删除
- 客户端心跳续期，避免在线用户在 30 分钟后被回收
- `we8-room-01` SecureNAT/DHCP 使用 `10.80.1.0/24`
- 客户端连接端口使用 TCP 992

仍需完成：

- 配置并验证其余 Virtual Hub 的独立网段
- 在两台 Windows 上验证完整的“登录 -> 进房 -> 自动 VPN -> WE8 对战 -> 退房”链路
- 在 Windows 7 与 Windows 10/11 验证 NSIS 安装器中的 SoftEther、虚拟网卡和防火墙规则

SoftEther 管理端口 `5555` 只允许 Docker 内网或宿主机访问，不应向公网开放。

## 六、主要接口

```text
GET  /healthz
POST /api/v1/auth/login
GET  /api/v1/me
GET  /api/v1/me/room-session
GET  /api/v1/rooms
GET  /api/v1/rooms/{roomID}
POST /api/v1/rooms/{roomID}/join
POST /api/v1/rooms/{roomID}/heartbeat
POST /api/v1/rooms/{roomID}/leave
GET  /api/v1/rooms/{roomID}/events
```

平台不再提供注册接口。用户注册、密码修改和账号禁用统一由 Laravel/Soccer 处理。

## 七、本地 Docker

主目录使用 `docker-compose.local.yml`，主要容器为：

```text
mysql             Soccer 与 Platform 两个独立数据库
redis             两个服务共享实例、逻辑隔离
soccer-app         Laravel PHP-FPM
soccer-nginx       Laravel API
soccer-web         Soccer Vue 前端
platform-api       Go API
platform-web       Platform Vue/Electron Web 前端
```

本地端口：

| 服务 | 地址 |
| --- | --- |
| Platform 前端 | `http://127.0.0.1:1421` |
| Platform API | `http://127.0.0.1:8082` |
| Soccer 前端 | `http://127.0.0.1:8852` |
| Soccer API | `http://127.0.0.1:8088` |
| MySQL | `127.0.0.1:3308` |
| Redis | `127.0.0.1:6381` |

关键环境变量：

```env
SOCCER_AUTH_URL=http://soccer-nginx/api/v1/auth/login
JWT_SECRET=replace-with-a-long-random-secret
SOFTETHER_MODE=vpncmd
SOFTETHER_CLIENT_HOST=www.jingzhu.top
SOFTETHER_CLIENT_PORT=992
SOFTETHER_ADMIN_ENDPOINT=host.docker.internal:5555
SOFTETHER_ADMIN_PASSWORD=replace-with-server-admin-password
```

## 八、部署原则

- 公网只开放 Nginx 的 `80/443` 和 SoftEther 客户端端口 `992`。
- 测试阶段可临时开放 Go API 端口，正式环境由 Nginx 反向代理。
- MySQL、Redis、PHP-FPM、Go 内部端口和 SoftEther `5555` 不开放公网。
- Laravel、Go、前端、MySQL、Redis 分容器运行；SoftEther 可继续作为宿主机服务。
- 正式环境必须替换所有本地密码、JWT 密钥和 Laravel `APP_KEY`。

## 九、下一步

1. 将本次 Laravel/Go 登录改造部署到测试服务器并验证真实 Soccer 账号。
2. 在 Windows 重新构建 Electron 客户端，API 地址指向测试域名。
3. 用两台 Windows 验证临时 SoftEther 账号、心跳续期和自动清理。
4. 完成 Windows 安装器中的 SoftEther Client、虚拟网卡和防火墙规则整合。
