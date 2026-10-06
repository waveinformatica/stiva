import { useEffect, useState } from "react";
import {
  PageHeader, Group, Text, Badge, Stack, Alert, Loader, Button, Modal,
  TextInput, PasswordInput, ActionIcon, DataTable, ConfirmModal, Code,
} from "../../components/ui";
import { IconPlus, IconTrash } from "../../components/icons";
import { api } from "../../lib/api";

type Secret = {
  key: string;
  description: string;
  public: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};

export default function Credentials() {
  const [rows, setRows] = useState<Secret[]>([]);
  const [usedBy, setUsedBy] = useState<Record<string, string[]>>({});
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [rotateTarget, setRotateTarget] = useState<Secret | null>(null);
  const [confirmDel, setConfirmDel] = useState<Secret | null>(null);
  const [busy, setBusy] = useState(false);

  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [description, setDescription] = useState("");
  const [publicKeyName, setPublicKeyName] = useState("access_key_id");
  const [publicValue, setPublicValue] = useState("");

  const load = () => {
    setLoading(true);
    Promise.all([api.secrets(), api.blobStores()])
      .then(([s, st]: any[]) => {
        setRows(s.secrets || []);
        // Which stores reference each credential: deleting one that is still
        // wired to something is refused, so say so before it is attempted.
        const map: Record<string, string[]> = {};
        for (const store of st.blob_stores || []) {
          const refs = [store.s3?.secret_key, store.azure?.connection_string].filter(Boolean);
          for (const r of refs) {
            const name = String(r).replace("vault://", "");
            (map[name] ||= []).push(store.name);
          }
        }
        setUsedBy(map);
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);

  const resetForm = () => {
    setKey("");
    setValue("");
    setDescription("");
    setPublicKeyName("access_key_id");
    setPublicValue("");
    setErr(null);
  };

  const create = async () => {
    setErr(null);
    setBusy(true);
    try {
      const pub: Record<string, string> = {};
      if (publicKeyName && publicValue) pub[publicKeyName] = publicValue;
      await api.createSecret({ key, value, description, public: pub });
      setCreateOpen(false);
      resetForm();
      load();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const rotate = async () => {
    if (!rotateTarget) return;
    setErr(null);
    setBusy(true);
    try {
      await api.updateSecret(rotateTarget.key, {
        value,
        description: description || rotateTarget.description,
        public: (rotateTarget.public as Record<string, unknown>) || {},
      });
      setRotateTarget(null);
      resetForm();
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
      await api.deleteSecret(confirmDel.key);
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
        title="Credentials"
        actions={
          <Button
            leftSection={<IconPlus size={16} />}
            onClick={() => {
              resetForm();
              setCreateOpen(true);
            }}
          >
            New credential
          </Button>
        }
      />

      {err && !createOpen && !rotateTarget && (
        <Alert color="red" title="Error" mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        Stored encrypted and referenced by name, so one credential can serve several stores and
        rotating it is a single edit. A value goes in once and is never shown again — it can be
        replaced, not read.
      </Text>

      <DataTable<Secret>
        rows={rows}
        rowKey={(s) => s.key}
        empty="No credential yet."
        columns={[
          {
            header: "Name",
            render: (s) => (
              <Stack gap={0}>
                <Text fw={500}>{s.key}</Text>
                {s.description && (
                  <Text size="xs" c="dimmed">
                    {s.description}
                  </Text>
                )}
              </Stack>
            ),
          },
          {
            header: "Public part",
            render: (s) =>
              Object.keys(s.public || {}).length ? (
                <Group gap={4}>
                  {Object.entries(s.public).map(([k, v]) => (
                    <Code key={k}>
                      {k}={String(v)}
                    </Code>
                  ))}
                </Group>
              ) : (
                <Text size="xs" c="dimmed">
                  none
                </Text>
              ),
          },
          {
            header: "Used by",
            render: (s) =>
              usedBy[s.key]?.length ? (
                <Group gap={4}>
                  {usedBy[s.key].map((n) => (
                    <Badge key={n} variant="light" size="sm">
                      {n}
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
            header: "Updated",
            width: 170,
            render: (s) => (
              <Text size="xs" c="dimmed">
                {new Date(s.updated_at).toLocaleString()}
              </Text>
            ),
          },
          {
            header: "",
            width: 130,
            render: (s) => (
              <Group gap="xs" justify="flex-end">
                <Button
                  size="xs"
                  variant="default"
                  onClick={() => {
                    setValue("");
                    setDescription(s.description);
                    setErr(null);
                    setRotateTarget(s);
                  }}
                >
                  Rotate
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(s)}>
                  <IconTrash size={16} />
                </ActionIcon>
              </Group>
            ),
          },
        ]}
      />

      <Modal opened={createOpen} onClose={() => setCreateOpen(false)} title="New credential">
        <Stack>
          {err && (
            <Alert color="red" title="Could not save">
              {err}
            </Alert>
          )}
          <TextInput
            label="Name"
            placeholder="minio-registry-rw"
            description="How you will refer to it elsewhere. Pick something you will recognise in six months."
            value={key}
            onChange={(e) => setKey(e.currentTarget.value)}
          />
          <TextInput
            label="Description"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <Group gap="sm" grow>
            <TextInput
              label="Public field"
              description="The non-secret half, kept alongside"
              value={publicKeyName}
              onChange={(e) => setPublicKeyName(e.currentTarget.value)}
            />
            <TextInput
              label="Its value"
              placeholder="registry"
              value={publicValue}
              onChange={(e) => setPublicValue(e.currentTarget.value)}
            />
          </Group>
          <PasswordInput
            label="Secret value"
            description="Encrypted at rest. It cannot be read back afterwards."
            value={value}
            onChange={(e) => setValue(e.currentTarget.value)}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button loading={busy} onClick={create} disabled={!key || !value}>
              Create
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={!!rotateTarget}
        onClose={() => setRotateTarget(null)}
        title={`Rotate ${rotateTarget?.key}`}
      >
        <Stack>
          {err && (
            <Alert color="red" title="Could not save">
              {err}
            </Alert>
          )}
          <Text size="sm">
            Everything referencing this credential picks up the new value immediately. That is the
            point of naming credentials instead of copying them.
          </Text>
          {usedBy[rotateTarget?.key || ""]?.length ? (
            <Alert color="yellow" title="In use">
              {usedBy[rotateTarget!.key].join(", ")} will start using the new value at once. Make
              sure it is already valid at the provider.
            </Alert>
          ) : null}
          <PasswordInput
            label="New value"
            value={value}
            onChange={(e) => setValue(e.currentTarget.value)}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setRotateTarget(null)}>
              Cancel
            </Button>
            <Button loading={busy} onClick={rotate} disabled={!value}>
              Rotate
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title="Delete credential"
        message={
          usedBy[confirmDel?.key || ""]?.length
            ? `${confirmDel?.key} is used by ${usedBy[confirmDel!.key].join(", ")}. Detach it there first — the server refuses the deletion.`
            : `Delete ${confirmDel?.key}? The value is gone for good.`
        }
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
