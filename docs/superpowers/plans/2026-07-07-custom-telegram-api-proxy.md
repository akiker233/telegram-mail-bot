# 自定义 Telegram Bot API 后端与代理支持 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 支持自定义 Telegram Bot API 后端 URL，以及分别为 Telegram Bot API 和全局出站请求配置 HTTP/SOCKS5 代理。

**架构：** 新增 `internal/proxy` 包负责按 URL scheme 构造带代理的 `*http.Client`；`Config` 扩展三个可选字段；`telegram.New` 和 `manager.New` 分别接收 Telegram 专用代理客户端和全局代理客户端；OAuth 与 `/update` 通过 context 或参数使用全局客户端。

**技术栈：** Go, `github.com/go-telegram-bot-api/telegram-bot-api/v5`, `golang.org/x/net/proxy`, `golang.org/x/oauth2`

---

## 文件清单

- **创建**
  - `internal/proxy/proxy.go`：根据代理 URL 创建 `*http.Client`，支持 http/https/socks5/socks5h。
  - `internal/proxy/proxy_test.go`：测试 `NewClient` 的空值、HTTP、SOCKS5、认证、错误 scheme 等场景。
  - `internal/telegram/bot_test.go`：测试自定义 API URL 和默认端点选择。
- **修改**
  - `internal/config/config.go`：`Config` 增加 `TelegramAPIURL`、`TelegramProxy`、`GlobalProxy`。
  - `internal/config/config_test.go`：增加对新字段的读取测试。
  - `internal/config/setup.go`：交互向导增加三个可选字段。
  - `.env.example`：增加三个新环境变量的注释占位。
  - `internal/manager/manager.go`：`Manager` 保存全局 `*http.Client`，并在 token 刷新前注入 context。
  - `internal/telegram/bot.go`：`Bot` 保存全局 `*http.Client`，`New` 使用 `tgbotapi.NewBotAPIWithClient`。
  - `internal/telegram/handlers.go`：OAuth 流程注入全局客户端；`/update` 使用 Bot 保存的全局客户端。
  - `internal/update/update.go`：`Run` 和 `CheckVersion` 接收 `*http.Client`。
  - `internal/update/update_test.go`：适配新签名。
  - `cmd/mailbot/main.go`：构造代理客户端并传给所有构造函数。

---

### 任务 1：创建 `internal/proxy/proxy.go`

**文件：**
- 创建：`internal/proxy/proxy.go`

- [ ] **步骤 1：编写实现代码**

```go
package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// NewClient 根据代理 URL 创建 *http.Client。proxyURL 为空时返回 nil。
func NewClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return nil, nil
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("proxy: 解析代理地址失败: %w", err)
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	switch u.Scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		dialer, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("proxy: 创建 SOCKS5 代理拨号器失败: %w", err)
		}
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
	default:
		return nil, fmt.Errorf("proxy: 不支持的代理协议 %q", u.Scheme)
	}

	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}, nil
}
```

- [ ] **步骤 2：运行 go vet / build 检查语法**

运行：`go build ./internal/proxy`
预期：exit 0

- [ ] **步骤 3：Commit**

```bash
git add internal/proxy/proxy.go
git commit -m "feat(proxy): 新增代理客户端构造包，支持 http/https/socks5/socks5h"
```

---

### 任务 2：为 `internal/proxy` 编写测试

**文件：**
- 创建：`internal/proxy/proxy_test.go`

- [ ] **步骤 1：编写失败测试**

```go
package proxy

import (
	"net/http"
	"testing"
)

func TestNewClientEmptyReturnsNil(t *testing.T) {
	c, err := NewClient("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != nil {
		t.Fatalf("expected nil client for empty proxy URL, got %v", c)
	}
}

func TestNewClientHTTP(t *testing.T) {
	c, err := NewClient("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("expected Proxy to be set")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	u, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy returned error: %v", err)
	}
	if u.Host != "127.0.0.1:8080" {
		t.Fatalf("expected proxy host 127.0.0.1:8080, got %s", u.Host)
	}
}

func TestNewClientHTTPSWithAuth(t *testing.T) {
	c, err := NewClient("https://user:pass@proxy.example.com:8443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("expected Proxy to be set")
	}
}

func TestNewClientSOCKS5(t *testing.T) {
	c, err := NewClient("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("expected DialContext to be set")
	}
}

func TestNewClientSOCKS5WithAuth(t *testing.T) {
	c, err := NewClient("socks5://user:pass@127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClientUnsupportedScheme(t *testing.T) {
	_, err := NewClient("ftp://127.0.0.1:1080")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}
```

- [ ] **步骤 2：运行测试**

运行：`go test ./internal/proxy -v`
预期：6 个测试全部 PASS

- [ ] **步骤 3：Commit**

```bash
git add internal/proxy/proxy_test.go
git commit -m "test(proxy): 添加 NewClient 单元测试"
```

---

### 任务 3：扩展 `Config` 读取新环境变量

**文件：**
- 修改：`internal/config/config.go`

- [ ] **步骤 1：修改 `Config` 结构体**

在 `Config` 结构体末尾追加：

```go
	TelegramAPIURL string // 自定义 Telegram Bot API 基础 URL
	TelegramProxy  string // Telegram Bot API 专用代理
	GlobalProxy    string // 全局代理，用于 OAuth、/update 等
```

- [ ] **步骤 2：在 `Load()` 返回值中填充新字段**

在 `return &Config{...}` 中添加：

```go
		TelegramAPIURL:           os.Getenv("TELEGRAM_API_URL"),
		TelegramProxy:            os.Getenv("TELEGRAM_PROXY"),
		GlobalProxy:              os.Getenv("GLOBAL_PROXY"),
```

- [ ] **步骤 3：Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): 增加 TELEGRAM_API_URL、TELEGRAM_PROXY、GLOBAL_PROXY 配置项"
```

---

### 任务 4：为 `Config` 新字段添加测试

**文件：**
- 修改：`internal/config/config_test.go`

- [ ] **步骤 1：在 `TestLoadSuccess` 中增加断言**

在 `TestLoadSuccess` 中 `t.Setenv("DB_PATH", "")` 之后添加：

```go
	t.Setenv("TELEGRAM_API_URL", "https://api.example.com")
	t.Setenv("TELEGRAM_PROXY", "socks5://127.0.0.1:1080")
	t.Setenv("GLOBAL_PROXY", "http://user:pass@proxy:8080")
```

在 `cfg, err := Load()` 之后添加：

```go
	if cfg.TelegramAPIURL != "https://api.example.com" {
		t.Errorf("expected TelegramAPIURL https://api.example.com, got %q", cfg.TelegramAPIURL)
	}
	if cfg.TelegramProxy != "socks5://127.0.0.1:1080" {
		t.Errorf("expected TelegramProxy socks5://127.0.0.1:1080, got %q", cfg.TelegramProxy)
	}
	if cfg.GlobalProxy != "http://user:pass@proxy:8080" {
		t.Errorf("expected GlobalProxy http://user:pass@proxy:8080, got %q", cfg.GlobalProxy)
	}
```

- [ ] **步骤 2：运行测试**

运行：`go test ./internal/config -run TestLoadSuccess -v`
预期：PASS

- [ ] **步骤 3：Commit**

```bash
git add internal/config/config_test.go
git commit -m "test(config): 验证新代理和 API URL 配置项读取"
```

---

### 任务 5：在交互向导中加入新配置项

**文件：**
- 修改：`internal/config/setup.go`

- [ ] **步骤 1：扩展 `setupFields`**

在 `setupFields` 末尾追加：

```go
	{key: "TELEGRAM_API_URL", prompt: "自定义 Telegram Bot API 基础 URL（不需要可留空）", required: false},
	{key: "TELEGRAM_PROXY", prompt: "Telegram Bot API 代理地址，支持 http/https/socks5（不需要可留空）", required: false},
	{key: "GLOBAL_PROXY", prompt: "全局代理地址，用于 OAuth 和更新检查（不需要可留空）", required: false},
```

- [ ] **步骤 2：运行相关测试**

运行：`go test ./internal/config -v`
预期：全部 PASS

- [ ] **步骤 3：Commit**

```bash
git add internal/config/setup.go
git commit -m "feat(config): 交互向导增加 Telegram API URL 与代理配置项"
```

---

### 任务 6：更新 `.env.example`

**文件：**
- 修改：`.env.example`

- [ ] **步骤 1：在文件末尾追加**

```text
# 可选：自定义 Telegram Bot API 基础 URL（例如本地 Bot API 服务器）。
# 程序会自动补齐路径为 <URL>/bot<token>/<method>。留空使用官方 https://api.telegram.org。
TELEGRAM_API_URL=

# 可选：Telegram Bot API 专用代理，支持 http://、https://、socks5://、socks5h://。
# 示例：socks5://127.0.0.1:1080 或 http://user:pass@proxy:8080
TELEGRAM_PROXY=

# 可选：全局代理，用于 OAuth token 刷新和 /update 检查 GitHub Releases。
# 支持协议同上。留空则相关请求走直连。
GLOBAL_PROXY=
```

- [ ] **步骤 2：Commit**

```bash
git add .env.example
git commit -m "docs(env): .env.example 增加 Telegram API URL 与代理配置示例"
```

---

### 任务 7：`Manager` 保存全局代理客户端并注入 OAuth 刷新

**文件：**
- 修改：`internal/manager/manager.go`

- [ ] **步骤 1：修改 `Manager` 结构体**

在 `oauthConfigs` 字段后追加：

```go
	httpClient   *http.Client // 全局代理客户端，用于 OAuth token 刷新
```

需要添加 `net/http` 导入。

- [ ] **步骤 2：修改 `New` 函数签名和实现**

改为：

```go
func New(database *sql.DB, key []byte, send SendFunc, oauthConfigs map[string]oauth2.Config, httpClient *http.Client) *Manager {
	return &Manager{
		db:           database,
		key:          key,
		send:         send,
		oauthConfigs: oauthConfigs,
		httpClient:   httpClient,
		cancels:      make(map[int64]context.CancelFunc),
	}
}
```

- [ ] **步骤 3：在 `tokenProvider` 中注入全局客户端**

把：

```go
		token, err := oauth.RefreshIfNeeded(ctx, m.db, m.key, oauthCfg, account)
```

改为：

```go
		refreshCtx := ctx
		if m.httpClient != nil {
			refreshCtx = context.WithValue(context.WithoutCancel(ctx), oauth2.HTTPClient, m.httpClient)
		}
		token, err := oauth.RefreshIfNeeded(refreshCtx, m.db, m.key, oauthCfg, account)
```

需要确保已导入 `golang.org/x/oauth2`（已有）和 `context`（已有）。

- [ ] **步骤 4：运行编译检查**

运行：`go build ./internal/manager`
预期：exit 0

- [ ] **步骤 5：Commit**

```bash
git add internal/manager/manager.go
git commit -m "feat(manager): 支持全局代理客户端，并在 OAuth token 刷新时注入"
```

---

### 任务 8：`telegram.Bot` 支持自定义 API URL 和代理客户端

**文件：**
- 修改：`internal/telegram/bot.go`

- [ ] **步骤 1：扩展 `Bot` 结构体**

在 `oauthConfigs` 字段后追加：

```go
	httpClient    *http.Client // 全局代理客户端，用于 /update
```

需要添加 `net/http` 导入。

- [ ] **步骤 2：修改 `New` 函数签名和实现**

改为：

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
) (*Bot, error) {
	endpoint := tgbotapi.APIEndpoint
	if apiURL != "" {
		endpoint = strings.TrimRight(apiURL, "/") + "/bot%s/%s"
	}

	client := telegramProxyClient
	if client == nil {
		client = http.DefaultClient
	}

	api, err := tgbotapi.NewBotAPIWithClient(token, endpoint, client)
	if err != nil {
		return nil, err
	}

	// ... 保持原有逻辑 ...

	return &Bot{
		api:           api,
		db:            database,
		manager:       manager,
		sessions:      NewSessionStore(database),
		sendSessions:  NewSendSessionStore(database),
		allowedUsers:  allowedUsers,
		encryptionKey: encryptionKey,
		oauthConfigs:  oauthConfigs,
		version:       version,
		httpClient:    globalHTTPClient,
	}, nil
}
```

需要添加 `strings` 和 `net/http` 导入（如果尚未导入）。

- [ ] **步骤 3：运行编译检查**

运行：`go build ./internal/telegram`
预期：exit 0

- [ ] **步骤 4：Commit**

```bash
git add internal/telegram/bot.go
git commit -m "feat(telegram): Bot 初始化支持自定义 API URL 和代理客户端"
```

---

### 任务 9：为 `telegram.Bot` 自定义 API URL 添加测试

**文件：**
- 创建：`internal/telegram/bot_test.go`

- [ ] **步骤 1：编写测试**

```go
package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestNewUsesDefaultEndpointWhenAPIURLEmpty(t *testing.T) {
	bot, err := New("dummy-token", nil, nil, nil, nil, nil, "", "", nil, nil)
	if err == nil {
		// tgbotapi 可能会因为 token 格式或网络 GetMe 失败，但这里只关心构造行为
		// 实际测试中若 GetMe 成功需要真实 token，通常这里会失败；
		// 因此改为直接检查 BotAPI 端点字段。
	}
	_ = bot
	_ = err
}
```

注意：`tgbotapi.NewBotAPIWithClient` 会在构造时调用 `GetMe`，因此单元测试需要 mock 或检查内部字段。由于库未暴露 endpoint 字段，此测试难以在不发网络请求的情况下断言。更可靠的测试方式是：

- 在 `bot.go` 中提取一个 `buildEndpoint(apiURL string) string` 小函数并导出测试；或者
- 使用真实测试 token 和本地 HTTP server 作为 API 端点验证请求路径。

推荐第一种：

```go
func buildEndpoint(apiURL string) string {
	if apiURL == "" {
		return tgbotapi.APIEndpoint
	}
	return strings.TrimRight(apiURL, "/") + "/bot%s/%s"
}
```

然后测试：

```go
func TestBuildEndpoint(t *testing.T) {
	if got := buildEndpoint(""); got != tgbotapi.APIEndpoint {
		t.Errorf("empty apiURL should use default endpoint, got %q", got)
	}
	if got := buildEndpoint("https://api.example.com"); got != "https://api.example.com/bot%s/%s" {
		t.Errorf("unexpected endpoint: %q", got)
	}
	if got := buildEndpoint("https://api.example.com/"); got != "https://api.example.com/bot%s/%s" {
		t.Errorf("trailing slash should be trimmed, got %q", got)
	}
}
```

- [ ] **步骤 2：运行测试**

运行：`go test ./internal/telegram -run TestBuildEndpoint -v`
预期：PASS

- [ ] **步骤 3：Commit**

```bash
git add internal/telegram/bot.go internal/telegram/bot_test.go
git commit -m "feat(telegram): 提取 endpoint 构造逻辑并添加单元测试"
```

---

### 任务 10：OAuth 流程注入全局代理客户端

**文件：**
- 修改：`internal/telegram/handlers.go`

- [ ] **步骤 1：在 `startOAuthFlow` 中注入全局客户端**

在调用 `oauth.StartDeviceFlow` 前添加：

```go
	flowCtx := b.ctx
	if b.httpClient != nil {
		flowCtx = context.WithValue(b.ctx, oauth2.HTTPClient, b.httpClient)
	}
	resp, err := oauth.StartDeviceFlow(flowCtx, cfg)
```

并同步修改 `oauth.PollToken(flowCtx, cfg, resp)`。

- [ ] **步骤 2：在 `startReauthorize` 中注入全局客户端**

同样把 `b.ctx` 替换为注入了 `b.httpClient` 的 context，传给 `oauth.StartDeviceFlow` 和 `oauth.PollToken`。

- [ ] **步骤 3：运行编译检查**

运行：`go build ./internal/telegram`
预期：exit 0

- [ ] **步骤 4：Commit**

```bash
git add internal/telegram/handlers.go
git commit -m "feat(telegram): OAuth 设备码流程使用全局代理客户端"
```

---

### 任务 11：`/update` 命令使用全局代理客户端

**文件：**
- 修改：`internal/telegram/handlers.go`

- [ ] **步骤 1：修改 `handleUpdate` 中调用 `update.CheckVersion` 和 `update.Run` 的地方**

改为：

```go
		newVer, err := update.CheckVersion(b.version, b.httpClient)
		// ...
		if err := update.Run(b.version, b.httpClient); err != nil {
```

- [ ] **步骤 2：运行编译检查**

运行：`go build ./internal/telegram`
预期：exit 0

- [ ] **步骤 3：Commit**

```bash
git add internal/telegram/handlers.go
git commit -m "feat(telegram): /update 命令使用全局代理客户端"
```

---

### 任务 12：`internal/update` 包接收并正确使用 `*http.Client`

**文件：**
- 修改：`internal/update/update.go`
- 修改：`internal/update/update_test.go`

- [ ] **步骤 1：修改 `CheckVersion` 和 `Run` 签名**

```go
func CheckVersion(currentVersion string, client *http.Client) (string, error)
func Run(currentVersion string, client *http.Client) error
```

- [ ] **步骤 2：修改 `fetchLatestReleaseHTTP` 和 `fetchAssetHTTP` 为接收 client 的闭包/参数**

把 `fetchLatestRelease = fetchLatestReleaseHTTP` 改为：

```go
var (
	fetchLatestRelease = fetchLatestReleaseHTTP
	fetchAsset         = fetchAssetHTTP
	applyBinary        = applyBinaryReal
)
```

并修改：

```go
func fetchLatestReleaseHTTP(ctx context.Context, client *http.Client) (*Release, error) {
	// ... 使用 client.Do(req) 替代 http.DefaultClient.Do(req)
}

func fetchAssetHTTP(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	// ... 使用 client.Do(req) 替代 http.DefaultClient.Do(req)
}
```

`Run` 和 `CheckVersion` 在调用这两个函数时传入 `client`。

如果测试用例通过替换 `fetchLatestRelease`/`fetchAsset` 变量来 mock，则无需在测试中传递真实 client；但函数签名变化后，测试文件中对 `CheckVersion` 和 `Run` 的调用需要补传 `nil` 或 `http.DefaultClient`。

- [ ] **步骤 3：更新 `update_test.go`**

找到所有 `CheckVersion(` 和 `Run(` 调用，传入 `http.DefaultClient` 或 `nil`。

- [ ] **步骤 4：运行测试**

运行：`go test ./internal/update -v`
预期：全部 PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/update/update.go internal/update/update_test.go
git commit -m "feat(update): 检查更新流程支持传入 *http.Client"
```

---

### 任务 13：`cmd/mailbot/main.go` 组装代理客户端并传递给各模块

**文件：**
- 修改：`cmd/mailbot/main.go`

- [ ] **步骤 1：导入 `internal/proxy` 包**

```go
import (
	// ... 现有导入 ...
	"telegram-mail-bot/internal/proxy"
)
```

- [ ] **步骤 2：在 `main()` 中构造客户端**

在 `cfg, err := config.Load()` 之后添加：

```go
	telegramClient, err := proxy.NewClient(cfg.TelegramProxy)
	if err != nil {
		slog.Error("解析 Telegram 代理配置失败", "err", err)
		os.Exit(1)
	}

	globalClient, err := proxy.NewClient(cfg.GlobalProxy)
	if err != nil {
		slog.Error("解析全局代理配置失败", "err", err)
		os.Exit(1)
	}
```

- [ ] **步骤 3：修改 `update` 子命令分支**

把：

```go
	if err := update.Run(version); err != nil {
```

改为：

```go
	globalClient, err := proxy.NewClient(os.Getenv("GLOBAL_PROXY"))
	if err != nil {
		slog.Error("解析全局代理配置失败", "err", err)
		os.Exit(1)
	}
	if err := update.Run(version, globalClient); err != nil {
```

注意：`./mailbot update` 子命令不走 `config.Load()`，因此直接从环境变量读取 `GLOBAL_PROXY`。

- [ ] **步骤 4：修改 manager 和 bot 的创建**

把：

```go
	mgr := manager.New(database, encryptionKey, send, oauthConfigs)

	bot, err = telegram.New(cfg.TelegramBotToken, database, mgr, cfg.AllowedUsers, encryptionKey, oauthConfigs, version)
```

改为：

```go
	mgr := manager.New(database, encryptionKey, send, oauthConfigs, globalClient)

	bot, err = telegram.New(cfg.TelegramBotToken, database, mgr, cfg.AllowedUsers, encryptionKey, oauthConfigs, version, cfg.TelegramAPIURL, telegramClient, globalClient)
```

- [ ] **步骤 5：运行编译检查**

运行：`go build ./cmd/mailbot`
预期：exit 0

- [ ] **步骤 6：Commit**

```bash
git add cmd/mailbot/main.go
git commit -m "feat(main): 组装代理客户端并传递给 manager、telegram 和 update"
```

---

### 任务 14：全量测试与构建验证

- [ ] **步骤 1：运行所有单元测试**

运行：`go test ./...`
预期：全部 PASS

- [ ] **步骤 2：运行 go vet**

运行：`go vet ./...`
预期：无报错

- [ ] **步骤 3：构建二进制**

运行：`go build -o mailbot ./cmd/mailbot`
预期：exit 0，生成可执行文件

- [ ] **步骤 4：Commit（如只有测试/构建无代码变更则跳过）**

---

## 自检

- **规格覆盖度：** 每个规格项都对应到任务。
  - 自定义 API URL → 任务 8、9
  - Telegram 代理 → 任务 1、2、8、13
  - 全局代理 → 任务 1、2、7、10、11、12、13
  - 交互向导 → 任务 5
  - .env.example → 任务 6
- **占位符扫描：** 无 TODO、待定或模糊描述。
- **类型一致性：** `telegram.New` 和 `manager.New` 签名在所有任务中保持一致；`proxy.NewClient` 返回 `(*http.Client, error)`。
