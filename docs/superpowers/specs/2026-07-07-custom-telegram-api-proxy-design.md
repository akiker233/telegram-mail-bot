# 自定义 Telegram Bot API 后端与代理支持

## 背景

部分部署环境无法直连 Telegram 官方 Bot API，或需要把流量定向到私有 Bot API 后端。因此需要支持：

1. 自定义 Telegram Bot API 基础 URL。
2. 为 Telegram Bot API 单独配置 HTTP/SOCKS5 代理。
3. 为其他出站请求（OAuth token 刷新、/update 检查 GitHub Releases）单独配置全局代理。

## 决策记录

- Telegram 代理与全局代理可分别配置。
- API 地址使用“基础 URL”形式，程序内部补齐 `/bot<token>/<method>` 路径模板。
- 三个新配置项均加入交互式配置向导和 `.env.example`。
- 实现方案采用独立 `internal/proxy` 包，负责代理客户端构造。

## 配置项

| 环境变量 | 含义 | 必填 | 示例 |
|---|---|---|---|
| `TELEGRAM_BOT_TOKEN` | Telegram Bot Token | 是 | `123456:ABC...` |
| `MASTER_KEY` | 加密主密钥 | 是 | `openssl rand -hex 32` |
| `ALLOWED_TELEGRAM_USERS` | 允许使用的 Telegram 用户 ID | 是 | `123,456` |
| `DB_PATH` | SQLite 数据库路径 | 否 | `./mailbot.db` |
| `GMAIL_OAUTH_CLIENT_ID` | Gmail OAuth Client ID | 否 |  |
| `GMAIL_OAUTH_CLIENT_SECRET` | Gmail OAuth Client Secret | 否 |  |
| `OUTLOOK_OAUTH_CLIENT_ID` | Outlook OAuth Client ID | 否 |  |
| `OUTLOOK_OAUTH_CLIENT_SECRET` | Outlook OAuth Client Secret | 否 |  |
| `TELEGRAM_API_URL` | 自定义 Bot API 基础 URL | 否 | `https://api.example.com` |
| `TELEGRAM_PROXY` | Telegram Bot API 代理 | 否 | `socks5://127.0.0.1:1080` |
| `GLOBAL_PROXY` | 全局代理 | 否 | `http://user:pass@proxy:8080` |

## 组件设计

### 1. `internal/config`

`Config` 结构体新增三个字段：

```go
type Config struct {
    TelegramBotToken string
    MasterKey        string
    AllowedUsers     map[int64]bool
    DBPath           string

    GmailOAuthClientID       string
    GmailOAuthClientSecret   string
    OutlookOAuthClientID     string
    OutlookOAuthClientSecret string

    TelegramAPIURL string // 自定义 Telegram Bot API 基础 URL
    TelegramProxy  string // Telegram Bot API 专用代理
    GlobalProxy    string // 全局代理（OAuth、/update）
}
```

`Load()` 从环境变量读取 `TELEGRAM_API_URL`、`TELEGRAM_PROXY`、`GLOBAL_PROXY`，均为可选。

`setupFields` 追加三项可选字段，交互向导和 `./mailbot config` 中直接回车即可跳过。

### 2. `internal/proxy`（新增）

职责：根据代理 URL 创建 `*http.Client`。

```go
func NewClient(proxyURL string) (*http.Client, error)
```

支持 scheme：

- `http://`
- `https://`
- `socks5://`
- `socks5h://`（远程 DNS）

支持带认证的 URL，如 `http://user:pass@host:port`、`socks5://user:pass@host:port`。

实现要点：

- HTTP/HTTPS：使用 `http.Transport.Proxy = http.ProxyURL(u)`。
- SOCKS5：使用 `golang.org/x/net/proxy` 创建 `Dialer`，注入 `http.Transport.DialContext`。
- 返回的客户端设置合理超时（建议 30 秒）。
- `proxyURL` 为空时返回 `nil`，由调用方决定 fallback。
- 不支持的 scheme 或解析失败返回明确错误。

### 3. `internal/telegram/bot.go`

`New` 函数签名扩展为接收 `apiURL` 和 `proxyClient`：

```go
func New(
    token string,
    database *sql.DB,
    manager AccountStarter,
    allowedUsers map[int64]bool,
    encryptionKey []byte,
    oauthConfigs map[string]oauth2.Config,
    version string,
    apiURL string,
    telegramProxyClient tgbotapi.HTTPClient,
    globalHTTPClient *http.Client,
) (*Bot, error)
```

实现：

- `Bot` 结构体新增 `httpClient *http.Client` 字段，保存 `globalHTTPClient` 供 `/update` 使用。
- 若 `apiURL` 为空，使用 `tgbotapi.APIEndpoint`。
- 否则把 `apiURL` 拼接为 `apiURL + "/bot%s/%s"`。
- 使用 `tgbotapi.NewBotAPIWithClient(token, endpoint, client)` 创建 BotAPI。
- `telegramProxyClient` 为空时使用 `http.DefaultClient`。
- `globalHTTPClient` 为空时使用 `http.DefaultClient`。

### 4. OAuth 全局代理集成

OAuth 相关函数（`StartDeviceFlow`、`PollToken`、`RefreshIfNeeded`）均通过 `context.Context` 接收配置。调用方在 context 中注入全局代理客户端：

```go
ctx = context.WithValue(ctx, oauth2.HTTPClient, globalClient)
```

具体注入位置：

- `internal/telegram/handlers.go` 的 `startOAuthFlow` 和 `startReauthorize` 在调用 `oauth.StartDeviceFlow` / `oauth.PollToken` 前注入。
- `internal/manager/manager.go` 的 `Manager` 结构体保存全局代理客户端，并在 `tokenProvider` 调用 `oauth.RefreshIfNeeded` 前注入。

无需修改 `internal/oauth` 内部逻辑。

### 5. `internal/manager/manager.go`

`Manager` 结构体新增全局代理客户端字段：

```go
type Manager struct {
    db           *sql.DB
    key          []byte
    send         SendFunc
    oauthConfigs map[string]oauth2.Config
    httpClient   *http.Client // 全局代理客户端
    mu           sync.Mutex
    cancels      map[int64]context.CancelFunc
}
```

`New` 函数增加 `httpClient *http.Client` 参数，保存到结构体中。

`tokenProvider` 在调用 `oauth.RefreshIfNeeded` 前，把 `m.httpClient` 注入 context：

```go
ctx := context.WithValue(context.WithoutCancel(ctx), oauth2.HTTPClient, m.httpClient)
token, err := oauth.RefreshIfNeeded(ctx, m.db, m.key, oauthCfg, account)
```

使用 `context.WithoutCancel(ctx)` 避免父 context 取消导致刷新 token 失败。

### 6. `/update` 全局代理集成

`internal/update/update.go` 当前直接使用 `http.DefaultClient`。将其改为使用传入的 `*http.Client`：

- `Run(currentVersion string, client *http.Client) error`
- `CheckVersion(currentVersion string, client *http.Client) (string, error)`
- 包内 `fetchLatestRelease`、`fetchAsset` 等通过闭包或参数使用 `client`。

`main.go` 的 `./mailbot update` 分支先构造全局代理客户端再调用 `update.Run`。

### 7. `main.go`

启动流程：

1. 调用 `config.Load()` 获取配置。
2. 用 `cfg.TelegramProxy` 创建 Telegram 代理客户端（若配置）。
3. 用 `cfg.GlobalProxy` 创建全局代理客户端（若配置）。
4. 调用 `manager.New(database, encryptionKey, send, oauthConfigs, globalClient)`。
5. 调用 `telegram.New(..., cfg.TelegramAPIURL, telegramClient, globalClient)`。
6. OAuth context 注入全局客户端。
7. `/update` 调用传入全局客户端。

## 数据流

### 启动时

```
config.Load()
  ↓
proxy.NewClient(cfg.TelegramProxy) → telegramClient
proxy.NewClient(cfg.GlobalProxy)   → globalClient
  ↓
manager.New(database, encryptionKey, send, oauthConfigs, globalClient)
telegram.New(token, ..., cfg.TelegramAPIURL, telegramClient, globalClient)
  ↓
ctx = context.WithValue(ctx, oauth2.HTTPClient, globalClient)
  ↓
manager.StartAll(ctx) / 账号监听启动
```

### /update 时

```
main.go update 分支
  ↓
proxy.NewClient(cfg.GlobalProxy) → globalClient
  ↓
update.Run(version, globalClient)
```

## 错误处理

- 代理 URL 格式错误：在启动早期（`main.go` 构造客户端时）返回可读错误并退出。
- 自定义 API URL 无效：由 `tgbotapi.NewBotAPIWithClient` 返回的错误携带。
- 代理连接失败：在具体请求时记录日志，保持现有重连/重试行为。

## 测试策略

### `internal/proxy/proxy_test.go`

- 空字符串返回 `nil`。
- `http://` 和 `https://` 正确设置 `Transport.Proxy`。
- `socks5://` 正确设置 `Transport.DialContext`。
- 带用户名密码的 URL 能被解析。
- 不支持的 scheme 返回错误。

### `internal/config/config_test.go`

- 验证 `TELEGRAM_API_URL`、`TELEGRAM_PROXY`、`GLOBAL_PROXY` 被正确读入 `Config`。

### `internal/telegram/bot_test.go`

- 验证空 `apiURL` 使用默认端点。
- 验证传入 `apiURL` 时拼接为 `/bot%s/%s`（通过检查构造出的 `BotAPI` 端点字段，避免真实网络请求）。

### `internal/update/update_test.go`

- 将 `fetchLatestRelease` / `fetchAsset` 变量替换为使用传入 client 的闭包，保持现有 mock 测试能力。

## 待实现计划入口

本规格确认后，调用 `writing-plans` 技能生成具体实现计划。
