package chaoxing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// BeginSession 在一次课程同步或其他多步操作内保持隔离的Cookie轮换连续。
func (p *Platform) BeginSession(ctx context.Context, raw []byte) (context.Context, func(), error) {
	session, err := decodeSession(raw)
	if err != nil {
		return ctx, nil, learning.Fail(learning.FailureIdentityInvalid, "平台会话格式无效")
	}
	return p.withSessionClient(ctx, session)
}

// BeginWorkSession 兼容自动作答作用域，取题至提交使用同一个隔离容器。
func (p *Platform) BeginWorkSession(ctx context.Context, raw []byte) (context.Context, func(), error) {
	return p.BeginSession(ctx, raw)
}

type platformClientKey struct{}

type platformSessionClient struct {
	owner       *Platform
	client      *http.Client
	fingerprint [32]byte
}

// withSessionClient 只在一次平台操作内部共享Cookie轮换，不在账号或并发任务之间共享容器。
func (p *Platform) withSessionClient(ctx context.Context, session Session) (context.Context, func(), error) {
	client, cleanup, err := p.requestClient(ctx, session)
	if err != nil {
		return ctx, nil, err
	}
	raw, err := json.Marshal(session)
	if err != nil {
		cleanup()
		return ctx, nil, learning.Fail(learning.FailureIdentityInvalid, "平台会话无法识别")
	}
	return context.WithValue(ctx, platformClientKey{}, platformSessionClient{owner: p, client: client, fingerprint: sha256.Sum256(raw)}), cleanup, nil
}

func (p *Platform) requestClient(ctx context.Context, session Session) (*http.Client, func(), error) {
	raw, err := json.Marshal(session)
	if err != nil {
		return nil, nil, learning.Fail(learning.FailureIdentityInvalid, "平台会话无法识别")
	}
	if scoped, ok := ctx.Value(platformClientKey{}).(platformSessionClient); ok && scoped.owner == p && scoped.fingerprint == sha256.Sum256(raw) {
		return scoped.client, func() {}, nil
	}
	jar, err := sessionJar(session)
	if err != nil {
		return nil, nil, learning.Fail(learning.FailureIdentityInvalid, "平台会话无法建立Cookie容器")
	}
	client := p.factory.client(jar)
	return client, client.CloseIdleConnections, nil
}
