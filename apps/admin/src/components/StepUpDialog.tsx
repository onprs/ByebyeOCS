import { useEffect, useState } from 'react'
import { stepUp } from '../api/endpoints'
import { useToast } from './toast'
import { Button, ErrorState, Field, Modal } from './ui'

export function StepUpDialog({
  open,
  onClose,
  onSuccess,
}: {
  open: boolean
  onClose: () => void
  onSuccess?: () => void
}) {
  const { notify } = useToast()
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)

  useEffect(() => {
    if (!open) {
      setPassword('')
      setTotp('')
      setError(null)
    }
  }, [open])

  const submit = async () => {
    if (!password || !/^\d{6}$/.test(totp)) {
      setError(new Error('请输入密码和 6 位动态验证码。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      await stepUp({ password, totp })
      setPassword('')
      setTotp('')
      notify('管理员身份已重新验证。', 'success')
      onSuccess?.()
      onClose()
    } catch (reason) {
      setError(reason)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="重新验证管理员身份"
      description="敏感管理操作需要再次验证管理员身份。密码和动态验证码只用于本次验证，不会保存到浏览器。"
      actions={
        <>
          <Button onClick={onClose} disabled={saving}>取消</Button>
          <Button variant="primary" onClick={() => void submit()} loading={saving}>验证身份</Button>
        </>
      }
    >
      <Field label="密码" required>
        <input type="password" autoComplete="current-password" value={password} onChange={event => setPassword(event.target.value)} />
      </Field>
      <Field label="动态验证码" required hint="输入认证器当前的 6 位验证码。">
        <input
          inputMode="numeric"
          autoComplete="one-time-code"
          maxLength={6}
          value={totp}
          onChange={event => setTotp(event.target.value.replace(/\s+/g, '').slice(0, 6))}
        />
      </Field>
      {error ? <ErrorState error={error} compact /> : null}
    </Modal>
  )
}
