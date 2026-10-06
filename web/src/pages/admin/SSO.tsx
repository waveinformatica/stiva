import { useEffect, useState } from "react";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal, Switch,
  TextInput, PasswordInput, Select, ActionIcon, DataTable, ConfirmModal,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { api } from "../../lib/api";

type Preset = {
  key: string;
  label: string;
  kind: string;
  required: string[];
  fields: string[];
};

type Provider = {
  id: string;
  label: string;
  provider: string;
  kind: string;
  client_id: string;
  tenant: string;
  base_url: string;
  issuer: string;
  authorize_url: string;
  token_url: string;
  userinfo_url: string;
  scope: string;
  username_claim: string;
  groups_claim: string;
  audience: string;
  admin_group: string;
  enabled: boolean;
  has_secret: boolean;
};

const FIELD_LABEL: Record<string, string> = {
  client_id: "Client ID",
  secret: "Client secret",
  tenant: "Tenant",
  base_url: "Base URL",
  issuer: "Issuer",
  authorize_url: "Authorization URL",
  token_url: "Token URL",
  userinfo_url: "Userinfo URL",
  scope: "Scopes",
  username_claim: "Username claim",
  groups_claim: "Groups claim",
  audience: "Audience",
  admin_group: "Admin group",
};

const FIELD_DESC: Record<string, string> = {
  tenant: "Entra directory tenant. Never use common unless any Microsoft account may sign in.",
  base_url: "Self-hosted base (GitLab) or CAS server base.",
  scope: "Space-separated. Empty keeps the preset scopes.",
  username_claim: "Empty keeps the preset mapping.",
  groups_claim: "Empty keeps the preset mapping (or none).",
  audience: "Empty checks the client ID.",
  admin_group: "Members of this group (or Entra object ID) become admins.",
};

const REQUIRED_KEY: Record<string, string> = {
  client_id: "client_id",
  secret: "secret",
  tenant: "tenant",
  issuer: "issuer",
  authorize_url: "authorize",
  token_url: "token",
  userinfo_url: "userinfo",
  base_url: "base_url",
};

const emptyForm = (): Record<string, string> => ({
  id: "",
  label: "",
  provider: "microsoft",
  client_id: "",
  secret: "",
  tenant: "",
  base_url: "",
  issuer: "",
  authorize_url: "",
  token_url: "",
  userinfo_url: "",
  scope: "",
  username_claim: "",
  groups_claim: "",
  audience: "",
  admin_group: "",
});

export default function SSO() {
  const [presets, setPresets] = useState<Preset[]>([]);
  const [list, setList] = useState<Provider[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [form, setForm] = useState<Record<string, string>>(emptyForm());
  const [enabled, setEnabled] = useState(true);
  const [confirmDel, setConfirmDel] = useState<Provider | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () => {
    setLoading(true);
    Promise.all([api.ssoPresets(), api.ssoListProviders()])
      .then(([p, l]: any[]) => {
        setPresets(p.presets || []);
        setList(l.providers || []);
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const preset = presets.find((x) => x.key === form.provider);
  const isRequired = (field: string) => {
    if (!preset) return false;
    if (!preset.required.includes(REQUIRED_KEY[field] || field)) return false;
    if (field === "secret" && editId) {
      return !list.find((x) => x.id === editId)?.has_secret;
    }
    return true;
  };

  const set = (k: string) => (e: { currentTarget: { value: string } }) =>
    setForm({ ...form, [k]: e.currentTarget.value });

  const pickPreset = (key: string) => {
    // Switching presets clears preset-specific fields: the server fills
    // defaults for everything left empty, so a stale value from another
    // preset must never leak into the new one.
    const next = emptyForm();
    next.id = form.id;
    next.label = form.label;
    next.provider = key;
    setForm(next);
  };

  const openNew = () => {
    setEditId(null);
    setForm(emptyForm());
    setEnabled(true);
    setErr(null);
    setOpen(true);
  };

  const openEdit = (p: Provider) => {
    setEditId(p.id);
    setForm({
      ...emptyForm(),
      id: p.id,
      label: p.label,
      provider: p.provider,
      client_id: p.client_id || "",
      tenant: p.tenant || "",
      base_url: p.base_url || "",
      issuer: p.issuer || "",
      authorize_url: p.authorize_url || "",
      token_url: p.token_url || "",
      userinfo_url: p.userinfo_url || "",
      scope: p.scope || "",
      username_claim: p.username_claim || "",
      groups_claim: p.groups_claim || "",
      audience: p.audience || "",
      admin_group: p.admin_group || "",
    });
    setEnabled(p.enabled);
    setErr(null);
    setOpen(true);
  };

  const save = async () => {
    setErr(null);
    setBusy(true);
    try {
      const body: any = { ...form, enabled };
      delete body.secret;
      if (form.secret) body.client_secret = form.secret;
      if (editId) {
        await api.ssoUpdateProvider(editId, body);
      } else {
        await api.ssoCreateProvider(body);
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
      await api.ssoDeleteProvider(confirmDel.id);
      setConfirmDel(null);
      load();
    } catch (e: any) {
      setErr(e.message);
      setConfirmDel(null);
    }
  };

  const presetLabel = (key: string) => presets.find((x) => x.key === key)?.label || key;

  if (loading) return <Loader />;

  return (
    <div>
      <PageHeader
        title="Single sign-on"
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            New provider
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title="Error" mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        Browser login buttons for the sign-in page. Providers activate immediately —
        no restart. Client secrets are stored encrypted and can never be read back.
      </Text>

      <DataTable<Provider>
        rows={list}
        rowKey={(p) => p.id}
        empty="No provider yet. Add Microsoft 365, Google, GitHub, GitLab, LinkedIn, or a generic OIDC/OAuth2/CAS endpoint."
        columns={[
          {
            header: "Provider",
            render: (p) => (
              <Stack gap={0}>
                <Text fw={500}>{p.label || p.id}</Text>
                <Text size="xs" c="dimmed">
                  {p.id} · {presetLabel(p.provider)}
                </Text>
              </Stack>
            ),
          },
          {
            header: "Mechanism",
            render: (p) => <Badge variant="light">{p.kind}</Badge>,
          },
          {
            header: "Status",
            render: (p) => (
              <Group gap={4}>
                {p.enabled ? <Badge color="green">enabled</Badge> : <Badge color="gray">disabled</Badge>}
                {p.has_secret ? (
                  <Badge color="blue" variant="light">secret set</Badge>
                ) : (
                  <Badge color="yellow" variant="light">no secret</Badge>
                )}
              </Group>
            ),
          },
          {
            header: "",
            width: 110,
            render: (p) => (
              <Group gap="xs" justify="flex-end">
                <Button size="xs" variant="default" onClick={() => openEdit(p)}>
                  Edit
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(p)}>
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ),
          },
        ]}
      />

      <Modal opened={open} onClose={() => setOpen(false)} title={editId ? `Edit ${editId}` : "New provider"} size="lg">
        <Stack>
          {err && (
            <Alert color="red" title="Could not save">
              {err}
            </Alert>
          )}
          <Group gap="sm" grow>
            <TextInput
              label="ID"
              description="Unique handle in login URLs (a-z, 0-9, dashes)."
              placeholder="m365"
              value={form.id}
              disabled={!!editId}
              onChange={set("id")}
            />
            <TextInput
              label="Label"
              description="Button text on the sign-in page."
              placeholder="Microsoft 365"
              value={form.label}
              onChange={set("label")}
            />
          </Group>
          <Select
            label="Provider"
            data={presets.map((x) => ({ value: x.key, label: `${x.label} (${x.kind})` }))}
            value={form.provider}
            onChange={(v) => v && pickPreset(v)}
            allowDeselect={false}
          />
          {(preset?.fields || [])
            .filter((f) => f !== "admin_group")
            .map((f) =>
            f === "secret" ? (
              <PasswordInput
                key={f}
                label={FIELD_LABEL[f]}
                description={editId ? "Leave empty to keep the stored secret." : undefined}
                value={form.secret}
                onChange={set("secret")}
                required={isRequired(f)}
              />
            ) : (
              <TextInput
                key={f}
                label={FIELD_LABEL[f] || f}
                description={FIELD_DESC[f]}
                value={form[f] || ""}
                onChange={set(f)}
                required={isRequired(f)}
              />
            ),
          )}
          <TextInput
            label="Admin group"
            description={FIELD_DESC.admin_group}
            value={form.admin_group}
            onChange={set("admin_group")}
          />
          <Switch label="Enabled" checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button loading={busy} onClick={save}>
              {editId ? "Save changes" : "Create provider"}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title="Delete provider"
        message={`Delete ${confirmDel?.label || confirmDel?.id}? Its client secret is dropped too, and its login button disappears immediately.`}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
