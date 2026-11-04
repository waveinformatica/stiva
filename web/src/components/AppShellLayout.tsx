import { AppShell, Burger, Button, Group, Title, Text, Tooltip, NavLink } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { useTranslation } from "react-i18next";
import UserMenu from "./UserMenu";
import {
  IconSearch,
  IconUsers,
  IconKey,
  IconCloud,
  IconShield,
  IconSettings,
  IconClock,
  IconBell,
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
  | "jobs"
  | "webhooks"
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
  const { t } = useTranslation();
  const link = (key: View, label: string, icon: React.ReactNode) => (
    <NavLink
      label={label}
      leftSection={icon}
      active={view === key}
      onClick={() => {
        setView(key);
        closeNav();
      }}
    />
  );
  const [navOpened, { toggle: toggleNav, close: closeNav }] = useDisclosure(false);

  return (
    <AppShell
      header={{ height: 56 }}
      navbar={{ width: 240, breakpoint: "sm", collapsed: { mobile: !navOpened } }}
      padding="md"
    >
      <AppShell.Header className="stiva-header">
        <Group h="100%" px="md" justify="space-between" wrap="nowrap">
          <Group wrap="nowrap">
            <Burger opened={navOpened} onClick={toggleNav} hiddenFrom="sm" size="sm" aria-label={t("nav.navigation")} />
            <img src="/stiva-mark.png" alt="Stiva" style={{ height: 28, width: "auto" }} />
            <Title order={4} fw={700}>Stiva</Title>
          </Group>
          <Group gap="sm" wrap="nowrap">
            {user.anonymous && (
              <Tooltip label={t("login.anonymousHint")} multiline w={260}>
                <Button size="xs" onClick={onLogout}>
                  {t("nav.signIn")}
                </Button>
              </Tooltip>
            )}
            <UserMenu user={user} setView={setView} onLogout={onLogout} />
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="sm">
        <Text size="xs" tt="uppercase" c="dimmed" fw={700} lts={0.5} px="sm" pt="xs" pb={4}>
          {t("nav.browse")}
        </Text>
        {link("explorer", t("nav.explorer"), <IconSearch size={16} />)}
        {!user.anonymous && link("apikeys", t("nav.apikeys"), <IconKey size={16} />)}

        {user.admin && (
          <>
            <Text size="xs" tt="uppercase" c="dimmed" fw={700} lts={0.5} px="sm" pt="lg" pb={4}>
              {t("nav.administration")}
            </Text>
            {link("users", t("nav.users"), <IconUsers size={16} />)}
            {link("serviceAccounts", t("nav.serviceAccounts"), <IconKey size={16} />)}
            {link("registries", t("nav.registries"), <IconCloud size={16} />)}
            {link("stores", t("nav.stores"), <IconCloud size={16} />)}
            {link("jobs", t("nav.jobs"), <IconClock size={16} />)}
            {link("webhooks", t("nav.webhooks"), <IconBell size={16} />)}
            {link("credentials", t("nav.credentials"), <IconKey size={16} />)}
            {link("roles", t("nav.roles"), <IconShield size={16} />)}
            {link("groups", t("nav.groups"), <IconUsers size={16} />)}
            {link("grants", t("nav.grants"), <IconShield size={16} />)}
            {link("anonymous", t("nav.anonymousAccess"), <IconUsers size={16} />)}
            {link("sso", t("nav.sso"), <IconKey size={16} />)}
            {link("settings", t("nav.settings"), <IconSettings size={16} />)}
          </>
        )}
      </AppShell.Navbar>

      <AppShell.Main className="stiva-main" bg="var(--mantine-color-body)">
        <div className="stiva-content">{children}</div>
      </AppShell.Main>
    </AppShell>
  );
}
