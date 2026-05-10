import { useEffect, useMemo } from 'react';
import { Link, NavLink, Outlet, useLocation, useParams } from 'react-router-dom';
import { useAuth } from '../hooks/useAuth';
import { useProjects } from '../hooks/useProjects';
import { ProjectSwitcher } from './ProjectSwitcher';

// AppShell renders the persistent header (brand + project switcher +
// nav tabs + logout) and an Outlet for the routed page. Sits inside
// RequireAuth so the user is guaranteed authenticated.
//
// The active page section is derived from the URL so the project
// switcher can preserve it across project changes.
export function AppShell() {
  const { user, logout } = useAuth();
  const { projects } = useProjects();
  const params = useParams();
  const location = useLocation();

  // Derive the page section from the current path. Routes look like
  // /projects/:slug/logs|live|settings. If we're on /projects (the
  // listing page, no slug yet) we default to "logs" so the switcher
  // sends the user somewhere sensible on selection.
  const pageSection = useMemo(() => {
    const segs = location.pathname.split('/').filter(Boolean);
    if (segs[0] === 'projects' && segs.length >= 3) {
      return segs[2]; // /projects/:slug/<section>
    }
    return 'logs';
  }, [location.pathname]);

  // Persist last-active project so the root-redirect can pick it up.
  useEffect(() => {
    if (params.slug) {
      try {
        window.localStorage.setItem('mylogger42:lastProject', params.slug);
      } catch {
        // best-effort
      }
    }
  }, [params.slug]);

  return (
    <div className="app">
      <header className="app-header">
        <Link to="/projects" className="brand">
          MyLogger<span className="brand-accent">42</span>
        </Link>
        <ProjectSwitcher projects={projects} pageSection={pageSection} />
        {params.slug ? (
          <nav className="app-nav">
            <NavLink to={`/projects/${params.slug}/logs`} className={navClass}>
              Logs
            </NavLink>
            <NavLink to={`/projects/${params.slug}/live`} className={navClass}>
              Live tail
            </NavLink>
            <NavLink to={`/projects/${params.slug}/settings`} className={navClass}>
              Settings
            </NavLink>
          </nav>
        ) : null}
        <div className="app-spacer" />
        <span className="app-user">{user?.username}</span>
        <button
          type="button"
          className="app-logout"
          onClick={() => {
            void logout();
          }}
          aria-label="Log out"
          title="Log out"
        >
          ⏻
        </button>
      </header>
      <main className="app-main">
        <Outlet />
      </main>
    </div>
  );
}

function navClass({ isActive }: { isActive: boolean }) {
  return isActive ? 'nav-link nav-link-active' : 'nav-link';
}
