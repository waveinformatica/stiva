import { useEffect, useState } from "react";
import { Loader } from "@mantine/core";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import Login from "./pages/Login";
import Explorer from "./pages/Explorer";
import ApiKeys from "./pages/ApiKeys";
import Users from "./pages/admin/Users";
import ServiceAccounts from "./pages/admin/ServiceAccounts";
import Stores from "./pages/admin/Stores";
import Credentials from "./pages/admin/Credentials";
import Roles from "./pages/admin/Roles";
import Groups from "./pages/admin/Groups";
import Grants from "./pages/admin/Grants";
import Anonymous from "./pages/admin/Anonymous";
import Registries from "./pages/admin/Registries";
import Jobs from "./pages/admin/Jobs";
import Webhooks from "./pages/admin/Webhooks";
import SSO from "./pages/admin/SSO";
import Settings from "./pages/admin/Settings";
import AppShellLayout, { View, ShellUser } from "./components/AppShellLayout";
import { pathForView, viewForPath } from "./lib/routes";
import { api, clearToken, getToken, setToken, MeResponse } from "./lib/api";

const ADMIN_VIEWS: View[] = [
  "users",
  "serviceAccounts",
  "registries",
  "stores",
  "jobs",
  "webhooks",
  "credentials",
  "roles",
  "groups",
  "grants",
  "anonymous",
  "sso",
  "settings",
];

// Views a guest cannot use: a deep link to one asks for a sign-in.
const SIGNIN_VIEWS: View[] = [...ADMIN_VIEWS, "apikeys"];

export default function App() {
  const [user, setUser] = useState<ShellUser | null>(null);
  const [loading, setLoading] = useState(true);
  // The sign-in page is shown on request when anonymous browsing is available.
  const [showLogin, setShowLogin] = useState(false);

  // Ask who the server thinks we are. Without a token that is the anonymous
  // identity, when the registry allows anonymous access; otherwise the call
  // fails and the sign-in page is all there is.
  const refresh = () => {
    // The SSO callback returns the session token in the URL fragment.
    const hash = window.location.hash;
    if (hash.includes("sso_token=")) {
      const token = new URLSearchParams(hash.slice(1)).get("sso_token");
      window.location.hash = "";
      if (token) setToken(token);
    } else if (hash.includes("sso_error=")) {
      setShowLogin(true); // the login page shows the error
    }

    const resolve = (m: MeResponse) => {
      setUser(m.user);
      if (m.user.anonymous) {
        const v = viewForPath(window.location.pathname);
        if (v && SIGNIN_VIEWS.includes(v)) setShowLogin(true);
      }
    };
    api
      .me()
      .then(resolve)
      .catch(() => {
        const hadToken = !!getToken();
        clearToken();
        if (!hadToken) {
          setUser(null);
          return;
        }
        // An expired or revoked session falls back to guest browsing.
        return api
          .me()
          .then(resolve)
          .catch(() => setUser(null));
      })
      .finally(() => setLoading(false));
  };

  useEffect(refresh, []);

  if (loading) return <Loader />;
  if (!user || showLogin) {
    return (
      <Login
        onLogin={() => {
          setShowLogin(false);
          refresh();
        }}
        onCancel={user ? () => setShowLogin(false) : undefined}
      />
    );
  }

  return (
    <Shell
      user={user}
      onLogout={() => {
        if (user.anonymous) {
          setShowLogin(true);
          return;
        }
        clearToken();
        setLoading(true);
        refresh();
      }}
    />
  );
}

function Shell({ user, onLogout }: { user: ShellUser; onLogout: () => void }) {
  const location = useLocation();
  const navigate = useNavigate();
  const view = viewForPath(location.pathname) ?? "explorer";

  const setView = (v: View) => navigate(pathForView(v));

  const logout = () => {
    onLogout();
    navigate("/");
  };

  // Mirrors the nav visibility rules: anonymous callers have no API keys
  // page, non-admins no administration pages. The JSON API enforces the
  // same; this just avoids rendering a page of errors.
  const allowed =
    (ADMIN_VIEWS.includes(view) ? user.admin : true) &&
    (view === "apikeys" ? !user.anonymous : true);

  return (
    <AppShellLayout user={user} view={view} setView={setView} onLogout={logout}>
      {!allowed ? (
        <Navigate to="/" replace />
      ) : (
        <Routes>
          <Route path="/" element={<Explorer />} />
          <Route path="/explorer" element={<Explorer />} />
          <Route path="/apikeys" element={<ApiKeys anonymous={user.anonymous} />} />
          <Route path="/admin/users" element={<Users />} />
          <Route path="/admin/service-accounts" element={<ServiceAccounts />} />
          <Route path="/admin/stores" element={<Stores />} />
          <Route path="/admin/jobs" element={<Jobs />} />
          <Route path="/admin/webhooks" element={<Webhooks />} />
          <Route path="/admin/credentials" element={<Credentials />} />
          <Route path="/admin/roles" element={<Roles />} />
          <Route path="/admin/groups" element={<Groups />} />
          <Route path="/admin/grants" element={<Grants />} />
          <Route path="/admin/anonymous" element={<Anonymous />} />
          <Route path="/admin/registries" element={<Registries />} />
          <Route path="/admin/sso" element={<SSO />} />
          <Route path="/admin/settings" element={<Settings />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      )}
    </AppShellLayout>
  );
}
