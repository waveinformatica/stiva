import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  TextInput, ActionIcon, DataTable, ConfirmModal, Tooltip,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { PermissionPicker } from "../../components/PermissionPicker";
import { api } from "../../lib/api";

type Role = {
  name: string;
  description: string;
  permissions: string[];
  built_in: boolean;
};

export default function Roles() {
  const { t } = useTranslation();
  const [roles, setRoles] = useState<Role[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editName, setEditName] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [perms, setPerms] = useState<string[]>([]);
  const [confirmDel, setConfirmDel] = useState<Role | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () => {
    setLoading(true);
    api
      .roles()
      .then((r: any) => setRoles(r.roles || []))
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const openNew = () => {
    setEditName(null);
    setName("");
    setDescription("");
    setPerms([]);
    setErr(null);
    setOpen(true);
  };

  const openEdit = (r: Role) => {
    setEditName(r.name);
    setName(r.name);
    setDescription(r.description);
    setPerms(r.permissions || []);
    setErr(null);
    setOpen(true);
  };

  const save = async () => {
    setErr(null);
    setBusy(true);
    try {
      await api.upsertRole({ name, description, permissions: perms }, editName || undefined);
      setOpen(false);
      load();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!confirmDel) return;
    try {
      await api.deleteRole(confirmDel.name);
      setConfirmDel(null);
      load();
    } catch (e: any) {
      setErr(e.message);
      setConfirmDel(null);
    }
  };

  if (loading) return <Loader />;

  return (
    <div>
      <PageHeader
        title={t("roles.title")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            {t("roles.newRole")}
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title={t("common.error")} mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        {t("roles.intro")}
      </Text>

      <DataTable<Role>
        rows={roles}
        rowKey={(r) => r.name}
        empty={t("roles.empty")}
        columns={[
          {
            header: t("roles.nameHeader"),
            render: (r) => (
              <Stack gap={0}>
                <Group gap="xs">
                  <Text fw={500}>{r.name}</Text>
                  {r.built_in && (
                    <Tooltip label={t("roles.builtInTip")}>
                      <Badge size="sm" color="grape">
                        {t("roles.builtIn")}
                      </Badge>
                    </Tooltip>
                  )}
                </Group>
                {r.description && (
                  <Text size="xs" c="dimmed">
                    {r.description}
                  </Text>
                )}
              </Stack>
            ),
          },
          {
            header: t("roles.permissionsHeader"),
            render: (r) => (
              <Group gap={4}>
                {(r.permissions || []).map((p) => (
                  <Badge key={p} variant="light" size="sm" color={p.startsWith("admin:") ? "orange" : "blue"}>
                    {p}
                  </Badge>
                ))}
              </Group>
            ),
          },
          {
            header: "",
            width: 110,
            render: (r) => (
              <Group gap="xs" justify="flex-end">
                <Button size="xs" variant="default" onClick={() => openEdit(r)} disabled={r.built_in}>
                  {t("common.edit")}
                </Button>
                <ActionIcon
                  color="red"
                  variant="subtle"
                  disabled={r.built_in}
                  onClick={() => setConfirmDel(r)}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ),
          },
        ]}
      />

      <Modal
        opened={open}
        onClose={() => setOpen(false)}
        title={editName ? t("roles.editTitle", { name: editName }) : t("roles.newTitle")}
        size="lg"
      >
        <Stack>
          {err && (
            <Alert color="red" title={t("common.saveFailed")}>
              {err}
            </Alert>
          )}
          <TextInput
            label={t("roles.nameLabel")}
            placeholder={t("roles.namePlaceholder")}
            value={name}
            disabled={!!editName}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <TextInput
            label={t("roles.descriptionLabel")}
            placeholder={t("roles.descriptionPlaceholder")}
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <PermissionPicker value={perms} onChange={setPerms} />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={save}>
              {editName ? t("common.saveChanges") : t("roles.createRole")}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("roles.deleteTitle")}
        message={t("roles.deleteMessage", { name: confirmDel?.name })}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
