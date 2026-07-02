package oauth

import (
	"context"
	"time"

	"golang.org/x/oauth2"
)

// deviceFlowTimeout 是用户完成浏览器授权的最长等待时间。
// oauth2.Config.DeviceAccessToken 本身不会超时，必须由调用方通过 context 控制。
const deviceFlowTimeout = 8 * time.Minute

// StartDeviceFlow 向服务商申请一个设备码，返回需要展示给用户的链接和一次性代码。
func StartDeviceFlow(ctx context.Context, cfg oauth2.Config) (*oauth2.DeviceAuthResponse, error) {
	return cfg.DeviceAuth(ctx)
}

// PollToken 轮询 token 端点直到用户完成授权、拒绝授权或超时。
// oauth2.Config.DeviceAccessToken 内部已经处理了 authorization_pending/slow_down 的重试节奏，
// 这里只需要包一层超时。
func PollToken(ctx context.Context, cfg oauth2.Config, resp *oauth2.DeviceAuthResponse) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(ctx, deviceFlowTimeout)
	defer cancel()
	return cfg.DeviceAccessToken(ctx, resp)
}
