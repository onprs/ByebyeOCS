export function Logo({ compact = false }: { compact?: boolean }) {
  return (
    <span className="brand" aria-label="ByebyeOCS">
      <span className="brand-mark" aria-hidden="true">
        <svg viewBox="0 0 30 30">
          <path d="M8 6h9a5 5 0 0 1 0 10H8V6Zm0 10h10a4 4 0 0 1 0 8H8V16Z" fill="none" stroke="currentColor" strokeWidth="2.5" />
          <path d="M4 6v18" stroke="currentColor" strokeWidth="2.5" />
        </svg>
      </span>
      {compact ? null : (
        <span>
          Byebye<span className="brand-light">OCS</span>
        </span>
      )}
    </span>
  )
}
