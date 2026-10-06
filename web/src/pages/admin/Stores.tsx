import { useEffect, useState } from "react";
import {
  PageHeader, Card, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  ActionIcon, DataTable, ConfirmModal,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { BlobStoreForm, emptyStore, type BlobStore } from "../../components/BlobStoreForm";
import { api } from "../../lib/api";

type Row = BlobStore & { used_by?: string[] };

export default function Stores() {
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
        title="Blob stores"
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            New store
          </Button>
        }
      />

      {err && (
        <Alert color="red" title="Error" mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <DataTable<Row>
        rows={rows}
        rowKey={(r) => r.name}
        empty="No store yet. Create one before adding a registry — a registry needs somewhere to put its blobs."
        columns={[
          {
            header: "Name",
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
          { header: "Type", render: (r) => <Badge color="indigo">{r.kind}</Badge>, width: 110 },
          { header: "Target", render: (r) => <Text size="sm">{summary(r)}</Text> },
          {
            header: "Credential",
            render: (r) => {
              const ref = r.s3?.secret_key || r.azure?.connection_string || "";
              return ref ? (
                <Text size="xs">{ref.replace("vault://", "")}</Text>
              ) : (
                <Text size="xs" c="dimmed">
                  ambient
                </Text>
              );
            },
          },
          {
            header: "Used by",
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
                  unused
                </Text>
              ),
          },
          {
            header: "",
            width: 100,
            render: (r) => (
              <Group gap="xs" justify="flex-end">
                <Button size="xs" variant="default" onClick={() => openEdit(r)}>
                  Edit
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
        title={editName ? `Edit ${editName}` : "New blob store"}
        size="lg"
      >
        <BlobStoreForm
          value={form}
          onChange={setForm}
          nameLocked={!!editName}
          submitLabel={editName ? "Save changes" : "Create store"}
          onSaved={() => {
            setOpen(false);
            load();
          }}
        />
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title="Delete blob store"
        message={
          confirmDel?.used_by?.length
            ? `${confirmDel.name} is used by ${confirmDel.used_by.join(", ")}. Detach those registries first.`
            : `Delete ${confirmDel?.name}? The stored objects are not removed — only this definition.`
        }
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />

      <Card withBorder mt="lg" padding="md">
        <Text size="sm" fw={500} mb={4}>
          Credentials
        </Text>
        <Text size="xs" c="dimmed">
          Access keys and connection strings are held encrypted and referenced by name, so one
          credential can serve several stores and rotating it is a single edit. A stored value is
          never shown again — it can be replaced, not read.
        </Text>
      </Card>
    </div>
  );
}
