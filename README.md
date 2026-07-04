# Telegram Mail Bot

一个 Telegram 机器人：把多个邮箱账号（Gmail、Outlook、QQ、163 等）收到的新邮件转发到 Telegram，也可以反过来用这些账号发信。用 Go 编写，编译为单一二进制文件，无需额外运行时，数据库用 SQLite（纯 Go 驱动，交叉编译不依赖 CGO）。

## 功能

- 通过 Telegram 命令交互式添加邮箱账号（`/addaccount`），无需改配置文件
- IMAP IDLE 实时监听新邮件，断线自动重连（指数退避），服务器不支持 IDLE 时自动降级为轮询
- 可选 POP3 协议（`/addaccount pop3`），适合不支持 IMAP 或只想用 POP3 的邮箱
- 邮件正文转换为 Telegram 安全 HTML 子集显示（保留 `<b>`、`<i>`、链接等格式），转换失败自动降级为纯文本，不会丢通知
- 支持 Gmail / Outlook 的 OAuth2 登录（Device Code Flow），也支持传统密码/应用专用密码/授权码登录
- `/send` 命令用已添加账号的身份发信（SMTP，支持 465 隐式 TLS 和 587 STARTTLS）
- 白名单限制可用 Telegram 用户；邮箱密码、授权码、OAuth token 均用 AES-256-GCM 加密后存入 SQLite，不落明文

## 快速开始

### 1. 准备 Telegram Bot Token

找 [@BotFather](https://t.me/BotFather) 创建一个机器人，拿到 Token。

### 2. 配置环境变量

有两种方式：

**方式一：首次运行自动引导**（推荐）

直接运行编译好的 `mailbot`，如果检测到必填的环境变量缺失且当前在交互式终端中，会依次询问每一项配置，非必填项直接回车即可跳过，填写完成后自动保存到工作目录下的 `.env` 文件。

**方式二：手动编辑 .env**

复制 `.env.example` 为 `.env`，填入必填项：

```bash
cp .env.example .env
```

必填的三项：

| 变量 | 说明 |
|---|---|
| `TELEGRAM_BOT_TOKEN` | BotFather 给的 Token |
| `MASTER_KEY` | 加密数据库中敏感信息的主密钥，建议用 `openssl rand -hex 32` 生成一个随机值。**确定后不要更换**，否则已加密的旧数据无法解密 |
| `ALLOWED_TELEGRAM_USERS` | 允许使用机器人的 Telegram 用户 ID，逗号分隔（用 [@userinfobot](https://t.me/userinfobot) 查看自己的 ID） |

可选项见 `.env.example` 里的注释（数据库路径、Gmail/Outlook OAuth 客户端凭证）。

无交互终端环境（如容器后台运行、CI）下不会触发引导，需要提前通过 `.env` 文件或系统环境变量配置好。

### 3. 编译

```bash
go build -o mailbot ./cmd/mailbot        # 当前平台
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o mailbot.exe ./cmd/mailbot   # 交叉编译到 Windows
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o mailbot     ./cmd/mailbot   # 交叉编译到 Linux
```

### 4. 运行

```bash
./mailbot
```

启动时会自动读取工作目录下的 `.env`（如果存在）；生产部署也可以直接用系统环境变量或容器编排工具注入配置，不依赖 `.env` 文件。真实环境变量的优先级高于 `.env` 里的同名项。

### 5. 重新配置

```bash
./mailbot config
```

交互式重新填写全部配置项：已有值会展示出来，直接回车保留，输入新内容则覆盖，完成后整体重写 `.env`。

### 6. 自动更新

```bash
./mailbot update
```

检查 GitHub 上的最新 Release，如果比当前版本新，会下载对应平台的压缩包、校验 SHA256 后自动替换掉当前正在运行的二进制文件。更新完成后需要手动重启程序才会生效。

只有通过 Release 页面下载的正式发布版本才带有版本号，可以使用该命令更新；自行编译的开发版本没有版本号，运行 `update` 会直接报错，请手动下载新版本替换。

## Telegram 命令

| 命令 | 说明 |
|---|---|
| `/start`、`/help` | 显示帮助 |
| `/addaccount` | 添加一个邮箱账号，不带参数时先弹出协议选择按钮（IMAP/POP3），机器人会依次询问邮箱、服务器信息、密码/授权码等 |
| `/addaccount pop3` | 直接用 POP3 协议添加账号，跳过协议选择按钮（无实时推送，定时轮询） |
| `/listaccounts` | 列出已添加的账号及状态，每个账号带一个删除按钮 |
| `/delaccount` | 列出已添加的账号，每个账号带一个删除按钮，点击即可删除 |
| `/delaccount <id>` | 直接删除指定 id 的账号（id 从 `/listaccounts` 或 `/delaccount` 获取） |
| `/status` | 查看每个账号的基础信息（协议、认证方式、启用状态、监听是否运行中）和同步进度 |
| `/send` | 用已配置发信（SMTP）的账号发一封邮件，账号、确认等步骤用按钮操作 |
| `/cancel` | 取消当前正在进行的多轮问答 |

添加账号时，常见邮箱域名（gmail.com、outlook.com、hotmail.com、qq.com、163.com、126.com）会自动填好服务器地址和端口，只需要输入密码或授权码；其他邮箱需要手动输入 IMAP/POP3 服务器地址。多轮问答中的选择步骤（认证方式、是否配置发信、最终确认等）都提供按钮，也可以直接输入文字回复。

### QQ / 163 / 126 邮箱

需要在邮箱设置里开启 IMAP/POP3/SMTP 服务并生成授权码，添加账号时输入授权码，不是登录密码。

### Gmail / Outlook

- 密码方式：需要开启两步验证并生成"应用专用密码"（App Password），登录密码本身无法用于 IMAP/SMTP。
- OAuth 方式：需要先在 `.env` 里配置好对应的 `*_OAUTH_CLIENT_ID`/`*_OAUTH_CLIENT_SECRET`（分别在 [Google Cloud Console](https://console.cloud.google.com/) / [Azure Portal](https://portal.azure.com/) 创建 OAuth 客户端），配置好后 `/addaccount` 遇到 Gmail/Outlook 邮箱会多问一步"密码还是 OAuth"，选 OAuth 后机器人会发一个授权链接和一次性代码，在浏览器里完成授权即可，无需手动处理密码。

## 项目结构

```
cmd/mailbot/          程序入口
internal/
  config/             环境变量 / .env 加载
  crypto/             AES-256-GCM 加解密
  db/                 SQLite schema、迁移、accounts/mail_state/pop3_seen_uids 表的读写
  mail/               IMAP IDLE 监听、POP3 轮询、邮件摘要生成（含 HTML 转 Telegram 安全子集）
  manager/            账号级监听 goroutine 的生命周期管理
  oauth/              Gmail/Outlook OAuth2 Device Code Flow、token 刷新
  smtp/                发信（含 XOAUTH2）
  telegram/           命令分发、白名单校验、多轮问答状态机
```

## 测试

```bash
go test ./...
```

单元测试覆盖加解密、数据库迁移与 CRUD、HTML 安全转换、SMTP 消息构造、OAuth token 刷新逻辑、Telegram 多轮问答状态机等。IMAP/POP3 实际连接和端到端收发邮件需要真实邮箱账号手动验证。

## 安全说明

- 机器人只响应 `ALLOWED_TELEGRAM_USERS` 里的用户，其他人发消息会被直接忽略
- 密码、授权码、OAuth access/refresh token 均用 `MASTER_KEY` 派生的密钥加密后存入 SQLite
- 这是一个长期运行的后台服务（Telegram 长轮询 + 常驻 IMAP 连接），请妥善保管运行环境和 `MASTER_KEY`
