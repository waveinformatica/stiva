import { useEffect, useState } from "react";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  TextInput, MultiSelect, ActionIcon, DataTable, ConfirmModal,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { api } from "../../lib/api";

type Grp = { name: string; description: string; members: string[] };

export default function Groups() {
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
        title="Groups"
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            New group
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title="Error" mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        Grant a role to a group and it reaches every member. Identities arriving from LDAP or
        OIDC bring their own groups as claims; these are for local accounts, and both resolve
        the same way.
      </Text>

      <DataTable<Grp>
        rows={groups}
        rowKey={(g) => g.name}
        empty="No group yet."
        columns={[
          {
            header: "Name",
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
            header: "Members",
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
                  Edit
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(g)}>
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ),
          },
        ]}
      />

      <Modal opened={open} onClose={() => setOpen(false)} title={editName ? `Edit ${editName}` : "New group"}>
        <Stack>
          {err && (
            <Alert color="red" title="Could not save">
              {err}
            </Alert>
          )}
          <TextInput
            label="Name"
            placeholder="devs"
            value={name}
            disabled={!!editName}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <TextInput
            label="Description"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <MultiSelect
            label="Members"
            description="Local accounts. Federated identities join through their own group claims."
            data={users}
            value={members}
            onChange={setMembers}
            searchable
            clearable
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button loading={busy} onClick={save}>
              {editName ? "Save changes" : "Create group"}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title="Delete group"
        message={`Delete ${confirmDel?.name}? Grants made to it are removed too, so its members lose whatever they reached through it.`}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
