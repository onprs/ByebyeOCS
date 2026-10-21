import { Check, Moon, Palette, Sun } from 'lucide-react'
import { usePreview } from './App'
import { directions } from './data'
import { Badge, Select } from './ui'

export function AppearanceSettings() {
  const { design, setDesign, theme, setTheme } = usePreview()
  return <section className="appearance-settings" aria-label="外观偏好">
    <h2 className="profile-section-title"><Palette size={17}/>外观</h2>
    <div className="settings-toggle"><div><b>配色方案</b><p>默认使用石墨，也可以选择你喜欢的配色。</p></div><Badge>{directions.find(d => d.id === design)?.name}</Badge></div>
    <div className="palette-options" role="group" aria-label="选择配色方案">{directions.map(direction => <button key={direction.id} type="button" aria-pressed={design === direction.id} aria-label={`使用${direction.name}配色`} className={`palette-option palette-${direction.id} ${design === direction.id ? 'selected' : ''}`} onClick={() => setDesign(direction.id)}>
      <span className="palette-sample" aria-hidden="true"><i/><span><b/><em/><em/></span></span>
      <span className="palette-caption"><b>{direction.name}</b>{design === direction.id ? <Check size={13}/> : direction.id === 'graphite' ? <small>默认</small> : null}</span>
    </button>)}</div>
    <div className="settings-toggle"><div><b>界面主题</b><p>浅色与深色均可搭配所选配色。</p></div><Select label="界面主题" value={theme} onChange={setTheme} options={[{ value: 'light', label: '浅色' }, { value: 'dark', label: '深色' }]}/></div>
    <div className="theme-previews"><button type="button" aria-pressed={theme === 'light'} className={`theme-tile light ${theme === 'light' ? 'selected' : ''}`} onClick={() => setTheme('light')}><div><i/><section><b/><span/><span/></section></div><span><Sun size={13}/>浅色</span></button><button type="button" aria-pressed={theme === 'dark'} className={`theme-tile dark ${theme === 'dark' ? 'selected' : ''}`} onClick={() => setTheme('dark')}><div><i/><section><b/><span/><span/></section></div><span><Moon size={13}/>深色</span></button></div>
  </section>
}
