import { useEffect, useState } from "react";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  Select, ActionIcon, DataTable, ConfirmModal, Code,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { ScopeBuilder } from "../../components/ScopeBuilder";
import { api } from "../../lib/api";

type Grant = { id: number; subject: string; role: string; scope: string };

export default function Grants() {
  const [grants, setGrants] = useState<Grant[]>([]);
  const [roles, setRoles] = useState<string[]>([]);
  const [users, setUsers] = useState<string[]>([]);
  const [groups, setGroups] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmDel, setConfirmDel] = useState<Grant | null>(null);
  const [editId, setEditId] = useState<number | null>(null);

  const [kind, setKind] = useState("user");
  const [who, setWho] = useState("");
  const [role, setRole] = useState("");
  const [scope, setScope] = useState("*");

  const load = () => {
    setLoading(true);
    Promise.all([api.grants(), api.roles(), api.adminUsers(), api.groups()])
      .then(([g, r, u, gr]: any[]) => {
        setGrants(g.grants || []);
        setRoles((r.roles || []).map((x: any) => x.name));
        setUsers((u.users || []).map((x: any) => x.name));
        setGroups((gr.groups || []).map((x: any) => x.name));
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const subject = () => (kind === "user" || kind === "group" ? `${kind}:${who}` : kind);

  const openNew = () => {
    setEditId(null);
    setKind("user");
    setWho("");
    setRole("");
    setScope("*");
    setErr(null);
    setOpen(true);
  };

  const openEdit = (g: Grant) => {
    setEditId(g.id);
    if (g.subject.startsWith("user:")) {
      setKind("user");
      setWho(g.subject.slice(5));
    } else if (g.subject.startsWith("group:")) {
      setKind("group");
      setWho(g.subject.slice(6));
    } else {
      setKind(g.subject);
      setWho("");
    }
    setRole(g.role);
    setScope(g.scope);
    setErr(null);
    setOpen(true);
  };

  const save = async () => {
    setErr(null);
    setBusy(true);
    try {
      if (editId === null) {
        await api.addGrant({ subject: subject(), role, scope });
      } else {
        await api.updateGrant(editId, { subject: subject(), role, scope });
      }
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
      await api.deleteGrant(confirmDel.id);
      setConfirmDel(null);
      load();
    } catch (e: any) {
      // The anti-lockout guard answers here: the change is refused and nothing
      // is removed, so the message is the whole story.
      setErr(e.message);
      setConfirmDel(null);
    }
  };

  const subjectBadge = (s: string) => {
    if (s === "anonymous") return <Badge color="gray">anonymous</Badge>;
    if (s === "authenticated") return <Badge color="cyan">any signed-in user</Badge>;
    if (s.startsWith("group:")) return <Badge color="teal">group {s.slice(6)}</Badge>;
    return <Badge color="blue">{s.replace("user:", "")}</Badge>;
  };

  if (loading) return <Loader />;

  return (
    <div>
      <PageHeader
        title="Grants"
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            New grant
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title="Error" mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        A grant is what makes a role take effect: it ties one to somebody, within a scope.
        Nothing is permitted without one.
      </Text>

      <DataTable<Grant>
        rows={grants}
        rowKey={(g) => String(g.id)}
        empty="No grant yet — nobody can do anything."
        columns={[
          { header: "Who", render: (g) => subjectBadge(g.subject), width: 220 },
          { header: "Role", render: (g) => <Text size="sm">{g.role}</Text>, width: 180 },
          {
            header: "Where",
            render: (g) => (
              <Code>{g.scope === "*" ? "* (everywhere)" : g.scope}</Code>
            ),
          },
          {
            header: "",
            width: 120,
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

      <Modal opened={open} onClose={() => setOpen(false)} title={editId === null ? "New grant" : "Edit grant"} size="lg">
        <Stack>
          {err && (
            <Alert color="red" title="Could not save">
              {err}
            </Alert>
          )}
          <Group gap="sm" align="flex-end">
            <Select
              label="Who"
              data={[
                { value: "user", label: "A user" },
                { value: "group", label: "A group" },
                { value: "authenticated", label: "Any signed-in user" },
                { value: "anonymous", label: "Anonymous callers" },
              ]}
              value={kind}
              onChange={(v) => {
                setKind(v || "user");
                setWho("");
              }}
              allowDeselect={false}
              w={210}
            />
            {(kind === "user" || kind === "group") && (
              <Select
                label={kind === "user" ? "User" : "Group"}
                data={kind === "user" ? users : groups}
                value={who || null}
                onChange={(v) => setWho(v || "")}
                searchable
                style={{ flex: 1 }}
              />
            )}
          </Group>

          <Select
            label="Role"
            data={roles}
            value={role || null}
            onChange={(v) => setRole(v || "")}
            searchable
          />

          <ScopeBuilder value={scope} onChange={setScope} />

          {kind === "anonymous" && (
            <Alert color="yellow" title="Anonymous callers">
              A role holding administrative permissions is refused here. Anonymous access is for
              reading, and typically for image pulls from inside the cluster.
            </Alert>
          )}

          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button loading={busy} onClick={save} disabled={!role || ((kind === "user" || kind === "group") && !who)}>
              {editId === null ? "Create grant" : "Save changes"}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title="Remove grant"
        message={`Remove ${confirmDel?.role} from ${confirmDel?.subject}? If it is the last one that leaves somebody able to administer, the change is refused and nothing is lost.`}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
