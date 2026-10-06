import { useEffect, useState } from "react";
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
        title="Roles"
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            New role
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title="Error" mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        A role is a set of permissions. It grants nothing on its own — it takes effect when a
        grant ties it to someone within a scope.
      </Text>

      <DataTable<Role>
        rows={roles}
        rowKey={(r) => r.name}
        empty="No role yet."
        columns={[
          {
            header: "Name",
            render: (r) => (
              <Stack gap={0}>
                <Group gap="xs">
                  <Text fw={500}>{r.name}</Text>
                  {r.built_in && (
                    <Tooltip label="Built in: it holds every permission, including ones added in future releases, and cannot be changed or deleted.">
                      <Badge size="sm" color="grape">
                        built-in
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
            header: "Permissions",
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
                  Edit
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
        title={editName ? `Edit ${editName}` : "New role"}
        size="lg"
      >
        <Stack>
          {err && (
            <Alert color="red" title="Could not save">
              {err}
            </Alert>
          )}
          <TextInput
            label="Name"
            placeholder="kosmos-dev"
            value={name}
            disabled={!!editName}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <TextInput
            label="Description"
            placeholder="What someone holding this role is meant to do"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <PermissionPicker value={perms} onChange={setPerms} />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button loading={busy} onClick={save}>
              {editName ? "Save changes" : "Create role"}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title="Delete role"
        message={`Delete ${confirmDel?.name}? Every grant using it is removed too.`}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
