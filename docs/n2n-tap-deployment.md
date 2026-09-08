# TAP 房间服务器部署

网卡房间 01/02 使用客户端 TAP-Windows 和 n2n edge。服务器端只需要运行一个 n2n `supernode`；客户端不连接 OpenVPN，也不需要服务器创建 TAP 网卡。

## 端口

- `22222/udp`：n2n supernode，网卡房间 01/02 必须开放。
- `22333/udp`：现有无网卡游戏中继，保持开放，不要改动。
- `3478/udp`、`3478/tcp`：现有 STUN/TURN，按当前业务保持开放。
- `80/tcp`、`443/tcp`：现有网站/API 入口，保持现状。

网卡房间不需要开放 `12001-12006`。这些端口属于旧 OpenVPN/TAP 方案，只有旧客户端仍在使用时才保留；不要为了新 TAP 房间把它们映射到 n2n。

## n2n supernode

使用与 Windows 客户端 edge 相同的 n2n 3.0 源码构建 Linux `supernode`，安装到 `/usr/local/bin/supernode`，并创建 systemd 服务。supernode 只负责节点发现和转发控制，不创建虚拟网卡。

示例环境变量：

```env
N2N_PORT=22222
N2N_CLIENT_HOST=8.155.145.132
N2N_CLIENT_PORT=22222
N2N_ROOM_PORTS=1:22222,2:22222
```

后端容器需要同时设置这些变量。`N2N_CLIENT_HOST` 必须是玩家能访问的公网地址；不能填 Docker 内网地址。`N2N_ROOM_PORTS` 只影响网卡房间租约返回值。

## 防火墙

CentOS 7 使用 firewalld 时：

```bash
firewall-cmd --permanent --zone=public --add-port=22222/udp
firewall-cmd --reload
ss -lunp | grep 22222
systemctl status weln2n-supernode
```

云厂商安全组也要放行 `22222/UDP`。只开放服务器端口还不够，客户端所在网络必须允许出站 UDP。

## 与旧 OpenVPN 的关系

新 TAP 房间不依赖 OpenVPN。若旧客户端/旧房间仍在线，保留旧 OpenVPN 服务及其端口；若已经彻底下线旧方案，可以另行清理，但不能作为本次 n2n 部署步骤的一部分删除。
