import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Stack, Text, Checkbox, Group, Badge, Divider, Alert } from "./ui";
import { api } from "../lib/api";

export type PermissionInfo = { name: string; description: string; admin: boolean };

/**
 * PermissionPicker lists the permissions a role may hold, each with what it
 * actually allows.
 *
 * The vocabulary comes from the server rather than being typed in. Before, a
 * role's permissions were free text with no defined set, so anything could be
 * entered and nothing was ever matched — which is how the whole roles screen
 * came to have no effect at all.
 */
export function PermissionPicker({
  value,
  onChange,
  disabled,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const [perms, setPerms] = useState<PermissionInfo[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api
      .permissions()
      .then((r: any) => setPerms(r.permissions || []))
      .catch((e) => setErr(e.message));
  }, []);

  const toggle = (name: string) =>
    onChange(value.includes(name) ? value.filter((v) => v !== name) : [...value, name]);

  const section = (title: string, hint: string, items: PermissionInfo[]) =>
    items.length === 0 ? null : (
      <div>
        <Group gap="xs" mb={2}>
          <Text size="xs" tt="uppercase" fw={700} c="dimmed">
            {title}
          </Text>
        </Group>
        <Text size="xs" c="dimmed" mb="xs">
          {hint}
        </Text>
        <Stack gap={6}>
          {items.map((p) => (
            <Checkbox
              key={p.name}
              disabled={disabled}
              checked={value.includes(p.name)}
              onChange={() => toggle(p.name)}
              label={
                <div>
                  <Text size="sm" fw={500}>
                    {p.name}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {p.description}
                  </Text>
                </div>
              }
            />
          ))}
        </Stack>
      </div>
    );

  return (
    <Stack gap="sm">
      {err && (
        <Alert color="red" title={t("roles.permissionsTitle")}>
          {err}
        </Alert>
      )}
      {section(
        t("roles.registrySection"),
        t("roles.registryHint"),
        perms.filter((p) => !p.admin),
      )}
      <Divider />
      {section(
        t("roles.adminSection"),
        t("roles.adminHint"),
        perms.filter((p) => p.admin),
      )}
      {value.length > 0 && (
        <Group gap={4} mt={4}>
          {value.map((v) => (
            <Badge key={v} variant="light" size="sm">
              {v}
            </Badge>
          ))}
        </Group>
      )}
    </Stack>
  );
}
