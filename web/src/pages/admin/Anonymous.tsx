import { useEffect, useState } from "react";
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
        title="Anonymous access"
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={openNew}>
            New identity
          </Button>
        }
      />

      {err && !open && (
        <Alert color="red" title="Error" mb="md" withCloseButton onClose={() => setErr(null)}>
          {err}
        </Alert>
      )}

      <Text size="sm" c="dimmed" mb="md">
        A caller with no credentials is matched against these in order, and the first that fits
        becomes the principal. From then on it is treated like any other identity: what it may do
        comes from the grants made to it. Address-restricted identities are tried first, so a
        catch-all never hides a narrower rule.
      </Text>

      {trustedProxies === 0 && rows.some((r) => (r.cidrs || []).length > 0) && (
        <Alert color="orange" title="Address filters are not being applied as you expect" mb="md">
          No proxy hop is trusted, so the address seen is the one the connection came from — for
          traffic through the gateway that is the gateway itself, not the caller. Until{" "}
          <Code>REGISTRY_TRUSTED_PROXIES</Code> is set to match the number of proxies in front of
          the registry, a CIDR rule cannot tell an outside caller from an in-cluster one. Reading
          the header without that would be worse: anyone could claim any address.
        </Alert>
      )}

      {catchAlls.length > 1 && (
        <Alert color="yellow" title="More than one catch-all" mb="md">
          {catchAlls.map((c) => c.name).join(", ")} all accept any address. Only the first is ever
          reached.
        </Alert>
      )}

      <DataTable<Identity>
        rows={rows}
        rowKey={(i) => i.name}
        empty="No anonymous identity. Callers without credentials are refused."
        columns={[
          {
            header: "Identity",
            render: (i) => (
              <Stack gap={0}>
                <Group gap="xs">
                  <Text fw={500}>{i.name}</Text>
                  {i.disabled && <Badge color="red" size="sm">disabled</Badge>}
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
            header: "Recognised by",
            render: (i) =>
              (i.cidrs || []).length ? (
                <Group gap={4}>
                  {(i.cidrs || []).map((c) => (
                    <Code key={c}>{c}</Code>
                  ))}
                </Group>
              ) : (
                <Badge color="orange" variant="light">
                  any address
                </Badge>
              ),
          },
          {
            header: "Grants",
            width: 120,
            render: (i) => {
              const own = grantCount[i.name] || 0;
              const shared = grantCount["*any*"] || 0;
              return (
                <Text size="xs" c={own + shared ? undefined : "dimmed"}>
                  {own} own{shared ? ` + ${shared} shared` : ""}
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
                  Edit
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
        title={editName ? `Edit ${editName}` : "New anonymous identity"}
        size="lg"
      >
        <Stack>
          {err && (
            <Alert color="red" title="Could not save">
              {err}
            </Alert>
          )}
          <TextInput
            label="Name"
            placeholder="anon-kubernetes"
            description="Grants are made to this name, the same way as for a user."
            value={name}
            disabled={!!editName}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <TextInput
            label="Description"
            placeholder="Image pulls from the cluster nodes"
            value={description}
            onChange={(e) => setDescription(e.currentTarget.value)}
          />
          <Textarea
            label="Addresses"
            description="One CIDR per line. Leave empty to accept any address — that makes this a catch-all, and it is tried after every restricted identity."
            placeholder={"172.16.0.0/16\n192.168.2.0/24"}
            autosize
            minRows={3}
            value={cidrText}
            onChange={(e) => setCidrText(e.currentTarget.value)}
          />
          <Switch
            label="Disabled"
            description="Kept, but never matched."
            checked={disabled}
            onChange={(e) => setDisabled(e.currentTarget.checked)}
          />
          <Text size="xs" c="dimmed">
            An identity grants nothing on its own. After creating it, give it a grant — typically
            a read-only role scoped to the registries it should pull from.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button loading={busy} onClick={save}>
              {editName ? "Save changes" : "Create identity"}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title="Delete anonymous identity"
        message={`Delete ${confirmDel?.name}? Callers it used to recognise fall through to the next matching identity, or are refused if there is none.`}
        danger
        onConfirm={remove}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
