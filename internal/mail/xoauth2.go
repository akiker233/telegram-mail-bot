package mail

import "fmt"

// xoauth2Client 实现 sasl.Client 接口。go-imap 依赖的 github.com/emersion/go-sasl
// 不包含 XOAUTH2 机制（该机制是 Google 专有的，曾在 go-sasl issue #18 中被主动移除），
// 所以这里自己实现协议里描述的两步交互：
// https://developers.google.com/workspace/gmail/imap/xoauth2-protocol
type xoauth2Client struct {
	username string
	token    string
}

// newXOAUTH2Client 创建一个用于 IMAP AUTHENTICATE XOAUTH2 的 sasl.Client。
func newXOAUTH2Client(username, token string) *xoauth2Client {
	return &xoauth2Client{username: username, token: token}
}

func (a *xoauth2Client) Start() (mech string, ir []byte, err error) {
	ir = []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.username, a.token))
	return "XOAUTH2", ir, nil
}

// Next 在服务器因 token 无效等原因拒绝时会返回一个 JSON 格式的错误 challenge，
// 此时协议要求客户端回复一个空字节串来结束这轮失败的 AUTHENTICATE，
// 否则连接会卡在等待客户端响应上。返回 error 而不是空字节串会导致 go-imap 直接断开连接
// 而不是让服务器返回明确的 NO/BAD 状态，所以这里始终返回 nil error。
func (a *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	return []byte{}, nil
}
