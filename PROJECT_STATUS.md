# WEL 职业联盟对战平台项目状态

更新时间：2026 年 8 月 6 日

## 一、项目定位

WEL 职业联盟对战平台面向 Windows 上的 WE8（Winning Eleven 8）局域网联机。玩家通过 OpenVPN TAP 房间进入同一个二层虚拟局域网，继续使用游戏原有的局域网搜索和对战机制。

```text
Windows WEL 客户端
  -> Go 对战平台 API
  -> OpenVPN TAP 房间服务
  -> WE8 局域网对战
```

## 二、服务边界

Laravel 是统一用户中心和业务管理服务，负责账号、密码、账号状态、平台使用期限、队员、战队、联盟、赛事和荣誉管理。

Go 是对战平台 API，负责调用 Laravel 校验账号密码，签发和校验平台 JWT，保存 Soccer 用户与平台用户映射，维护房间、在线状态和 IP 租约，并向 Windows 客户端下发 OpenVPN 房间地址与端口。

Windows 客户端使用 Vue 3 + TypeScript + Electron 22，负责登录、房间列表、进入和退出房间、调用本机 OpenVPN 组件连接房间，以及启动 WE8。

## 三、登录链路

```text
Windows 客户端
  -> POST Go /api/v1/auth/login
  -> POST Laravel /api/v1/auth/platform-login
  -> Laravel 校验密码、账号状态和平台使用期限
  -> Go 按 soccer_user_id 同步 platform_users 映射
  -> Go 签发 issuer=pes8-platform 的 JWT
  -> Windows 后续仅携带 Go JWT 调用平台接口
```

数据原则：

- `soccer.users` 是账号唯一真源。
- `soccer.users.platform_access_expires_at` 是对战平台权限到期时间，不影响 Soccer 前后台登录。
- `platform.platform_users` 只保存 `soccer_user_id`、用户名/昵称快照和平台状态。
- Soccer 用户修改用户名或昵称后，下次平台登录会更新快照。
- Laravel 不可用时 Go 返回 `502`，不会降级到旧平台密码登录。

## 四、平台数据库迁移

Go 启动时自动执行幂等迁移：

- 创建 `platform_schema_migrations`。
- 创建 `platform_users`。
- 保留旧 `users` 数据，但旧账号设为 `disabled` 且不再允许密码登录。
- 将 `room_ip_leases.user_id` 外键改为指向 `platform_users.id`。
- 限制房间数量为 6 个。
- 为单账号单会话增加 `active_session_id` 和租约 `session_id`。
- 将历史租约用户名字段改为通用 `vpn_username`。

## 五、OpenVPN 状态

已完成：

- 6 个房间独立 OpenVPN TAP 网段。
- Go API 下发每个房间对应的服务器地址和 UDP 端口。
- Windows 客户端使用平台 JWT 作为 OpenVPN 认证凭据。
- 客户端安装器集成 OpenVPN 运行组件并创建 `WEL TAP` 虚拟网卡。
- 客户端心跳续期，避免在线用户在 30 分钟后被回收。
- 房间成员列表、单账号单会话和被顶号下线提示。

当前线上房间端口：

```text
房间 1: UDP 12001
房间 2: UDP 12002
房间 3: UDP 12003
房间 4: UDP 12004
房间 5: UDP 12005
房间 6: UDP 12006
```

OpenVPN 不下发默认网关和 DNS，避免直播、网页和其他电脑流量走虚拟网卡。

## 六、主要接口

```text
GET  /healthz
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/me
GET  /api/v1/me/room-session
GET  /api/v1/rooms
GET  /api/v1/rooms/{roomID}
GET  /api/v1/rooms/{roomID}/members
POST /api/v1/rooms/{roomID}/join
POST /api/v1/rooms/{roomID}/heartbeat
POST /api/v1/rooms/{roomID}/leave
GET  /api/v1/rooms/{roomID}/events
```

平台不再提供注册接口。用户注册、密码修改和账号禁用统一由 Laravel/Soccer 处理。

## 七、部署原则

- 公网开放 Nginx `80/443` 和 OpenVPN 房间 UDP 端口。
- 测试阶段可临时开放 Go API 端口，正式环境由 Nginx 反向代理。
- MySQL、Redis、PHP-FPM 和 Go 内部端口不开放公网。
- 正式环境必须替换所有本地密码、JWT 密钥和 Laravel `APP_KEY`。
