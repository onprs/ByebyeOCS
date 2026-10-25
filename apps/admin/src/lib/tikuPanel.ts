// 题库维护面板与主管理端独立部署；同域默认挂在 /panel/，维护环境可用环境变量覆盖。
const configured = (import.meta.env.VITE_TIKU_PANEL_URL as string | undefined)?.trim()

export const tikuPanelUrl = configured && configured.length > 0 ? configured : '/panel/'

export function openTikuPanel() {
  window.open(tikuPanelUrl, '_blank', 'noopener,noreferrer')
}
