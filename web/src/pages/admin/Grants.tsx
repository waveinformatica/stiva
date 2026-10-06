import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  Select, ActionIcon, DataTable, ConfirmModal, Code,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { ScopeBuilder } from "../../components/ScopeBuilder";
import { api } from "../../lib/api";

type Grant = { id: number; subject: string; role: string; scope: string };

export default function Grants() {
  const { t } = useTranslation();
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
    if (s === "authenticated") return <Badge color="cyan">{t("grants.anySignedIn")}</Badge>;
    if (s.startsWith("group:")) return <Badge color="teal">{t("grants.groupBadge", { name: s.slice(6) })}</Badge>;
    return <Badge color="blue">{s.replace("user:", "")}</Badge>;
  };

  if (loading) return <Loader />;

  return (
    <div>
      <PageHeader
        title={t("grants.title")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            {t("grants.newGrant")}
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title={t("common.error")} mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        {t("grants.intro")}
      </Text>

      <DataTable<Grant>
        rows={grants}
        rowKey={(g) => String(g.id)}
        empty={t("grants.empty")}
        columns={[
          { header: t("grants.whoHeader"), render: (g) => subjectBadge(g.subject), width: 220 },
          { header: t("grants.roleHeader"), render: (g) => <Text size="sm">{g.role}</Text>, width: 180 },
          {
            header: t("grants.whereHeader"),
            render: (g) => (
              <Code>{g.scope === "*" ? t("grants.everywhere") : g.scope}</Code>
            ),
          },
          {
            header: "",
            width: 120,
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

      <Modal opened={open} onClose={() => setOpen(false)} title={editId === null ? t("grants.newTitle") : t("grants.editTitle")} size="lg">
        <Stack>
          {err && (
            <Alert color="red" title={t("common.saveFailed")}>
              {err}
            </Alert>
          )}
          <Group gap="sm" align="flex-end">
            <Select
              label={t("grants.whoLabel")}
              data={[
                { value: "user", label: t("grants.whoUser") },
                { value: "group", label: t("grants.whoGroup") },
                { value: "authenticated", label: t("grants.whoAuthenticated") },
                { value: "anonymous", label: t("grants.whoAnonymous") },
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
                label={kind === "user" ? t("grants.userLabel") : t("grants.groupLabel")}
                data={kind === "user" ? users : groups}
                value={who || null}
                onChange={(v) => setWho(v || "")}
                searchable
                style={{ flex: 1 }}
              />
            )}
          </Group>

          <Select
            label={t("grants.roleLabel")}
            data={roles}
            value={role || null}
            onChange={(v) => setRole(v || "")}
            searchable
          />

          <ScopeBuilder value={scope} onChange={setScope} />

          {kind === "anonymous" && (
            <Alert color="yellow" title={t("grants.anonTitle")}>
              {t("grants.anonBody")}
            </Alert>
          )}

          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={save} disabled={!role || ((kind === "user" || kind === "group") && !who)}>
              {editId === null ? t("grants.createGrant") : t("common.saveChanges")}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("grants.removeTitle")}
        message={t("grants.removeMessage", { role: confirmDel?.role, subject: confirmDel?.subject })}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
