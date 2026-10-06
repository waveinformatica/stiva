import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader,
  DataTable,
  Button,
  Modal,
  TextInput,
  Group,
  Text,
  Badge,
  Stack,
  Alert,
  Loader,
  ConfirmModal,
  ActionIcon,
  Code,
} from "../components/ui";
import { api } from "../lib/api";
import { IconPlus, IconTrash, IconKey } from "../components/icons";
import { GrantsEditor, type KeyGrant } from "../components/GrantsEditor";

interface KeyRow {
  username: string;
  label: string;
  masked: string;
  grants?: KeyGrant[];
}

export default function ApiKeys({ anonymous }: { anonymous?: boolean }) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<KeyRow[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [confirmDel, setConfirmDel] = useState<string | null>(null);
  const [label, setLabel] = useState("");
  const [grants, setGrants] = useState<KeyGrant[]>([]);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () => {
    setLoading(true);
    setErr(null);
    api
      .accountKeys()
      .then((r) => setRows(r.service_accounts || []))
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const submitCreate = async () => {
    setBusy(true);
    try {
      const r = await api.accountCreateKey({ label, grants });
      setCreatedKey(r.key);
      setCreateOpen(false);
      setLabel("");
      setGrants([]);
      load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const submitDelete = async () => {
    if (!confirmDel) return;
    try {
      await api.accountRevokeKey(confirmDel);
      setConfirmDel(null);
      load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  if (anonymous) {
    return (
      <div>
        <PageHeader title={t("apikeys.title")} />
        <Alert color="blue" title={t("apikeys.signinRequiredTitle")}>
          {t("apikeys.signinRequiredBody")}
        </Alert>
      </div>
    );
  }

  if (loading) return <Loader />;

  return (
    <div>
      <PageHeader
        title={t("apikeys.title")}
        actions={
          <Button leftSection={<IconPlus size={14} />} onClick={() => { setCreatedKey(null); setCreateOpen(true); }}>
            {t("apikeys.newKey")}
          </Button>
        }
      />
      {err && <Alert color="red" mb="md" title={t("common.error")}>{err}</Alert>}
      {createdKey && (
        <Alert color="teal" mb="md" title={t("apikeys.createdTitle")}>
          <Code block style={{ overflowWrap: "anywhere" }}>{createdKey}</Code>
        </Alert>
      )}
      <Text size="sm" c="dimmed" mb="md">
        {t("apikeys.intro")}
      </Text>
      <DataTable
        rowKey={(r) => r.masked}
        rows={rows}
        empty={t("apikeys.empty")}
        columns={[
          { header: t("apikeys.labelHeader"), render: (r) => r.label || "—" },
          { header: t("apikeys.keyHeader"), render: (r) => <Code>{r.masked}</Code> },
          {
            header: t("apikeys.restrictionsHeader"),
            render: (r) =>
              (r.grants || []).length === 0 ? (
                <Text size="xs" c="dimmed">{t("apikeys.fullAccess")}</Text>
              ) : (
                <Group gap={4}>
                  {(r.grants || []).map((g, i) => (
                    <Badge key={i} variant="light" size="sm">{g.role} · {g.scope}</Badge>
                  ))}
                </Group>
              ),
          },
          {
            header: "",
            width: 60,
            render: (r) => (
              <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(r.masked)}>
                <IconTrash size={16} />
              </ActionIcon>
            ),
          },
        ]}
      />

      <Modal opened={createOpen} onClose={() => setCreateOpen(false)} title={t("apikeys.newTitle")} centered size="lg">
        <Stack>
          {err && (
            <Alert color="red" title={t("apikeys.createFailed")}>
              {err}
            </Alert>
          )}
          <TextInput label={t("apikeys.labelLabel")} placeholder={t("apikeys.labelPlaceholder")} value={label} onChange={(e) => setLabel(e.currentTarget.value)} />
          <GrantsEditor value={grants} onChange={setGrants} />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setCreateOpen(false)}>{t("common.cancel")}</Button>
            <Button leftSection={<IconKey size={14} />} loading={busy} onClick={submitCreate}>{t("apikeys.generate")}</Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={confirmDel !== null}
        title={t("apikeys.revokeTitle")}
        message={t("apikeys.revokeMessage")}
        danger
        onConfirm={submitDelete}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
