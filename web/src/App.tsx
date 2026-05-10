import { useEffect } from 'react';
import { Navigate, Route, Routes, useNavigate } from 'react-router-dom';
import { AppShell } from './components/AppShell';
import { RequireAuth } from './components/RequireAuth';
import { AUTH_EXPIRED } from './api/client';
import { LiveTailPage } from './pages/LiveTailPage';
import { LoginPage } from './pages/LoginPage';
import { LogsPage } from './pages/LogsPage';
import { ProjectsPage } from './pages/ProjectsPage';
import { SettingsPage } from './pages/SettingsPage';

// App is the top-level router. Layered structure:
//
//   /login                         public, no shell
//   /                              redirect — see RootRedirect below
//   /projects                      shell + RequireAuth, lists projects
//   /projects/:slug                redirect to /projects/:slug/logs
//   /projects/:slug/logs|live|settings  per-project pages inside shell
//
// A single effect at the top listens for the AUTH_EXPIRED window event
// (fired by client.ts on any 401) and bounces the user to /login.
export function App() {
  const navigate = useNavigate();

  useEffect(() => {
    const handler = () => navigate('/login', { replace: true });
    window.addEventListener(AUTH_EXPIRED, handler);
    return () => window.removeEventListener(AUTH_EXPIRED, handler);
  }, [navigate]);

  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route path="/" element={<RootRedirect />} />
        <Route path="/projects" element={<ProjectsPage />} />
        <Route path="/projects/:slug" element={<Navigate to="logs" replace />} />
        <Route path="/projects/:slug/logs" element={<LogsPage />} />
        <Route path="/projects/:slug/live" element={<LiveTailPage />} />
        <Route path="/projects/:slug/settings" element={<SettingsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/projects" replace />} />
    </Routes>
  );
}

// RootRedirect is the destination for "/". If localStorage has a
// last-active project slug, send the user there; otherwise to the
// list. We don't validate that the slug still exists — the projects
// fetch on the destination page will surface a 404 if it doesn't, and
// the user will pick another. Cheap to be wrong.
function RootRedirect() {
  let slug = '';
  try {
    slug = window.localStorage.getItem('mylogger42:lastProject') ?? '';
  } catch {
    // private mode / quota — fall through to /projects
  }
  if (slug) {
    return <Navigate to={`/projects/${encodeURIComponent(slug)}/logs`} replace />;
  }
  return <Navigate to="/projects" replace />;
}
