import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { AuthProvider } from './auth/AuthProvider'
import { RequireAuth } from './auth/RequireAuth'
import { PermissionGate } from './auth/PermissionGate'
import { LandingRedirect } from './auth/LandingRedirect'
import { ToastProvider } from './components/toast'
import { AdminLayout } from './layout/AdminLayout'
import { AdministratorsPage } from './pages/AdministratorsPage'
import { DashboardPage } from './pages/DashboardPage'
import { FinancePage } from './pages/FinancePage'
import { LoginPage } from './pages/LoginPage'
import { PrivacyRequestsPage } from './pages/PrivacyRequestsPage'
import { QuestionsPage } from './pages/QuestionsPage'
import { SettingsPage } from './pages/SettingsPage'
import { TasksPage } from './pages/TasksPage'
import { UsersPage } from './pages/UsersPage'
import { AppearanceProvider } from './theme/AppearanceProvider'
import { Button, EmptyState, PageHeader } from './components'

function NotFoundPage() {
  return (
    <>
      <PageHeader title="页面不存在"  />
      <div className="card">
        <EmptyState
          title="未找到此页面"
          description="请从左侧导航进入运行总览、账号、任务、财务或题库管理。"
          action={<Button onClick={() => window.history.back()}>返回上一页</Button>}
        />
      </div>
    </>
  )
}

export function App() {
  return (
    <BrowserRouter>
      <ToastProvider>
        <AuthProvider>
          <AppearanceProvider>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route
                element={
                  <RequireAuth>
                    <AdminLayout />
                  </RequireAuth>
                }
              >
                <Route path="/" element={<LandingRedirect />} />
                <Route path="/dashboard" element={<PermissionGate permission="learning.read"><DashboardPage /></PermissionGate>} />
                <Route path="/users" element={<PermissionGate permission="learning.read"><UsersPage /></PermissionGate>} />
                <Route path="/tasks" element={<PermissionGate permission="learning.read"><TasksPage /></PermissionGate>} />
                <Route path="/finance" element={<PermissionGate permission="billing.read"><FinancePage /></PermissionGate>} />
                <Route path="/questions" element={<PermissionGate permission="question:read"><QuestionsPage /></PermissionGate>} />
                <Route path="/administrators" element={<PermissionGate permission="admin.users.manage"><AdministratorsPage /></PermissionGate>} />
                <Route path="/privacy-requests" element={<PermissionGate permission="privacy.review"><PrivacyRequestsPage /></PermissionGate>} />
                <Route path="/settings" element={<SettingsPage />} />
                <Route path="*" element={<NotFoundPage />} />
              </Route>
            </Routes>
          </AppearanceProvider>
        </AuthProvider>
      </ToastProvider>
    </BrowserRouter>
  )
}
