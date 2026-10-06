import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, Card, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  ActionIcon, DataTable, ConfirmModal,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { BlobStoreForm, emptyStore, type BlobStore } from "../../components/BlobStoreForm";
import { api } from "../../lib/api";

type Row = BlobStore & { used_by?: string[] };

export default function Stores() {
  const { t } = useTranslation();
  const [rows, setRows] = useState<Row[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editName, setEditName] = useState<string | null>(null);
  const [form, setForm] = useState<BlobStore>(emptyStore());
  const [confirmDel, setConfirmDel] = useState<Row | null>(null);

  const load = () => {
    setLoading(true);
    api
      .blobStores()
      .then((r: any) => setRows(r.blob_stores || []))
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const openNew = () => {
    setEditName(null);
    setForm(emptyStore());
    setOpen(true);
  };

  const openEdit = (r: Row) => {
    setEditName(r.name);
    setForm(r);
    setOpen(true);
  };

  const remove = async () => {
    if (!confirmDel) return;
    try {
      await api.deleteBlobStore(confirmDel.name);
      setConfirmDel(null);
      load();
    } catch (e: any) {
      setErr(e.message);
      setConfirmDel(null);
    }
  };

  const summary = (r: Row) => {
    if (r.kind === "s3") return `${r.s3?.bucket || "?"}${r.s3?.endpoint ? " @ " + r.s3.endpoint : ""}`;
    if (r.kind === "file") return r.file?.root || "?";
    if (r.kind === "gcs") return r.gcs?.bucket || "?";
    if (r.kind === "azure") return r.azure?.container || "?";
    return "";
  };

  if (loading) return <Loader />;

  return (
    <div>
      <PageHeader
        title={t("stores.title")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            {t("stores.newStore")}
          </Button>
        }
      />

      {err && (
        <Alert color="red" title={t("common.error")} mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <DataTable<Row>
        rows={rows}
        rowKey={(r) => r.name}
        empty={t("stores.empty")}
        columns={[
          {
            header: t("stores.nameHeader"),
            render: (r) => (
              <Stack gap={0}>
                <Text fw={500}>{r.name}</Text>
                {r.description && (
                  <Text size="xs" c="dimmed">
                    {r.description}
                  </Text>
                )}
              </Stack>
            ),
          },
          { header: t("stores.typeHeader"), render: (r) => <Badge color="indigo">{r.kind}</Badge>, width: 110 },
          { header: t("stores.targetHeader"), render: (r) => <Text size="sm">{summary(r)}</Text> },
          {
            header: t("stores.credentialHeader"),
            render: (r) => {
              const ref = r.s3?.secret_key || r.azure?.connection_string || "";
              return ref ? (
                <Text size="xs">{ref.replace("vault://", "")}</Text>
              ) : (
                <Text size="xs" c="dimmed">
                  {t("stores.ambient")}
                </Text>
              );
            },
          },
          {
            header: t("stores.usedByHeader"),
            render: (r) =>
              r.used_by?.length ? (
                <Group gap={4}>
                  {r.used_by.map((u) => (
                    <Badge key={u} variant="light" size="sm">
                      {u}
                    </Badge>
                  ))}
                </Group>
              ) : (
                <Text size="xs" c="dimmed">
                  {t("stores.unused")}
                </Text>
              ),
          },
          {
            header: "",
            width: 100,
            render: (r) => (
              <Group gap="xs" justify="flex-end">
                <Button size="xs" variant="default" onClick={() => openEdit(r)}>
                  {t("common.edit")}
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(r)}>
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
        title={editName ? t("stores.editTitle", { name: editName }) : t("stores.newTitle")}
        size="lg"
      >
        <BlobStoreForm
          value={form}
          onChange={setForm}
          nameLocked={!!editName}
          submitLabel={editName ? t("common.saveChanges") : t("stores.createStore")}
          onSaved={() => {
            setOpen(false);
            load();
          }}
        />
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("stores.deleteTitle")}
        message={
          confirmDel?.used_by?.length
            ? t("stores.deleteUsedMessage", { name: confirmDel.name, users: confirmDel.used_by.join(", ") })
            : t("stores.deleteMessage", { name: confirmDel?.name })
        }
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />

      <Card withBorder mt="lg" padding="md">
        <Text size="sm" fw={500} mb={4}>
          {t("stores.credentialsTitle")}
        </Text>
        <Text size="xs" c="dimmed">
          {t("stores.credentialsBody")}
        </Text>
      </Card>
    </div>
  );
}
