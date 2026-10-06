import { useEffect, useState } from "react";
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
        <PageHeader title="API keys" />
        <Alert color="blue" title="Sign in required">
          API keys belong to a named account. Anonymous callers have no account to own one with.
        </Alert>
      </div>
    );
  }

  if (loading) return <Loader />;

  return (
    <div>
      <PageHeader
        title="API keys"
        actions={
          <Button leftSection={<IconPlus size={14} />} onClick={() => { setCreatedKey(null); setCreateOpen(true); }}>
            New key
          </Button>
        }
      />
      {err && <Alert color="red" mb="md" title="Error">{err}</Alert>}
      {createdKey && (
        <Alert color="teal" mb="md" title="API key created — copy it now">
          <Code block>{createdKey}</Code>
        </Alert>
      )}
      <Text size="sm" c="dimmed" mb="md">
        Keys authenticate as you: as a client password (<Text span ff="monospace">docker login -u your-name</Text>)
        and as a bearer token for the API. Optional restrictions narrow the key;
        every request must still pass your own grants, so a key can never exceed you.
      </Text>
      <DataTable
        rowKey={(r) => r.masked}
        rows={rows}
        empty="No keys yet. Create one for each service or script."
        columns={[
          { header: "Label", render: (r) => r.label || "—" },
          { header: "Key", render: (r) => <Code>{r.masked}</Code> },
          {
            header: "Restrictions",
            render: (r) =>
              (r.grants || []).length === 0 ? (
                <Text size="xs" c="dimmed">full access</Text>
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

      <Modal opened={createOpen} onClose={() => setCreateOpen(false)} title="New API key" centered size="lg">
        <Stack>
          {err && (
            <Alert color="red" title="Could not create key">
              {err}
            </Alert>
          )}
          <TextInput label="Label" placeholder="ci-push, backup script…" value={label} onChange={(e) => setLabel(e.currentTarget.value)} />
          <GrantsEditor value={grants} onChange={setGrants} />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setCreateOpen(false)}>Cancel</Button>
            <Button leftSection={<IconKey size={14} />} loading={busy} onClick={submitCreate}>Generate key</Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={confirmDel !== null}
        title="Revoke key"
        message="Revoking a key immediately blocks its access. Services using it stop working."
        danger
        onConfirm={submitDelete}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
