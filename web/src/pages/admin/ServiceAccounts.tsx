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
  ConfirmModal,
  ActionIcon,
  Code,
} from "../../components/ui";
import { api } from "../../lib/api";
import { IconPlus, IconTrash, IconKey } from "../../components/icons";
import { GrantsEditor, type KeyGrant } from "../../components/GrantsEditor";

interface KeyRow {
  username: string;
  label: string;
  masked: string;
  grants?: KeyGrant[];
  key?: string;
}

export default function ServiceAccounts() {
  const [rows, setRows] = useState<KeyRow[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [confirmDel, setConfirmDel] = useState<string | null>(null);
  const [username, setUsername] = useState("");
  const [label, setLabel] = useState("");
  const [grants, setGrants] = useState<KeyGrant[]>([]);
  const [createdKey, setCreatedKey] = useState<string | null>(null);

  const load = () => {
    setErr(null);
    api
      .serviceAccounts()
      .then((r) => setRows(r.service_accounts))
      .catch((e) => setErr(e.message));
  };
  useEffect(load, []);

  const submitCreate = async () => {
    try {
      const r = await api.createServiceAccount({ username, label, grants });
      setCreatedKey(r.key);
      setCreateOpen(false);
      setUsername("");
      setLabel("");
      setGrants([]);
      load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  const submitDelete = async () => {
    if (!confirmDel) return;
    try {
      await api.revokeServiceAccount(confirmDel);
      setConfirmDel(null);
      load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <div>
      <PageHeader
        title="Service Accounts"
        actions={
          <Button leftSection={<IconPlus size={14} />} onClick={() => { setCreatedKey(null); setCreateOpen(true); }}>
            New key
          </Button>
        }
      />
      {err && <Alert color="red" mb="md" title="Error">{err}</Alert>}
      {createdKey && (
        <Alert color="teal" mb="md" title="API key created — copy it now">
          <Code block style={{ overflowWrap: "anywhere" }}>{createdKey}</Code>
        </Alert>
      )}
      <DataTable
        rowKey={(r) => r.masked + r.username}
        rows={rows}
        columns={[
          { header: "Username", render: (r) => <Text fw={600}>{r.username}</Text> },
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
            header: "Actions",
            render: (r) => (
              <ActionIcon color="red" variant="subtle" onClick={() => setConfirmDel(r.key || r.masked)}>
                <IconTrash size={16} />
              </ActionIcon>
            ),
          },
        ]}
      />

      <Modal opened={createOpen} onClose={() => setCreateOpen(false)} title="New service account" centered size="lg">
        <Stack>
          <TextInput label="Username" value={username} onChange={(e) => setUsername(e.currentTarget.value)} />
          <TextInput label="Label" value={label} onChange={(e) => setLabel(e.currentTarget.value)} />
          <GrantsEditor value={grants} onChange={setGrants} />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setCreateOpen(false)}>Cancel</Button>
            <Button leftSection={<IconKey size={14} />} onClick={submitCreate}>Generate key</Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={confirmDel !== null}
        title="Revoke key"
        message="Revoking a service account key immediately blocks its access."
        danger
        onCancel={() => setConfirmDel(null)}
        onConfirm={submitDelete}
      />
    </div>
  );
}
