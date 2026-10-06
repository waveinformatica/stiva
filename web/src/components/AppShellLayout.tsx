import { AppShell, Group, Title, Badge, Button, Text, NavLink } from "@mantine/core";
import {
  IconSearch,
  IconUsers,
  IconKey,
  IconCloud,
  IconShield,
  IconSettings,
  IconLogout,
} from "./icons";

export type View =
  | "explorer"
  | "apikeys"
  | "users"
  | "serviceAccounts"
  | "stores"
  | "credentials"
  | "roles"
  | "groups"
  | "grants"
  | "anonymous"
  | "registries"
  | "sso"
  | "settings";

export interface ShellUser {
  name: string;
  admin: boolean;
  anonymous: boolean;
}

export default function AppShellLayout({
  user,
  view,
  setView,
  onLogout,
  children,
}: {
  user: ShellUser;
  view: View;
  setView: (v: View) => void;
  onLogout: () => void;
  children: React.ReactNode;
}) {
  const link = (key: View, label: string, icon: React.ReactNode) => (
    <NavLink
      label={label}
      leftSection={icon}
      active={view === key}
      onClick={() => setView(key)}
    />
  );

  return (
    <AppShell
      header={{ height: 56 }}
      navbar={{ width: 240, breakpoint: "sm", collapsed: { mobile: true } }}
      padding="md"
    >
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group>
            <IconCloud size={22} />
            <Title order={4}>Registry</Title>
            <Badge variant="light" color="blue">
              OCI
            </Badge>
          </Group>
          <Group gap="xs">
            <Badge color={user.admin ? "grape" : "gray"}>
              {user.anonymous ? "anonymous" : user.name}
            </Badge>
            <Button
              size="xs"
              variant="default"
              leftSection={<IconLogout size={14} />}
              onClick={onLogout}
            >
              Logout
            </Button>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="xs">
        <Text size="xs" tt="uppercase" c="dimmed" fw={700} px="sm" pt="xs">
          Browse
        </Text>
        {link("explorer", "Explorer", <IconSearch size={16} />)}
        {!user.anonymous && link("apikeys", "API keys", <IconKey size={16} />)}

        {user.admin && (
          <>
            <Text size="xs" tt="uppercase" c="dimmed" fw={700} px="sm" pt="md">
              Administration
            </Text>
            {link("users", "Users", <IconUsers size={16} />)}
            {link("serviceAccounts", "Service Accounts", <IconKey size={16} />)}
            {link("registries", "Registries", <IconCloud size={16} />)}
            {link("stores", "Blob Stores", <IconCloud size={16} />)}
            {link("credentials", "Credentials", <IconKey size={16} />)}
            {link("roles", "Roles", <IconShield size={16} />)}
            {link("groups", "Groups", <IconUsers size={16} />)}
            {link("grants", "Grants", <IconShield size={16} />)}
            {link("anonymous", "Anonymous Access", <IconUsers size={16} />)}
            {link("sso", "Single sign-on", <IconKey size={16} />)}
            {link("settings", "Settings", <IconSettings size={16} />)}
          </>
        )}
      </AppShell.Navbar>

      <AppShell.Main>{children}</AppShell.Main>
    </AppShell>
  );
}
