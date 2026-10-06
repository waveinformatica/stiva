import { AppShell, Burger, Group, Title, Badge, Button, Text, NavLink, Select } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { useTranslation } from "react-i18next";
import { LANGUAGES } from "../lib/i18n";
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
  const { t, i18n } = useTranslation();
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
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between" wrap="nowrap">
          <Group wrap="nowrap">
            <Burger opened={navOpened} onClick={toggleNav} hiddenFrom="sm" size="sm" aria-label={t("nav.navigation")} />
            <img src="/stiva-mark.png" alt="Stiva" style={{ height: 28, width: "auto" }} />
            <Title order={4}>Stiva</Title>
            <Badge variant="light" color="blue" visibleFrom="sm">
              OCI
            </Badge>
          </Group>
          <Group gap="xs">
            <Select
              size="xs"
              w={130}
              aria-label={t("nav.language")}
              data={LANGUAGES.map((l) => ({ value: l.code, label: l.label }))}
              value={i18n.language?.split("-")[0] || "en"}
              onChange={(v) => v && i18n.changeLanguage(v)}
              allowDeselect={false}
            />
            <Badge color={user.admin ? "grape" : "gray"}>
              {user.anonymous ? t("nav.anonymous") : user.name}
            </Badge>
            <Button
              size="xs"
              variant="default"
              leftSection={<IconLogout size={14} />}
              onClick={onLogout}
            >
              {t("nav.logout")}
            </Button>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="xs">
        <Text size="xs" tt="uppercase" c="dimmed" fw={700} px="sm" pt="xs">
          {t("nav.browse")}
        </Text>
        {link("explorer", t("nav.explorer"), <IconSearch size={16} />)}
        {!user.anonymous && link("apikeys", t("nav.apikeys"), <IconKey size={16} />)}

        {user.admin && (
          <>
            <Text size="xs" tt="uppercase" c="dimmed" fw={700} px="sm" pt="md">
              {t("nav.administration")}
            </Text>
            {link("users", t("nav.users"), <IconUsers size={16} />)}
            {link("serviceAccounts", t("nav.serviceAccounts"), <IconKey size={16} />)}
            {link("registries", t("nav.registries"), <IconCloud size={16} />)}
            {link("stores", t("nav.stores"), <IconCloud size={16} />)}
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

      <AppShell.Main>{children}</AppShell.Main>
    </AppShell>
  );
}
