import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, DataTable, Badge, Button, Modal, TextInput, PasswordInput, Switch,
  Group, Text, Stack, Alert, ConfirmModal, ActionIcon, Code, Loader,
} from "../../components/ui";
import { api } from "../../lib/api";
import { IconPlus, IconTrash, IconShield } from "../../components/icons";

interface UserRow {
  name: string;
  admin: boolean;
  disabled: boolean;
}

type Grant = { id: number; subject: string; role: string; scope: string };

export default function Users() {
  const { t } = useTranslation();
  const [users, setUsers] = useState<UserRow[]>([]);
  const [grants, setGrants] = useState<Grant[]>([]);
  const [groups, setGroups] = useState<{ name: string; members: string[] }[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [accessOpen, setAccessOpen] = useState<string | null>(null);
  const [confirmDel, setConfirmDel] = useState<string | null>(null);

  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [disabled, setDisabled] = useState(false);
  const [editTarget, setEditTarget] = useState("");

  const load = () => {
    setLoading(true);
    Promise.all([api.adminUsers(), api.grants(), api.groups()])
      .then(([u, g, gr]: any[]) => {
        setUsers(u.users || []);
        setGrants(g.grants || []);
        setGroups(gr.groups || []);
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  // What a user can actually do: grants made to them directly, to a group they
  // belong to, or to everyone signed in. This is the answer the old "roles"
  // dialog pretended to give while reading a table nothing consulted.
  const accessOf = (username: string) => {
    const mine = groups.filter((g) => (g.members || []).includes(username)).map((g) => "group:" + g.name);
    const subjects = new Set(["user:" + username, "authenticated", ...mine]);
    return grants.filter((g) => subjects.has(g.subject));
  };

  const create = async () => {
    setErr(null);
    try {
      await api.adminCreateUser({ name, password });
      setCreateOpen(false);
      setName("");
      setPassword("");
      load();
    } catch (e: any) {
      setErr(e.message);
    }
  };

  const openEdit = (u: UserRow) => {
    setEditTarget(u.name);
    setDisabled(u.disabled);
    setPassword("");
    setErr(null);
    setEditOpen(true);
  };

  const saveEdit = async () => {
    setErr(null);
    try {
      const body: any = { disabled };
      if (password) body.password = password;
      await api.adminUpdateUser(editTarget, body);
      setEditOpen(false);
      load();
    } catch (e: any) {
      setErr(e.message);
    }
  };

  const remove = async () => {
    if (!confirmDel) return;
    try {
      await api.adminDeleteUser(confirmDel);
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
        title={t("users.title")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={() => setCreateOpen(true)}>
            {t("users.newUser")}
          </Button>
        }
      />

      {err && (
        <Alert color="red" title={t("common.error")} mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        {t("users.intro")}
      </Text>

      <DataTable<UserRow>
        rows={users}
        rowKey={(u) => u.name}
        empty={t("users.empty")}
        columns={[
          { header: t("users.nameHeader"), render: (u) => <Text fw={500}>{u.name}</Text> },
          {
            header: t("users.groupsHeader"),
            render: (u) => {
              const mine = groups.filter((g) => (g.members || []).includes(u.name));
              return mine.length ? (
                <Group gap={4}>
                  {mine.map((g) => (
                    <Badge key={g.name} variant="light" size="sm" color="teal">
                      {g.name}
                    </Badge>
                  ))}
                </Group>
              ) : (
                <Text size="xs" c="dimmed">
                  {t("users.noGroups")}
                </Text>
              );
            },
          },
          {
            header: t("users.accessHeader"),
            render: (u) => {
              const a = accessOf(u.name);
              const admin = a.some((g) => g.role === "system:admin");
              return (
                <Group gap={4}>
                  {admin && <Badge color="grape">{t("users.administrator")}</Badge>}
                  <Badge variant="light">{t("users.grantCount", { count: a.length })}</Badge>
                </Group>
              );
            },
          },
          {
            header: t("users.statusHeader"),
            render: (u) =>
              u.disabled ? <Badge color="red">{t("users.disabled")}</Badge> : <Badge color="green">{t("users.active")}</Badge>,
            width: 110,
          },
          {
            header: "",
            width: 190,
            render: (u) => (
              <Group gap="xs" justify="flex-end">
                <Button
                  size="xs"
                  variant="default"
                  leftSection={<IconShield size={14} />}
                  onClick={() => setAccessOpen(u.name)}
                >
                  {t("users.access")}
                </Button>
                <Button size="xs" variant="default" onClick={() => openEdit(u)}>
                  {t("common.edit")}
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(u.name)}>
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ),
          },
        ]}
      />

      <Modal opened={createOpen} onClose={() => setCreateOpen(false)} title={t("users.newTitle")}>
        <Stack>
          <TextInput label={t("users.nameLabel")} value={name} onChange={(e) => setName(e.currentTarget.value)} />
          <PasswordInput
            label={t("users.passwordLabel")}
            value={password}
            onChange={(e) => setPassword(e.currentTarget.value)}
          />
          <Text size="xs" c="dimmed">
            {t("users.createHint")}
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setCreateOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button onClick={create}>{t("common.create")}</Button>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={editOpen} onClose={() => setEditOpen(false)} title={t("users.editTitle", { name: editTarget })}>
        <Stack>
          <PasswordInput
            label={t("users.newPasswordLabel")}
            placeholder={t("users.newPasswordPlaceholder")}
            value={password}
            onChange={(e) => setPassword(e.currentTarget.value)}
          />
          <Switch
            label={t("users.disabledLabel")}
            description={t("users.disabledDesc")}
            checked={disabled}
            onChange={(e) => setDisabled(e.currentTarget.checked)}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setEditOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button onClick={saveEdit}>{t("common.save")}</Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={!!accessOpen}
        onClose={() => setAccessOpen(null)}
        title={t("users.accessTitle", { name: accessOpen })}
        size="lg"
      >
        <Stack gap="sm">
          {accessOpen && accessOf(accessOpen).length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("users.noAccess")}
            </Text>
          ) : (
            accessOpen &&
            accessOf(accessOpen).map((g) => (
              <Group key={g.id} justify="space-between" wrap="nowrap">
                <Stack gap={0}>
                  <Text size="sm" fw={500}>
                    {g.role}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {g.subject === "user:" + accessOpen
                      ? t("users.grantedDirect")
                      : g.subject === "authenticated"
                        ? t("users.grantedEveryone")
                        : t("users.grantedThrough", { group: g.subject.replace("group:", "") })}
                  </Text>
                </Stack>
                <Code>{g.scope === "*" ? t("users.everywhere") : g.scope}</Code>
              </Group>
            ))
          )}
          <Text size="xs" c="dimmed">
            {t("users.grantsHint")}
          </Text>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("users.deleteTitle")}
        message={t("users.deleteMessage", { name: confirmDel })}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
