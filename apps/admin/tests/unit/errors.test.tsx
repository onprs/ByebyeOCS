import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ApiError } from '../../src/api/client'
import { ErrorState } from '../../src/components/ui'

describe('接口错误反馈', () => {
  it('412 显示版本冲突、请求编号与重新加载', () => {
    render(
      <ErrorState
        error={new ApiError(412, {
          code: 'version_conflict',
          message: '设置已在其他设备更新。',
          request_id: 'req_conflict',
          retryable: false,
          scope: 'request',
        })}
        onRetry={() => undefined}
      />,
    )
    expect(screen.getByText('数据版本已变化')).toBeInTheDocument()
    expect(screen.getByText('设置已在其他设备更新。')).toBeInTheDocument()
    expect(screen.getByText('请求编号 req_conflict')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
  })

  it('429 展示重试等待时间', () => {
    render(
      <ErrorState
        error={new ApiError(
          429,
          {
            code: 'rate_limited',
            message: '请求过于频繁。',
            request_id: 'req_limited',
            retryable: true,
            scope: 'request',
          },
          90,
        )}
      />,
    )
    expect(screen.getByText('请求过于频繁')).toBeInTheDocument()
    expect(screen.getByText('2 分钟后可重试')).toBeInTheDocument()
  })

  it('503 未配置使用独立标题且不声称成功', () => {
    render(
      <ErrorState
        error={new ApiError(503, {
          code: 'not_configured',
          message: '题库服务尚未配置。',
          request_id: 'req_config',
          retryable: true,
          scope: 'service',
        })}
      />,
    )
    expect(screen.getByText('功能尚未配置')).toBeInTheDocument()
    expect(screen.queryByText('操作成功')).not.toBeInTheDocument()
  })

  it('403 不提供重试按钮', () => {
    render(
      <ErrorState
        error={new ApiError(403, {
          code: 'forbidden',
          message: '当前管理员无权执行此操作。',
          request_id: 'req_forbidden',
          retryable: false,
          scope: 'request',
        })}
        onRetry={() => undefined}
      />,
    )
    expect(screen.getByText('没有执行此操作的权限')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重新加载' })).not.toBeInTheDocument()
  })
})
