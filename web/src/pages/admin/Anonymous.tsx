import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  TextInput, Textarea, Switch, ActionIcon, DataTable, ConfirmModal, Code,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { api } from "../../lib/api";

type Identity = {
  name: string;
  description: string;
  disabled: boolean;
  cidrs: string[] | null;
};

export default function Anonymous() {
  const { t } = useTranslation();
  const [rows, setRows] = useState<Identity[]>([]);
  const [trustedProxies, setTrustedProxies] = useState(0);
  const [grantCount, setGrantCount] = useState<Record<string, number>>({});
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editName, setEditName] = useState<string | null>(null);
  const [confirmDel, setConfirmDel] = useState<Identity | null>(null);
  const [busy, setBusy] = useState(false);

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [cidrText, setCidrText] = useState("");
  const [disabled, setDisabled] = useState(false);

  const load = () => {
    setLoading(true);
    Promise.all([api.anonymous(), api.grants()])
      .then(([a, g]: any[]) => {
        setRows(a.identities || []);
        setTrustedProxies(a.trusted_proxies || 0);
        const counts: Record<string, number> = {};
        for (const gr of g.grants || []) {
          if (gr.subject === "anonymous") counts["*any*"] = (counts["*any*"] || 0) + 1;
          if (gr.subject.startsWith("user:"))
            counts[gr.subject.slice(5)] = (counts[gr.subject.slice(5)] || 0) + 1;
        }
        setGrantCount(counts);
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const openNew = () => {
    setEditName(null);
    setName("");
    setDescription("");
    setCidrText("");
    setDisabled(false);
    setErr(null);
    setOpen(true);
  };

  const openEdit = (i: Identity) => {
    setEditName(i.name);
    setName(i.name);
    setDescription(i.description);
    setCidrText((i.cidrs || []).join("\n"));
    setDisabled(i.disabled);
    setErr(null);
    setOpen(true);
  };

  const save = async () => {
    setErr(null);
    setBusy(true);
    try {
      const cidrs = cidrText
        .split(/[\n,]/)
        .map((s) => s.trim())
        .filter(Boolean);
      await api.upsertAnonymous({ name, description, disabled, cidrs }, editName || undefined);
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
      await api.deleteAnonymous(confirmDel.name);
      setConfirmDel(null);
      load();
    } catch (e: any) {
      setErr(e.message);
      setConfirmDel(null);
    }
  };

  if (loading) return <Loader />;

  const catchAlls = rows.filter((r) => !(r.cidrs || []).length && !r.disabled);

  return (
    <div>
      <PageHeader
        title={t("anonymous.title")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            {t("anonymous.newIdentity")}
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title={t("common.error")} mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        {t("anonymous.intro")}
      </Text>

      {trustedProxies === 0 && rows.some((r) => (r.cidrs || []).length > 0) && (
        <Alert color="orange" title={t("anonymous.proxyTitle")} mb="md">
          {t("anonymous.proxyBodyPre")} <Code>REGISTRY_TRUSTED_PROXIES</Code> {t("anonymous.proxyBodyPost")}
        </Alert>
      )}

      {catchAlls.length > 1 && (
        <Alert color="yellow" title={t("anonymous.catchAllTitle")} mb="md">
          {t("anonymous.catchAllBody", { names: catchAlls.map((c) => c.name).join(", ") })}
        </Alert>
      )}

      <DataTable<Identity>
        rows={rows}
        rowKey={(i) => i.name}
        empty={t("anonymous.empty")}
        columns={[
          {
            header: t("anonymous.identityHeader"),
            render: (i) => (
              <Stack gap={0}>
                <Group gap="xs">
                  <Text fw={500}>{i.name}</Text>
                  {i.disabled && <Badge color="red" size="sm">{t("anonymous.disabledBadge")}</Badge>}
                </Group>
                {i.description && (
                  <Text size="xs" c="dimmed">
                    {i.description}
                  </Text>
                )}
              </Stack>
            ),
          },
          {
            header: t("anonymous.recognisedHeader"),
            render: (i) =>
              (i.cidrs || []).length ? (
                <Group gap={4}>
                  {(i.cidrs || []).map((c) => (
                    <Code key={c}>{c}</Code>
                  ))}
                </Group>
              ) : (
                <Badge color="orange" variant="light">
                  {t("anonymous.anyAddress")}
                </Badge>
              ),
          },
          {
            header: t("anonymous.grantsHeader"),
            width: 120,
            render: (i) => {
              const own = grantCount[i.name] || 0;
              const shared = grantCount["*any*"] || 0;
              return (
                <Text size="xs" c={own + shared ? undefined : "dimmed"}>
                  {t("anonymous.ownCount", { count: own })}
                  {shared > 0 ? ` + ${t("anonymous.sharedCount", { count: shared })}` : ""}
                </Text>
              );
            },
          },
          {
            header: "",
            width: 120,
            render: (i) => (
              <Group gap="xs" justify="flex-end">
                <Button size="xs" variant="default" onClick={() => openEdit(i)}>
                  {t("common.edit")}
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(i)}>
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
        title={editName ? t("anonymous.editTitle", { name: editName }) : t("anonymous.newTitle")}
        size="lg"
      >
        <Stack>
          {err && (
            <Alert color="red" title={t("common.saveFailed")}>
              {err}
            </Alert>
          )}
          <TextInput
            label={t("anonymous.nameLabel")}
            placeholder={t("anonymous.namePlaceholder")}
            description={t("anonymous.nameDesc")}
            value={name}
            disabled={!!editName}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <TextInput
            label={t("anonymous.descriptionLabel")}
            placeholder={t("anonymous.descriptionPlaceholder")}
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <Textarea
            label={t("anonymous.addressesLabel")}
            description={t("anonymous.addressesDesc")}
            placeholder={"172.16.0.0/16\n192.168.2.0/24"}
            autosize
            minRows={3}
            value={cidrText}
            onChange={(e) => setCidrText(e.currentTarget.value)}
          />
          <Switch
            label={t("anonymous.disabledLabel")}
            description={t("anonymous.disabledDesc")}
            checked={disabled}
            onChange={(e) => setDisabled(e.currentTarget.checked)}
          />
          <Text size="xs" c="dimmed">
            {t("anonymous.createHint")}
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button loading={busy} onClick={save}>
              {editName ? t("common.saveChanges") : t("anonymous.createIdentity")}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("anonymous.deleteTitle")}
        message={t("anonymous.deleteMessage", { name: confirmDel?.name })}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
