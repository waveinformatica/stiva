import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  TextInput, MultiSelect, ActionIcon, DataTable, ConfirmModal,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { api } from "../../lib/api";

type Grp = { name: string; description: string; members: string[] };

export default function Groups() {
  const { t } = useTranslation();
  const [groups, setGroups] = useState<Grp[]>([]);
  const [users, setUsers] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editName, setEditName] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [members, setMembers] = useState<string[]>([]);
  const [confirmDel, setConfirmDel] = useState<Grp | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () => {
    setLoading(true);
    Promise.all([api.groups(), api.adminUsers()])
      .then(([g, u]: any[]) => {
        setGroups(g.groups || []);
        setUsers((u.users || []).map((x: any) => x.name));
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const openNew = () => {
    setEditName(null);
    setName("");
    setDescription("");
    setMembers([]);
    setErr(null);
    setOpen(true);
  };

  const openEdit = (g: Grp) => {
    setEditName(g.name);
    setName(g.name);
    setDescription(g.description);
    setMembers(g.members || []);
    setErr(null);
    setOpen(true);
  };

  const save = async () => {
    setErr(null);
    setBusy(true);
    try {
      await api.upsertGroup({ name, description, members }, editName || undefined);
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
      await api.deleteGroup(confirmDel.name);
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
        title={t("groups.title")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            {t("groups.newGroup")}
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title={t("common.error")} mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        {t("groups.intro")}
      </Text>

      <DataTable<Grp>
        rows={groups}
        rowKey={(g) => g.name}
        empty={t("groups.empty")}
        columns={[
          {
            header: t("groups.nameHeader"),
            render: (g) => (
              <Stack gap={0}>
                <Text fw={500}>{g.name}</Text>
                {g.description && (
                  <Text size="xs" c="dimmed">
                    {g.description}
                  </Text>
                )}
              </Stack>
            ),
          },
          {
            header: t("groups.membersHeader"),
            render: (g) =>
              g.members?.length ? (
                <Group gap={4}>
                  {g.members.map((m) => (
                    <Badge key={m} variant="light" size="sm">
                      {m}
                    </Badge>
                  ))}
                </Group>
              ) : (
                <Text size="xs" c="dimmed">
                  empty — a grant to it reaches nobody
                </Text>
              ),
          },
          {
            header: "",
            width: 110,
            render: (g) => (
              <Group gap="xs" justify="flex-end">
                <Button size="xs" variant="default" onClick={() => openEdit(g)}>
                  {t("common.edit")}
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(g)}>
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ),
          },
        ]}
      />

      <Modal opened={open} onClose={() => setOpen(false)} title={editName ? t("groups.editTitle", { name: editName }) : t("groups.newTitle")}>
        <Stack>
          {err && (
            <Alert color="red" title={t("common.saveFailed")}>
              {err}
            </Alert>
          )}
          <TextInput
            label={t("groups.nameLabel")}
            placeholder={t("groups.namePlaceholder")}
            value={name}
            disabled={!!editName}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <TextInput
            label={t("groups.descriptionLabel")}
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <MultiSelect
            label={t("groups.membersLabel")}
            description={t("groups.membersDesc")}
            data={users}
            value={members}
            onChange={setMembers}
            searchable
            clearable
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={save}>
              {editName ? t("common.saveChanges") : t("groups.createGroup")}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("groups.deleteTitle")}
        message={t("groups.deleteMessage", { name: confirmDel?.name })}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
