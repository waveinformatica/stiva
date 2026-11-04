import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, DataTable, Badge, Button, Modal, TextInput, Textarea, Switch, MultiSelect, Group, Stack, Text,
  Code, ScrollArea, ConfirmModal, ErrorAlert, PageLoader, AddButton, DeleteButton, FormFooter, Alert,
} from "../../components/ui";
import { SecretField } from "../../components/SecretField";
import { api } from "../../lib/api";

type Hook = {
  id: number;
  name: string;
  url: string;
  secret: string;
  events: string[];
  registries: string[];
  enabled: boolean;
  description: string;
};
type Delivery = {
  id: number;
  event: string;
  status: string;
  attempts: number;
  status_code: number;
  error?: string;
  created_at: number;
  payload?: any;
};

const EMPTY: Hook = { id: 0, name: "", url: "", secret: "", events: ["push.*"], registries: [], enabled: true, description: "" };
const STATUS_COLOR: Record<string, string> = { pending: "gray", delivering: "blue", succeeded: "green", failed: "red" };
const when = (ns: number) => (ns ? new Date(ns / 1e6).toLocaleString() : "—");

export default function Webhooks() {
  const { t } = useTranslation();
  const [hooks, setHooks] = useState<Hook[]>([]);
  const [events, setEvents] = useState<string[]>([]);
  const [registries, setRegistries] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<Hook>(EMPTY);
  const [busy, setBusy] = useState(false);
  const [formErr, setFormErr] = useState<string | null>(null);
  const [confirmDel, setConfirmDel] = useState<Hook | null>(null);

  const [logOf, setLogOf] = useState<Hook | null>(null);
  const [log, setLog] = useState<Delivery[]>([]);
  const [detail, setDetail] = useState<Delivery | null>(null);

  const evLabel = (e: string) => {
    const key = "webhooks.event_" + e.replace(/\./g, "_");
    const label = t(key);
    return label === key ? e : `${label} (${e})`;
  };
  const evOptions = [
    { value: "*", label: "* (all)" },
    { value: "push.*", label: "push.* — " + t("webhooks.event_push_manifest") + " / " + t("webhooks.event_push_artifact") },
    { value: "delete.*", label: "delete.* — " + t("webhooks.event_delete_manifest") + " / " + t("webhooks.event_delete_artifact") },
    { value: "security.*", label: "security.*" },
    ...events.map((e) => ({ value: e, label: evLabel(e) })),
  ];

  const load = () =>
    Promise.all([api.webhooks(), api.adminRegistries()])
      .then(([w, r]: any[]) => {
        setHooks(w.webhooks || []);
        setEvents(w.events || []);
        setRegistries((r.registries || []).map((x: any) => x.name));
        setErr(null);
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  useEffect(() => {
    load();
  }, []);

  const openNew = () => {
    setForm(EMPTY);
    setFormErr(null);
    setOpen(true);
  };
  const openEdit = (h: Hook) => {
    setForm({ ...EMPTY, ...h });
    setFormErr(null);
    setOpen(true);
  };

  const save = async () => {
    setBusy(true);
    setFormErr(null);
    try {
      const { id, ...body } = form;
      await api.webhookSave(body, id || undefined);
      setOpen(false);
      load();
    } catch (e: any) {
      setFormErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const openLog = (h: Hook) => {
    setLogOf(h);
    setLog([]);
    api.webhookDeliveries(h.id).then((r: any) => setLog(r.deliveries || [])).catch((e) => setErr(e.message));
  };

  if (loading) return <PageLoader />;

  return (
    <div>
      <PageHeader
        title={t("webhooks.title")}
        description={t("webhooks.description")}
        actions={<AddButton onClick={openNew}>{t("webhooks.newWebhook")}</AddButton>}
      />
      {err && <ErrorAlert onClose={() => setErr(null)}>{err}</ErrorAlert>}
      {note && (
        <Alert color="teal" mb="md" withCloseButton onClose={() => setNote(null)}>
          {note}
        </Alert>
      )}

      <DataTable<Hook>
        rows={hooks}
        rowKey={(h) => String(h.id)}
        empty={t("webhooks.empty")}
        columns={[
          {
            header: t("webhooks.nameLabel"),
            render: (h) => (
              <Stack gap={0}>
                <Group gap="xs">
                  <Text fw={500}>{h.name}</Text>
                  {!h.enabled && <Badge color="gray">off</Badge>}
                  {h.secret && <Badge variant="outline" color="gray">HMAC</Badge>}
                </Group>
                <Text size="xs" c="dimmed" maw={360} truncate>{h.url}</Text>
              </Stack>
            ),
          },
          {
            header: t("webhooks.eventsLabel"),
            render: (h) => (
              <Group gap={4}>
                {h.events.map((e) => (
                  <Badge key={e} variant="light" color="indigo">{e}</Badge>
                ))}
              </Group>
            ),
          },
          {
            header: t("webhooks.registriesLabel"),
            render: (h) => (h.registries.length ? h.registries.join(", ") : <Text size="sm" c="dimmed">{t("webhooks.allRegistries")}</Text>),
          },
          {
            header: "",
            render: (h) => (
              <Group gap={4} justify="flex-end">
                <Button size="compact-xs" variant="light" color="gray" onClick={() => openLog(h)}>
                  {t("webhooks.deliveries")}
                </Button>
                <Button
                  size="compact-xs"
                  variant="light"
                  color="gray"
                  onClick={() =>
                    api
                      .webhookTest(h.id)
                      .then(() => setNote(t("webhooks.testQueued")))
                      .catch((e) => setErr(e.message))
                  }
                >
                  {t("webhooks.test")}
                </Button>
                <Button size="compact-xs" variant="default" onClick={() => openEdit(h)}>
                  {t("common.edit")}
                </Button>
                <DeleteButton onClick={() => setConfirmDel(h)} />
              </Group>
            ),
          },
        ]}
      />
      <Text size="xs" c="dimmed" mt="xs">{t("webhooks.retryNote")}</Text>

      <Modal opened={open} onClose={() => setOpen(false)} title={form.id ? t("webhooks.editTitle") : t("webhooks.newWebhook")} size="lg">
        <Stack>
          {formErr && <ErrorAlert mb="0">{formErr}</ErrorAlert>}
          <Group grow align="flex-start">
            <TextInput label={t("webhooks.nameLabel")} value={form.name} onChange={(e) => setForm({ ...form, name: e.currentTarget.value })} />
            <Switch mt="xl" label={t("webhooks.enabledLabel")} checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.currentTarget.checked })} />
          </Group>
          <TextInput
            label={t("webhooks.urlLabel")}
            placeholder="https://ci.example.com/hooks/stiva"
            value={form.url}
            onChange={(e) => setForm({ ...form, url: e.currentTarget.value })}
          />
          <MultiSelect
            label={t("webhooks.eventsLabel")}
            description={t("webhooks.eventsDesc")}
            data={evOptions}
            value={form.events}
            onChange={(v) => setForm({ ...form, events: v })}
            searchable
          />
          <MultiSelect
            label={t("webhooks.registriesLabel")}
            description={t("webhooks.registriesDesc")}
            data={registries}
            value={form.registries}
            onChange={(v) => setForm({ ...form, registries: v })}
            searchable
            clearable
          />
          <SecretField
            label={t("webhooks.secretLabel")}
            description={t("webhooks.secretDesc")}
            value={form.secret}
            onChange={(ref) => setForm({ ...form, secret: ref })}
            suggestedName={form.name ? `webhook-${form.name}` : ""}
          />
          <Textarea
            label={t("webhooks.descriptionLabel")}
            autosize
            minRows={1}
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.currentTarget.value })}
          />
          <FormFooter onCancel={() => setOpen(false)} loading={busy} onSubmit={save}>
            {t("common.save")}
          </FormFooter>
        </Stack>
      </Modal>

      <Modal opened={!!logOf} onClose={() => setLogOf(null)} title={logOf ? t("webhooks.deliveriesTitle", { name: logOf.name }) : ""} size="xl">
        <DataTable<Delivery>
          rows={log}
          rowKey={(d) => String(d.id)}
          empty={t("webhooks.noDeliveries")}
          columns={[
            { header: t("webhooks.eventHeader"), render: (d) => <Code>{d.event}</Code> },
            {
              header: t("webhooks.statusHeader"),
              render: (d) => <Badge color={STATUS_COLOR[d.status] || "gray"}>{t(`webhooks.status_${d.status}`)}</Badge>,
            },
            { header: t("webhooks.attemptsHeader"), render: (d) => d.attempts, width: 80 },
            { header: t("webhooks.responseHeader"), render: (d) => (d.status_code ? d.status_code : d.error ? <Text size="xs" c="red" maw={220} truncate>{d.error}</Text> : "—") },
            { header: t("webhooks.whenHeader"), render: (d) => when(d.created_at) },
            {
              header: "",
              render: (d) => (
                <Group gap={4} wrap="nowrap">
                  <Button size="compact-xs" variant="default" onClick={() => api.webhookDelivery(d.id).then(setDetail).catch((e) => setErr(e.message))}>
                    {t("webhooks.payload")}
                  </Button>
                  {(d.status === "failed" || d.status === "succeeded") && (
                    <Button
                      size="compact-xs"
                      variant="light"
                      onClick={() =>
                        api
                          .webhookRedeliver(d.id)
                          .then(() => logOf && openLog(logOf))
                          .catch((e) => setErr(e.message))
                      }
                    >
                      {t("webhooks.redeliver")}
                    </Button>
                  )}
                </Group>
              ),
            },
          ]}
        />
      </Modal>

      <Modal opened={!!detail} onClose={() => setDetail(null)} title={t("webhooks.payload")} size="lg">
        {detail && (
          <ScrollArea.Autosize mah={420}>
            <Code block>{JSON.stringify(detail.payload, null, 2)}</Code>
          </ScrollArea.Autosize>
        )}
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("webhooks.deleteTitle")}
        message={confirmDel ? t("webhooks.deleteBody", { name: confirmDel.name }) : ""}
        danger
        onConfirm={() => {
          const h = confirmDel!;
          setConfirmDel(null);
          api.webhookDelete(h.id).then(load).catch((e) => setErr(e.message));
        }}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
