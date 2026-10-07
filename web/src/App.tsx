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
import SSO from "./pages/admin/SSO";
import Settings from "./pages/admin/Settings";
import AppShellLayout, { View, ShellUser } from "./components/AppShellLayout";
import { pathForView, viewForPath } from "./lib/routes";
import { api, clearToken, getToken, MeResponse } from "./lib/api";

const ADMIN_VIEWS: View[] = [
  "users",
  "serviceAccounts",
  "registries",
  "stores",
  "credentials",
  "roles",
  "groups",
  "grants",
  "anonymous",
  "sso",
  "settings",
];

export default function App() {
  const [user, setUser] = useState<ShellUser | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = () => {
    const t = getToken();
    if (!t) {
      setUser(null);
      setLoading(false);
      return;
    }
    api
      .me()
      .then((m: MeResponse) => setUser(m.user))
      .catch(() => {
        clearToken();
        setUser(null);
      })
      .finally(() => setLoading(false));
  };

  useEffect(refresh, []);

  if (loading) return <Loader />;
  if (!user) return <Login onLogin={refresh} />;

  return <Shell user={user} onLogout={() => {
    clearToken();
    setUser(null);
  }} />;
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
