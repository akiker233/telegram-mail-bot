package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// envField 描述首次运行向导中需要询问的一个环境变量。
type envField struct {
	key      string
	prompt   string
	required bool
}

var setupFields = []envField{
	{key: "TELEGRAM_BOT_TOKEN", prompt: "Telegram Bot Token（从 @BotFather 获取）", required: true},
	{key: "MASTER_KEY", prompt: "加密主密钥（建议用 openssl rand -hex 32 生成一个随机值，确定后不要更换）", required: true},
	{key: "ALLOWED_TELEGRAM_USERS", prompt: "允许使用机器人的 Telegram 用户 ID，多个用逗号分隔（可用 @userinfobot 查看自己的 ID）", required: true},
	{key: "DB_PATH", prompt: "SQLite 数据库文件路径（留空使用默认值 ./mailbot.db）", required: false},
	{key: "GMAIL_OAUTH_CLIENT_ID", prompt: "Gmail OAuth2 Client ID（不需要 OAuth 登录可留空跳过）", required: false},
	{key: "GMAIL_OAUTH_CLIENT_SECRET", prompt: "Gmail OAuth2 Client Secret（留空跳过）", required: false},
	{key: "OUTLOOK_OAUTH_CLIENT_ID", prompt: "Outlook OAuth2 Client ID（不需要 OAuth 登录可留空跳过）", required: false},
	{key: "OUTLOOK_OAUTH_CLIENT_SECRET", prompt: "Outlook OAuth2 Client Secret（留空跳过）", required: false},
}

// RunInteractiveSetupIfNeeded 在必填环境变量缺失且当前处于交互式终端时，
// 引导用户在命令行中依次填写 .env 所需内容（非必填项直接回车即可跳过），
// 并将填写结果写入工作目录下的 .env 文件。
// 非交互环境（如无 TTY 的容器/CI）下静默跳过，避免程序因等待输入而卡死；
// 后续正常走 Load() 的既有校验逻辑报错。
func RunInteractiveSetupIfNeeded() error {
	_ = godotenv.Load()
	if hasRequiredEnv() || !isInteractiveTerminal() {
		return nil
	}
	return runSetupWizard(os.Stdin, os.Stdout)
}

func runSetupWizard(in io.Reader, out io.Writer) error {
	fmt.Fprintln(out, "检测到缺少必要的配置，开始引导填写 .env（直接回车可跳过非必填项）：")

	reader := bufio.NewReader(in)
	values := make(map[string]string)
	for _, field := range setupFields {
		if os.Getenv(field.key) != "" {
			continue
		}
		for {
			fmt.Fprintf(out, "%s: ", field.prompt)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" && field.required {
				fmt.Fprintln(out, "该项为必填，请输入。")
				continue
			}
			if line != "" {
				values[field.key] = line
				os.Setenv(field.key, line)
			}
			break
		}
	}

	return appendEnvFile(values, out)
}

func hasRequiredEnv() bool {
	for _, field := range setupFields {
		if field.required && os.Getenv(field.key) == "" {
			return false
		}
	}
	return true
}

func isInteractiveTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// appendEnvFile 把新填写的值追加写入 .env（不存在则创建），不触碰其中已有的内容。
func appendEnvFile(values map[string]string, out io.Writer) error {
	if len(values) == 0 {
		return nil
	}

	f, err := os.OpenFile(".env", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("config: 写入 .env 失败: %w", err)
	}
	defer f.Close()

	for _, field := range setupFields {
		v, ok := values[field.key]
		if !ok {
			continue
		}
		if _, err := fmt.Fprintf(f, "%s=%s\n", field.key, v); err != nil {
			return fmt.Errorf("config: 写入 .env 失败: %w", err)
		}
	}

	fmt.Fprintln(out, "配置已保存到 .env")
	return nil
}
