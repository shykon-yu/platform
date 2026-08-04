# Windows 安装器方案

## 目标

把 WEL 职业联盟对战平台做成一个 Windows 安装包，安装后就具备联机所需的全部前置组件，不再在“进入房间”时提示用户单独安装 SoftEther。

用户完成安装后，桌面客户端只做三件事：

1. 登录平台
2. 进入房间并自动连接虚拟局域网
3. 启动 WE8

## 安装包内集成内容

安装包需要包含并处理这些部分：

- WEL 职业联盟对战平台主程序
- Electron 22 桌面壳，用于兼容 Windows 7
- SoftEther VPN Client
- SoftEther 虚拟网卡驱动
- 必要的虚拟网卡和防火墙配置
- WE8 启动入口和路径选择能力

## 用户可见流程

### 1. 安装向导

安装时先提示用户：

```text
本安装包将为联机功能安装虚拟局域网组件。
如果你拒绝管理员授权，平台可以继续打开，但不能进行联机。
```

### 2. 管理员授权

安装器在需要以下操作时必须申请管理员权限：

- 安装 SoftEther 组件
- 创建虚拟网卡
- 写入防火墙规则
- 注册或修复联机所需服务

如果用户拒绝授权：

- 可以继续安装平台主程序
- 不能进行联机
- 进入房间时应显示“联机环境未准备好”

### 3. 安装完成后的默认状态

安装完成后，客户端启动时应检测：

- SoftEther Client 是否可用
- 是否具有管理员权限
- 虚拟网卡是否可创建

如果未完成，则给出明确提示，而不是再弹出单独的 SoftEther 安装窗口。

## 客户端运行原则

### 进入房间前

客户端先做环境检查：

- 管理员权限
- SoftEther 组件是否存在
- 虚拟网卡是否能创建
- 必要防火墙规则是否已写入

只有检查通过时，才允许进入房间。

### 进入房间时

客户端只负责：

- 向 Go API 请求房间租约
- 读取后端分配的 VPN 参数
- 调用本机 `vpncmd.exe` 建立连接

### 退出房间时

客户端负责：

- 断开 VPN
- 删除本地连接配置
- 通知 Go API 回收租约

## 当前开发状态

### 已完成

- Electron 客户端已经具备调用 `vpncmd.exe` 的基础能力
- 客户端已能读取房间租约并连接 SoftEther
- 客户端已具备桌面状态检测入口
- 前端通过 Electron preload 暴露的 `window.we8Desktop` 调用本机能力
- electron-builder NSIS 安装器已经接入 `build/installer.nsh`
- 构建时会下载 SoftEther 安装文件到 `frontend/build`，由 NSIS 安装阶段直接执行
- 安装前会明确提示管理员授权与联机限制

### 仍需完成

- 在 NSIS 安装阶段执行 SoftEther Client 安装
- 安装阶段创建 `VPN` 虚拟网卡
- 安装阶段写入仅允许 `10.80.0.0/16` ICMPv4 的防火墙规则
- 安装失败回滚
- 卸载时清理联机组件

## 后续实现建议

最稳的实现路线是：

1. 构建阶段下载 SoftEther Client 安装文件
2. NSIS 安装阶段执行 SoftEther Client 安装
3. 安装阶段创建虚拟网卡并写入防火墙规则
4. 首次启动时只做状态检测，不再弹 SoftEther 安装提示
5. 进入房间前如果组件未准备好，明确提示重新运行完整安装包

## 当前接入点

electron-builder 官方支持：

- 在 `nsis.include` 中扩展 NSIS 安装阶段

当前仓库已配置：

```text
frontend/build/installer.nsh
frontend/scripts/download-softether.cjs
```

执行 `npm run electron:build` 时会先下载 SoftEther Windows 安装文件，然后 NSIS 安装器会在安装阶段执行它。进入房间时不再安装 SoftEther，只检查安装状态并调用 `vpncmd.exe`。

## 备注

当前仓库里还保留了服务器测试阶段的 `vpncmd` 模式和 Windows 客户端自动连接代码，这两部分会在安装器集成完成后继续复用，不需要推倒重写。
