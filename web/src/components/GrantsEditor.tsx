import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Group, TextInput, Select, Button, ActionIcon, Stack, Text } from "./ui";
import { IconPlus, IconTrash } from "./icons";
import { api } from "../lib/api";

export type KeyGrant = { role: string; scope: string };

// GrantsEditor edits the (role, scope) pairs restricting an API key. An empty
// list means no restriction: the key carries its owner's full power. Every
// entry must still pass the owner's own grants at request time, so a key can
// never exceed its owner however it is scoped here.
export function GrantsEditor({ value, onChange }: { value: KeyGrant[]; onChange: (v: KeyGrant[]) => void }) {
  const { t } = useTranslation();
  const [roles, setRoles] = useState<{ name: string; description?: string }[]>([]);

  useEffect(() => {
    api
      .rolesCatalog()
      .then((r: any) => setRoles(r.roles || []))
      .catch(() => setRoles([]));
  }, []);

  const update = (i: number, g: KeyGrant) => {
    const next = [...value];
    next[i] = g;
    onChange(next);
  };

  return (
    <Stack gap="xs">
      <Text size="sm" fw={500}>
        {t("grantsEditor.title")} <Text span c="dimmed" fw={400}>{t("grantsEditor.optional")}</Text>
      </Text>
      {value.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("grantsEditor.empty")}
        </Text>
      ) : (
        value.map((g, i) => (
          <Group key={i} gap="sm" align="flex-end">
            <Select
              label={i === 0 ? t("grantsEditor.role") : undefined}
              data={roles.map((r) => ({ value: r.name, label: r.description ? `${r.name} — ${r.description}` : r.name }))}
              value={g.role}
              onChange={(v) => update(i, { ...g, role: v || "" })}
              allowDeselect={false}
              style={{ flex: 1 }}
            />
            <TextInput
              label={i === 0 ? t("grantsEditor.scope") : undefined}
              placeholder="docker:prod:team/**"
              value={g.scope}
              onChange={(e) => update(i, { ...g, scope: e.currentTarget.value })}
              style={{ flex: 2 }}
            />
            <ActionIcon color="red" variant="subtle" onClick={() => onChange(value.filter((_, idx) => idx !== i))}>
              <IconTrash size={14} />
            </ActionIcon>
          </Group>
        ))
      )}
      <Group>
        <Button
          size="xs"
          variant="light"
          leftSection={<IconPlus size={14} />}
          onClick={() => onChange([...value, { role: roles[0]?.name || "", scope: "" }])}
        >
          {t("grantsEditor.add")}
        </Button>
      </Group>
      <Text size="xs" c="dimmed">
        {t("grantsEditor.shapeIntro")} <Text span ff="monospace">format:registry:pattern</Text>, {t("grantsEditor.shapeExample")}{" "}
        <Text span ff="monospace">docker:prod:team/**</Text> {t("grantsEditor.shapeOr")} <Text span ff="monospace">*</Text> {t("grantsEditor.shapeAll")}
      </Text>
    </Stack>
  );
}
