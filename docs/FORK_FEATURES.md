# Fork 新增功能说明

本文档记录 `yangbin1322/3x-ui-Manager` 相对上游 `MHSanaei/3x-ui` 新增的功能。
核心是把面板从「只能管理已有 3x-ui 节点」扩展为「通过 SSH 直接托管一批服务器
——自动装 / 导入 / 卸载 / 批量运维」,并配套一条自建的构建、发布与安装链路。

> 术语约定(见架构):**Server(服务器)** 指通过 SSH 托管的远程主机;
> **Node(节点 / 面板节点)** 指已经以 API 方式接入的 3x-ui 面板。一台被装好并
> 导入的 Server 会派生出一个关联的 Node。

---

## 1. 服务器(SSH)托管 — ManagedServer

面板「节点」页拆成两个 Tab:**Servers(服务器)** 与 **Panel Nodes(面板节点)**。

- 新增 `ManagedServer` 数据模型:保存远程主机的地址、SSH 端口、账号 +
  密码/私钥,SSH 凭据以 AES-256-GCM 加密后落库(密钥见第 6 节)。
- SSH 访问能力从原来的 Node 中拆出,独立成 ManagedServer;安装成功后自动派生
  一个关联的 Panel Node,并在服务器行上显示关联关系(**Panel Node** 列)。
- 服务器行内操作:
  - **Install 3X-UI**:在该 SSH 主机上远程安装 3x-ui(Phase 5 自动安装),
    装完就地转为 API 模式接入。
  - **Import as node(导入为节点)**:主机上已有 3x-ui 时,直接导入为面板节点
    (默认 http,不强制 https)。
  - **Uninstall 3x-ui(卸载)**:远程卸载面板。
  - 编辑 / 删除。

相关 PR:#5(ManagedServer 重构 + 两 Tab)、#6(安装管理)。

## 2. 批量添加服务器

一次性录入多台服务器,而不是逐行添加。

- **Bulk add servers(批量添加)**:粘贴 / 上传一张表格(多行:地址、端口、
  账号、密码/私钥、名称),带预览,导入前可选逐台校验 SSH 连通性。
- 兼容非 UTF-8 CSV(GBK 等自动转码),粘贴的凭据不会丢失。

相关 PR:#7。

## 3. 服务器批量运维

服务器 Tab 顶部工具栏支持对勾选的多台服务器批量操作(按钮常驻显示,不适用时
置灰):

- **Install 3X-UI**:批量远程安装(带版本选择)。
- **Import as node**:批量导入为节点。
- **Uninstall 3X-UI**:批量卸载。
- **Delete servers**:批量删除。
- 执行命令 / 执行历史:对选中服务器批量下发命令并查看历史。

配套修复:
- 同一主机(地址 / 端口 / 账号 / 密钥都相同、仅名称不同)的多台服务器共享同一个
  引用计数的 Node;导入不再触发 UNIQUE 冲突。
- 服务器添加 / 导入后立即触发一次心跳,并刷新列表。
- 卸载在实际成功时不再误报失败。

相关 PR:#6、#8。

## 4. 入站批量部署到节点

在「入站」页把一个入站的配置一次性下发到多个节点。

- **Deploy to nodes(部署到)**:选一个入站,复制其配置(相同端口 / 相同配置)
  到选中的多个节点;每个节点上的 tag 为「原 tag + 节点名」。
- 只复制配置本身,不复制客户端(下发后各节点客户端为空)。

相关 PR:#9。

## 5. 入站客户端批量操作

「入站」页新增 **Client operations(客户端操作)** 下拉,把原本只能逐行做的
客户端操作变成跨多个选中入站的批量操作:

- **Attach Existing Clients(批量附加)**:选客户端,加到所有选中入站。
- **Detach Clients(批量分离)**:从选中入站的客户端并集里选,批量分离。
- **Detach all clients(分离所有客户端)**:把选中入站的所有客户端分离
  (是分离,不是删除,客户端本身保留)。

下拉常驻显示,未勾选时置灰。

相关 PR:#10。

---

## 6. XUI_SECRET_KEY — SSH 凭据加密密钥

面板用环境变量 `XUI_SECRET_KEY` 对入库的 SSH 凭据做 AES-256-GCM 加密。缺失时
「服务器」功能会报错:`XUI_SECRET_KEY is not set; it is required to store SSH
credentials`。

- 面板启动时会自动从 systemd 的 EnvironmentFile 加载该变量:Debian/Ubuntu
  是 `/etc/default/x-ui`,Arch 是 `/etc/conf.d/x-ui`,RHEL 系是
  `/etc/sysconfig/x-ui`(与 `config.GetEnvFilePaths` 对应)。
- **一键安装(install.sh)** 会在全新安装时自动生成一把随机密钥
  (`openssl rand -hex 32`,退化到 `/dev/urandom`)写入该文件(权限 600),
  开箱即用,无需手动 export。
- **从源码部署(deploy.sh)** 则复用调用者提供的固定密钥(见下)。

> ⚠️ **密钥一旦确定不要更改。** 换密钥会导致已入库的 SSH 凭据永久无法解密。
> 因此脚本对已存在的密钥「只读不改」,只有在文件里没有密钥时才写入。

相关 PR:#15。

---

## 7. 自建构建 / 发布 / 安装链路

让这个 fork 能像官方一样「打 tag → 自动出 Release → 一键安装」,并且所有下载都
指向本 fork。

### 打 tag 自动发布 Release

`.github/workflows/release.yml`:推送 `v*.*.*` tag 后,

1. 先构建前端 `dist/`(`//go:embed all:dist` 要求编译期存在);
2. 交叉编译 7 个 Linux 架构 + Windows,二进制内嵌 dist;
3. 打包 `x-ui-linux-<arch>.tar.gz` / `x-ui-windows-amd64.zip`,上传到「tag 所在
   仓库」的 Release(推到本 fork 即发布到本 fork)。

发布一个新版本:

```bash
# 1) 改版本号
#    编辑 internal/config/version（如 3.5.1）
# 2) 合并到 main 后打 tag
git checkout main && git pull
git tag v3.5.1 && git push origin v3.5.1
# 3) Actions 跑完后把它设为正式 latest（release.yml 默认发的是 prerelease，
#    而无版本号安装走的 releases/latest 接口不返回 prerelease）
gh release edit v3.5.1 --prerelease=false --latest
```

### 一键安装(从本 fork 下载)

```bash
bash <(curl -Ls https://raw.githubusercontent.com/yangbin1322/3x-ui-Manager/main/install.sh)
```

- `install.sh` / `update.sh` / `x-ui.sh` 全部指向本 fork:下载 Release、自更新、
  指定版本安装、更新、卸载后提示的重装命令,都走 fork。
- 可用环境变量 `XUI_REPO`(默认 `yangbin1322/3x-ui-Manager`)/ `XUI_RAW_BRANCH`
  覆盖,回退到上游或其它 fork。
- 安装时会用 tarball 内自带的 systemd unit(不再多余联网),并按第 6 节自动配好
  `XUI_SECRET_KEY`。

> 这三个脚本在运行时从 fork 的 `main` 分支 raw 拉取,所以对脚本的改动一经合并到
> main **即时生效**,不必等下一个 Release。

相关 PR:#13、#14、#15。

### 从源码部署到服务器(开发用)

`deploy.sh` + `DEPLOY.md`(仓库根):在服务器上从源码构建并重启,持久化
`XUI_SECRET_KEY`。适用于开发迭代 —— 详见 `DEPLOY.md`。

```bash
# 首次部署提供一次固定密钥（写入 /etc/default/x-ui，之后无需再提供）
XUI_SECRET_KEY=<your-fixed-key> ./deploy.sh
# 之后
./deploy.sh
```

相关 PR:#11。

---

## 附:PR 一览

| PR | 功能 |
|----|------|
| #4  | SSH 节点自动安装 3x-ui 并转 API 模式(Phase 5) |
| #5  | ManagedServer 重构,节点页拆成 Servers / Panel Nodes 两 Tab |
| #6  | 服务器安装管理:导入 / 卸载 / 批量 / 版本选择 |
| #7  | 批量添加服务器(粘贴 / 上传,导入前校验) |
| #8  | 同主机共享节点、批量删除、即时心跳、操作标签优化 |
| #9  | 入站批量部署到多个节点 |
| #10 | 入站客户端批量附加 / 分离 / 分离所有 |
| #11 | deploy.sh + DEPLOY.md,源码部署持久化 XUI_SECRET_KEY |
| #12 | 服务器工具栏按钮换行,不再溢出 |
| #13 | install.sh 从本 fork 下载 Release |
| #14 | install.sh 优先使用 tarball 内自带的 systemd unit |
| #15 | 安装时自动生成 / 持久化 XUI_SECRET_KEY;update.sh / x-ui.sh 全部指向 fork |

> 打 tag 自动发布 Release 的能力由 `.github/workflows/release.yml` 提供
> (上游已有,本 fork 直接复用,推 tag 即发布到本 fork)。
