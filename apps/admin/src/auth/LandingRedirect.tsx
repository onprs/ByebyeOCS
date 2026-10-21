import { Navigate } from 'react-router-dom'
import { useAuth } from './AuthProvider'
import { landingPath } from './permissions'

export function LandingRedirect() {
  const { me } = useAuth()
  return <Navigate to={landingPath(me)} replace />
}
