# WireGuard 房间 07/08 服务端

这是 07/08 实验房间的服务端运行文件。两个房间共用一个 `welwg0` 接口：07 使用 `10.222.7.0/24`，08 使用 `10.222.8.0/24`。先安装 WireGuard、创建 `welwg0`，再启动 `welnpt-wireguard-controller.service`。API 服务通过以下变量调用本机控制器：

```text
WEL_WIREGUARD_LISTEN_PORT=51820
WEL_WIREGUARD_SERVER_PUBLIC_KEY=<welwg0 的公钥>
WEL_WIREGUARD_CONTROLLER_URL=http://host.docker.internal:51821
WEL_WIREGUARD_CONTROLLER_SECRET=<与 wireguard-controller.env 相同>
```

防火墙对公网只需新增 WireGuard 的 `51820/UDP`；无网卡 Hook/中继继续使用现有 `22333/UDP`。API 在 Docker 中通过 `host.docker.internal:51821` 调用控制器，因此控制器监听宿主机，但 `51821/TCP` 必须保持公网关闭，只允许 Docker bridge 网段访问。仓库 `docker-compose.yml` 已配置 `host-gateway` 映射。

服务器还必须允许 WireGuard 接口内的三层转发，否则客户端只能分别和服务器握手，07/08 玩家之间的虚拟地址不会互通。启用 IPv4 转发，并在服务器防火墙中放行 `welwg0` 到 `welwg0` 的转发：

```sh
sysctl -w net.ipv4.ip_forward=1
printf 'net.ipv4.ip_forward=1\n' > /etc/sysctl.d/99-wel-wireguard.conf
sysctl --system
iptables -A FORWARD -i welwg0 -o welwg0 -j ACCEPT
```

如果服务器使用 nftables 或云厂商安全组，请配置等价的 `welwg0 -> welwg0` 转发规则；不要只开放公网 UDP 端口而遗漏 FORWARD 链。

安装顺序：

1. 安装 `wireguard-tools`。现代内核直接使用内核模块；CentOS 7 的 `3.10` 内核没有模块时，在 `/usr/local/bin/wireguard-go` 安装官方 userspace 实现，`wg-quick` 会自动回退使用它，不需要升级线上内核。
2. 复制 `welwg0.conf.example` 为 `/etc/wireguard/welwg0.conf`，填入服务端私钥并设置 `0600`。
3. `systemctl enable --now wg-quick@welwg0`。
4. 编译并安装 `wgcontroller` 到 `/opt/wel-platform/wgcontroller`。
5. 复制环境变量示例到 `/etc/wel-platform/wireguard-controller.env`，填入随机密钥。
6. `systemctl enable --now welnpt-wireguard-controller`。
7. 给 API 进程设置上面的四个变量并重建 API 容器。
8. 在 API 容器中请求 `http://host.docker.internal:51821` 验证控制器可达；不要为公网开放 `51821/TCP`。

客户端不会把私钥上传服务器。服务端只在客户端登记时动态加入该客户端公钥和虚拟 IP，并在退出房间时删除；比赛对手 peer 由客户端识别真实 `GAME_PEER` 后按场次加入。
