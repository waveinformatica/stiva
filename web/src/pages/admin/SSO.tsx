import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal, Switch,
  TextInput, Textarea, PasswordInput, Select, ActionIcon, DataTable, ConfirmModal,
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
  entity_id?: string;
  idp_sso_url?: string;
  idp_cert?: string;
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
  entity_id: "Entity ID",
  idp_sso_url: "IdP single sign-on URL",
  idp_cert: "IdP signing certificate",
};

const FIELD_DESC: Record<string, string> = {
  tenant: "sso.fieldDesc.tenant",
  base_url: "sso.fieldDesc.base_url",
  scope: "sso.fieldDesc.scope",
  username_claim: "sso.fieldDesc.username_claim",
  groups_claim: "sso.fieldDesc.groups_claim",
  audience: "sso.fieldDesc.audience",
  admin_group: "sso.fieldDesc.admin_group",
  entity_id: "sso.fieldDesc.entity_id",
  idp_sso_url: "sso.fieldDesc.idp_sso_url",
  idp_cert: "sso.fieldDesc.idp_cert",
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
  entity_id: "",
  idp_sso_url: "",
  idp_cert: "",
});

export default function SSO() {
  const { t } = useTranslation();
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
      entity_id: p.entity_id || "",
      idp_sso_url: p.idp_sso_url || "",
      idp_cert: p.idp_cert || "",
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
        title={t("sso.title")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            {t("sso.newProvider")}
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title={t("common.error")} mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        {t("sso.intro")}
      </Text>

      <DataTable<Provider>
        rows={list}
        rowKey={(p) => p.id}
        empty={t("sso.empty")}
        columns={[
          {
            header: t("sso.providerHeader"),
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
            header: t("sso.mechanismHeader"),
            render: (p) => <Badge variant="light">{p.kind}</Badge>,
          },
          {
            header: t("sso.statusHeader"),
            render: (p) => (
              <Group gap={4}>
                {p.enabled ? <Badge color="green">{t("sso.enabled")}</Badge> : <Badge color="gray">{t("sso.disabled")}</Badge>}
                {p.has_secret ? (
                  <Badge color="blue" variant="light">{t("sso.secretSet")}</Badge>
                ) : (
                  <Badge color="yellow" variant="light">{t("sso.noSecret")}</Badge>
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
                  {t("common.edit")}
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(p)}>
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ),
          },
        ]}
      />

      <Modal opened={open} onClose={() => setOpen(false)} title={editId ? t("sso.editTitle", { id: editId }) : t("sso.newTitle")} size="lg">
        <Stack>
          {err && (
            <Alert color="red" title={t("common.saveFailed")}>
              {err}
            </Alert>
          )}
          <Group gap="sm" grow>
            <TextInput
              label={t("sso.idLabel")}
              description={t("sso.idDesc")}
              placeholder={t("sso.idPlaceholder")}
              value={form.id}
              disabled={!!editId}
              onChange={set("id")}
            />
            <TextInput
              label={t("sso.labelLabel")}
              description={t("sso.labelDesc")}
              placeholder={t("sso.labelPlaceholder")}
              value={form.label}
              onChange={set("label")}
            />
          </Group>
          <Select
            label={t("sso.providerLabel")}
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
                label={t("sso.field." + f, FIELD_LABEL[f])}
                description={editId ? t("sso.keepSecret") : undefined}
                value={form.secret}
                onChange={set("secret")}
                required={isRequired(f)}
              />
            ) : f === "idp_cert" ? (
              <Textarea
                key={f}
                label={t("sso.field." + f, FIELD_LABEL[f])}
                description={t(FIELD_DESC[f])}
                placeholder="-----BEGIN CERTIFICATE-----"
                value={form[f] || ""}
                onChange={(e) => setForm({ ...form, [f]: e.currentTarget.value })}
                required={isRequired(f)}
                autosize
                minRows={4}
              />
            ) : (
              <TextInput
                key={f}
                label={FIELD_LABEL[f] || f}
                description={t(FIELD_DESC[f])}
                value={form[f] || ""}
                onChange={set(f)}
                required={isRequired(f)}
              />
            ),
          )}
          <TextInput
            label={t("sso.adminGroupLabel")}
            description={t("sso.fieldDesc.admin_group")}
            value={form.admin_group}
            onChange={set("admin_group")}
          />
          <Switch label={t("sso.enabledLabel")} checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={save}>
              {editId ? t("common.saveChanges") : t("sso.createProvider")}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("sso.deleteTitle")}
        message={t("sso.deleteMessage", { name: confirmDel?.label || confirmDel?.id })}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
