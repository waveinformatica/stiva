import { useEffect, useState } from "react";
import { Loader } from "@mantine/core";
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
import { api, clearToken, getToken, MeResponse } from "./lib/api";

export default function App() {
  const [user, setUser] = useState<ShellUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [view, setView] = useState<View>("explorer");

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

  const logout = () => {
    clearToken();
    setUser(null);
    setView("explorer");
  };

  if (loading) return <Loader />;
  if (!user) return <Login onLogin={refresh} />;

  const renderPage = () => {
    switch (view) {
      case "explorer":
        return <Explorer />;
      case "apikeys":
        return <ApiKeys anonymous={user.anonymous} />;
      case "users":
        return <Users />;
      case "serviceAccounts":
        return <ServiceAccounts />;
      case "stores":
        return <Stores />;
      case "credentials":
        return <Credentials />;
      case "roles":
        return <Roles />;
      case "groups":
        return <Groups />;
      case "grants":
        return <Grants />;
      case "anonymous":
        return <Anonymous />;
      case "registries":
        return <Registries />;
      case "sso":
        return <SSO />;
      case "settings":
        return <Settings />;
    }
  };

  return (
    <AppShellLayout user={user} view={view} setView={setView} onLogout={logout}>
      {renderPage()}
    </AppShellLayout>
  );
}
