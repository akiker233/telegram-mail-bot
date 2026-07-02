package smtp

import (
	"errors"
	"fmt"
	"net/smtp"
)

// xoauth2Auth 实现 smtp.Auth 接口。标准库的 net/smtp 不支持 XOAUTH2（是 Google 专有机制），
// 协议格式与 IMAP 侧相同，但 smtp.Auth 和 sasl.Client 是两套不同的接口，不能共用同一个实现：
// https://developers.google.com/workspace/gmail/imap/xoauth2-protocol
type xoauth2Auth struct {
	username string
	token    string
}

// NewXOAUTH2Auth 创建一个用于 SMTP AUTH XOAUTH2 的 smtp.Auth。
func NewXOAUTH2Auth(username, token string) smtp.Auth {
	return &xoauth2Auth{username: username, token: token}
}

func (a *xoauth2Auth) Start(server *smtp.ServerInfo) (proto string, toServer []byte, err error) {
	toServer = []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.username, a.token))
	return "XOAUTH2", toServer, nil
}

func (a *xoauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	// 服务器认为 token 无效时会返回一个 JSON 错误 challenge 并要求继续响应；
	// XOAUTH2 不支持第二轮正常交互，这里把这种情况当作认证失败直接报错。
	return nil, errors.New("smtp: xoauth2 authentication rejected: " + string(fromServer))
}
