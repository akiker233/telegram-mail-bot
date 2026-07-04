package oauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golang.org/x/oauth2"

	"telegram-mail-bot/internal/crypto"
	"telegram-mail-bot/internal/db"
)

// expiryMargin 提前判定过期的余量，避免 access token 在网络请求路上过期。
const expiryMargin = 2 * time.Minute

// permanentErrorCodes 是 RFC 6749 token 端点错误里代表凭证本身失效的 error code，
// 重试无法自愈（refresh token 被撤销/过期，或 OAuth 客户端配置错误）。
// 其余错误（网络问题、服务器瞬时 5xx、临时性 rate limit 等）应当当作可重试的瞬时错误。
var permanentErrorCodes = map[string]bool{
	"invalid_grant":       true,
	"invalid_client":      true,
	"unauthorized_client": true,
}

// IsPermanent 判断一次 RefreshIfNeeded 失败是否是不可通过重试恢复的永久性错误
// （refresh token 被撤销/过期），而不是网络抖动等瞬时错误。
func IsPermanent(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		return permanentErrorCodes[retrieveErr.ErrorCode]
	}
	return false
}

// RefreshIfNeeded 返回一个可用的 access token，过期（或即将过期）时用 refresh token 换新的
// 并加密写回数据库。account 里的字段不会被修改，调用方如果需要最新状态应重新从数据库读取。
func RefreshIfNeeded(ctx context.Context, database *sql.DB, key []byte, cfg oauth2.Config, account *db.Account) (accessToken string, err error) {
	if !account.OAuthTokenExpiry.IsZero() && time.Until(account.OAuthTokenExpiry) > expiryMargin {
		return crypto.Decrypt(key, account.OAuthAccessToken)
	}

	refreshToken, err := crypto.Decrypt(key, account.OAuthRefreshToken)
	if err != nil {
		return "", fmt.Errorf("oauth: decrypt refresh token: %w", err)
	}

	tokenSource := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	newToken, err := tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("oauth: refresh token: %w", err)
	}

	encryptedAccess, err := crypto.Encrypt(key, newToken.AccessToken)
	if err != nil {
		return "", fmt.Errorf("oauth: encrypt access token: %w", err)
	}

	var encryptedRefresh string
	if newToken.RefreshToken != "" && newToken.RefreshToken != refreshToken {
		// Microsoft 有时会在刷新时轮换 refresh token，Google 通常不会；
		// 只有拿到新值时才覆盖，否则保留旧值（UpdateOAuthTokens 里空字符串表示不更新）。
		encryptedRefresh, err = crypto.Encrypt(key, newToken.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("oauth: encrypt refresh token: %w", err)
		}
	}

	if err := db.UpdateOAuthTokens(database, account.ID, encryptedAccess, encryptedRefresh, newToken.Expiry); err != nil {
		return "", fmt.Errorf("oauth: save refreshed tokens: %w", err)
	}

	return newToken.AccessToken, nil
}
