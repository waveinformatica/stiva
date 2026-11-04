import { Avatar, Badge, Box, Group, Menu, Text, UnstyledButton, useMantineColorScheme } from "@mantine/core";
import { useTranslation } from "react-i18next";
import { LANGUAGES } from "../lib/i18n";
import { IconKey, IconLogout, IconSettings } from "./icons";
import type { ShellUser, View } from "./AppShellLayout";

const SCHEMES = ["light", "dark", "auto"] as const;

function initials(name: string) {
  const parts = name.split(/[\s._@-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "?") + (parts.length > 1 ? parts[1][0] : "")).toUpperCase();
}

// Account menu in the header: identity, role, shortcuts, language, theme, sign out.
export default function UserMenu({
  user,
  setView,
  onLogout,
}: {
  user: ShellUser;
  setView: (v: View) => void;
  onLogout: () => void;
}) {
  const { t, i18n } = useTranslation();
  const { colorScheme, setColorScheme } = useMantineColorScheme();
  const lang = i18n.language?.split("-")[0] || "en";
  const display = user.anonymous ? t("nav.anonymous") : user.name;
  const roleLabel = user.admin ? t("nav.roleAdmin") : t("nav.roleUser");

  return (
    <Menu width={260} position="bottom-end" shadow="md" withinPortal>
      <Menu.Target>
        <UnstyledButton aria-label={t("nav.account")}>
          <Group gap="xs" wrap="nowrap">
            <Avatar color={user.admin ? "grape" : "indigo"} radius="xl" size={32}>
              {user.anonymous ? "?" : initials(user.name)}
            </Avatar>
            <Box visibleFrom="sm" style={{ lineHeight: 1.15 }}>
              <Text size="sm" fw={600} maw={160} truncate>
                {display}
              </Text>
              <Text size="xs" c="dimmed">
                {roleLabel}
              </Text>
            </Box>
          </Group>
        </UnstyledButton>
      </Menu.Target>

      <Menu.Dropdown>
        <Box px="sm" py={6}>
          <Text size="xs" c="dimmed">
            {t("nav.signedInAs")}
          </Text>
          <Group gap="xs" wrap="nowrap" mt={2}>
            <Text size="sm" fw={600} truncate>
              {display}
            </Text>
            <Badge size="xs" color={user.admin ? "grape" : "gray"}>
              {roleLabel}
            </Badge>
          </Group>
        </Box>
        <Menu.Divider />

        {!user.anonymous && (
          <Menu.Item leftSection={<IconKey size={16} />} onClick={() => setView("apikeys")}>
            {t("nav.apikeys")}
          </Menu.Item>
        )}
        {user.admin && (
          <Menu.Item leftSection={<IconSettings size={16} />} onClick={() => setView("settings")}>
            {t("nav.settings")}
          </Menu.Item>
        )}

        <Menu.Divider />
        <Menu.Label>{t("nav.language")}</Menu.Label>
        {LANGUAGES.map((l) => (
          <Menu.Item
            key={l.code}
            closeMenuOnClick={false}
            onClick={() => i18n.changeLanguage(l.code)}
            rightSection={lang === l.code ? <Text size="xs">✓</Text> : null}
            fw={lang === l.code ? 600 : 400}
          >
            {l.label}
          </Menu.Item>
        ))}

        <Menu.Divider />
        <Menu.Label>{t("nav.theme")}</Menu.Label>
        {SCHEMES.map((s) => (
          <Menu.Item
            key={s}
            closeMenuOnClick={false}
            onClick={() => setColorScheme(s)}
            rightSection={colorScheme === s ? <Text size="xs">✓</Text> : null}
            fw={colorScheme === s ? 600 : 400}
          >
            {t(`nav.theme${s === "auto" ? "Auto" : s === "dark" ? "Dark" : "Light"}`)}
          </Menu.Item>
        ))}

        <Menu.Divider />
        <Menu.Item color="red" leftSection={<IconLogout size={16} />} onClick={onLogout}>
          {user.anonymous ? t("nav.signIn") : t("nav.logout")}
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  );
}
