import type { View } from "../components/AppShellLayout";

// Every page has a path route, and the server knows this same list (see
// uiPagePaths in cmd/registry/main.go): it serves index.html for exactly
// these paths, unauthenticated, ahead of the artifact dispatcher. Keep the
// two lists in sync — a page missing here is unreachable by URL, a path
// missing there 404s (or hits auth) on refresh and deep links.
export const VIEW_PATHS: Record<View, string> = {
  explorer: "/explorer",
  apikeys: "/apikeys",
  users: "/admin/users",
  serviceAccounts: "/admin/service-accounts",
  registries: "/admin/registries",
  stores: "/admin/stores",
  credentials: "/admin/credentials",
  roles: "/admin/roles",
  groups: "/admin/groups",
  grants: "/admin/grants",
  anonymous: "/admin/anonymous",
  sso: "/admin/sso",
  settings: "/admin/settings",
};

export const PATH_VIEWS: Record<string, View> = Object.fromEntries(
  Object.entries(VIEW_PATHS).map(([view, path]) => [path, view as View]),
);

export function pathForView(view: View): string {
  return VIEW_PATHS[view];
}

export function viewForPath(pathname: string): View | null {
  const clean = pathname.endsWith("/") && pathname !== "/" ? pathname.slice(0, -1) : pathname;
  if (clean === "/") return "explorer";
  return PATH_VIEWS[clean] ?? null;
}
