package operations

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// SetRevokeUser 在服务开始接收请求前装配认证撤销器。
func (s *Service) SetRevokeUser(revoke func(context.Context, pgx.Tx, string) error) {
	s.options.RevokeUser = revoke
}
