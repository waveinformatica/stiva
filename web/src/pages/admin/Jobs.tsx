import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader, Section, DataTable, Badge, Button, Modal, Select, Switch, TextInput, Group, Stack, Text,
  Code, ScrollArea, ConfirmModal, ErrorAlert, PageLoader, AddButton, DeleteButton, FormFooter, Alert,
} from "../../components/ui";
import { api } from "../../lib/api";

type Job = {
  id: number;
  registry: string;
  kind: string;
  status: string;
  dry_run: boolean;
  trigger: string;
  created_by?: string;
  created_at: number;
  started_at: number;
  finished_at: number;
  error?: string;
  result?: any;
  params?: any;
};
type Schedule = { registry: string; kind: string; enabled: boolean; interval_secs: number; last_run_at: number };

const KINDS = ["cleanup", "gc", "reconcile"];
const STATUS_COLOR: Record<string, string> = { queued: "gray", running: "blue", succeeded: "green", failed: "red" };

// Timestamps are UnixNano; 0 means "not yet".
const when = (ns: number) => (ns ? new Date(ns / 1e6).toLocaleString() : "—");

function duration(j: Job) {
  if (!j.started_at) return "—";
  const end = j.finished_at || Date.now() * 1e6;
  const s = Math.max(0, Math.round((end - j.started_at) / 1e9));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

function intervalLabel(secs: number) {
  if (secs % 86400 === 0) return `${secs / 86400}d`;
  if (secs % 3600 === 0) return `${secs / 3600}h`;
  return `${Math.max(1, Math.round(secs / 60))}m`;
}

export default function Jobs() {
  const { t } = useTranslation();
  const [jobs, setJobs] = useState<Job[]>([]);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [registries, setRegistries] = useState<{ name: string; type: string }[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [fRegistry, setFRegistry] = useState<string | null>(null);
  const [fKind, setFKind] = useState<string | null>(null);

  const [newOpen, setNewOpen] = useState(false);
  const [nReg, setNReg] = useState<string | null>(null);
  const [nKind, setNKind] = useState<string | null>("cleanup");
  const [nDry, setNDry] = useState(true);
  const [nOlder, setNOlder] = useState("1h");
  const [busy, setBusy] = useState(false);
  const [formErr, setFormErr] = useState<string | null>(null);

  const [detail, setDetail] = useState<Job | null>(null);

  const [schedOpen, setSchedOpen] = useState(false);
  const [sReg, setSReg] = useState<string | null>(null);
  const [sKind, setSKind] = useState<string | null>("cleanup");
  const [sEvery, setSEvery] = useState("24h");
  const [sEnabled, setSEnabled] = useState(true);
  const [confirmDel, setConfirmDel] = useState<Schedule | null>(null);

  const load = (quiet = false) => {
    if (!quiet) setLoading(true);
    Promise.all([
      api.jobs({ registry: fRegistry || undefined, kind: fKind || undefined, limit: 100 }),
      api.schedules(),
    ])
      .then(([j, s]: any[]) => {
        setJobs(j.jobs || []);
        setSchedules(s.schedules || []);
        setErr(null);
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(() => load(), [fRegistry, fKind]);
  useEffect(() => {
    api.adminRegistries().then((r: any) => setRegistries(r.registries || [])).catch(() => {});
  }, []);

  // Follow active jobs without a manual refresh.
  const active = jobs.some((j) => j.status === "queued" || j.status === "running");
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => loadRef.current(true), 4000);
    return () => clearInterval(id);
  }, [active]);

  const regOptions = registries.filter((r) => r.type !== "group").map((r) => ({ value: r.name, label: r.name }));
  const kindOptions = KINDS.map((k) => ({ value: k, label: t(`jobs.kind_${k}`) }));

  const enqueue = async () => {
    if (!nReg || !nKind) return;
    setBusy(true);
    setFormErr(null);
    try {
      await api.jobEnqueue({
        registry: nReg,
        kind: nKind,
        dry_run: nDry,
        params: nKind === "gc" ? { older_than: nOlder } : undefined,
      });
      setNewOpen(false);
      load(true);
    } catch (e: any) {
      setFormErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const saveSchedule = async () => {
    if (!sReg || !sKind) return;
    setBusy(true);
    setFormErr(null);
    try {
      await api.scheduleSet(sReg, sKind, { enabled: sEnabled, interval: sEvery });
      setSchedOpen(false);
      load(true);
    } catch (e: any) {
      setFormErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const openDetail = (j: Job) => {
    setDetail(j);
    api.job(j.id).then((full: Job) => setDetail(full)).catch(() => {});
  };

  const statusBadge = (j: Job) => (
    <Group gap={4} wrap="nowrap">
      <Badge color={STATUS_COLOR[j.status] || "gray"}>{t(`jobs.status_${j.status}`)}</Badge>
      {j.dry_run && <Badge variant="outline" color="gray">{t("jobs.dryRunBadge")}</Badge>}
    </Group>
  );

  if (loading) return <PageLoader />;

  return (
    <div>
      <PageHeader
        title={t("jobs.title")}
        description={t("jobs.description")}
        actions={
          <AddButton
            onClick={() => {
              setFormErr(null);
              setNReg(regOptions[0]?.value ?? null);
              setNewOpen(true);
            }}
          >
            {t("jobs.newJob")}
          </AddButton>
        }
      />
      {err && <ErrorAlert onClose={() => setErr(null)}>{err}</ErrorAlert>}

      <Section
        title={t("jobs.schedules")}
        actions={
          <Button
            size="xs"
            variant="default"
            onClick={() => {
              setFormErr(null);
              setSReg(regOptions[0]?.value ?? null);
              setSchedOpen(true);
            }}
          >
            {t("jobs.addSchedule")}
          </Button>
        }
      >
        <DataTable<Schedule>
          rows={schedules}
          rowKey={(s) => s.registry + "/" + s.kind}
          empty={t("jobs.noSchedules")}
          columns={[
            { header: t("jobs.registryHeader"), render: (s) => <Text fw={500}>{s.registry}</Text> },
            { header: t("jobs.kindHeader"), render: (s) => t(`jobs.kind_${s.kind}`) },
            { header: t("jobs.intervalHeader"), render: (s) => intervalLabel(s.interval_secs), width: 90 },
            {
              header: t("jobs.enabledHeader"),
              render: (s) => (
                <Switch
                  checked={s.enabled}
                  onChange={(e) =>
                    api
                      .scheduleSet(s.registry, s.kind, {
                        enabled: e.currentTarget.checked,
                        interval: intervalLabel(s.interval_secs),
                      })
                      .then(() => load(true))
                      .catch((er) => setErr(er.message))
                  }
                />
              ),
              width: 100,
            },
            { header: t("jobs.lastRunHeader"), render: (s) => when(s.last_run_at) },
            { header: "", render: (s) => <DeleteButton onClick={() => setConfirmDel(s)} />, width: 60 },
          ]}
        />
        <Text size="xs" c="dimmed" mt="xs">{t("jobs.scheduleNote")}</Text>
      </Section>

      <Group justify="space-between" mb="xs">
        <Text fw={650}>{t("jobs.history")}</Text>
        <Group gap="xs">
          <Select
            size="xs"
            w={170}
            clearable
            placeholder={t("jobs.allRegistries")}
            data={regOptions}
            value={fRegistry}
            onChange={setFRegistry}
          />
          <Select
            size="xs"
            w={170}
            clearable
            placeholder={t("jobs.allKinds")}
            data={kindOptions}
            value={fKind}
            onChange={setFKind}
          />
          <Button size="xs" variant="default" onClick={() => load(true)}>
            {t("jobs.refresh")}
          </Button>
        </Group>
      </Group>
      <DataTable<Job>
        rows={jobs}
        rowKey={(j) => String(j.id)}
        empty={t("jobs.noJobs")}
        columns={[
          { header: t("jobs.idHeader"), render: (j) => j.id, width: 60 },
          { header: t("jobs.registryHeader"), render: (j) => <Text fw={500}>{j.registry}</Text> },
          { header: t("jobs.kindHeader"), render: (j) => t(`jobs.kind_${j.kind}`) },
          { header: t("jobs.statusHeader"), render: statusBadge },
          {
            header: t("jobs.triggerHeader"),
            render: (j) => (
              <Text size="sm">
                {j.trigger === "schedule" ? t("jobs.trigger_schedule") : j.created_by || t("jobs.trigger_manual")}
              </Text>
            ),
          },
          { header: t("jobs.whenHeader"), render: (j) => when(j.created_at) },
          { header: t("jobs.durationHeader"), render: duration, width: 100 },
          {
            header: "",
            render: (j) => (
              <Button size="compact-xs" variant="default" onClick={() => openDetail(j)}>
                {t("jobs.result")}
              </Button>
            ),
            width: 90,
          },
        ]}
      />

      <Modal opened={newOpen} onClose={() => setNewOpen(false)} title={t("jobs.newJob")}>
        <Stack>
          {formErr && <ErrorAlert mb="0">{formErr}</ErrorAlert>}
          <Select label={t("jobs.registryHeader")} data={regOptions} value={nReg} onChange={setNReg} allowDeselect={false} />
          <Select label={t("jobs.kindHeader")} data={kindOptions} value={nKind} onChange={setNKind} allowDeselect={false} />
          {nKind === "gc" && <TextInput label={t("jobs.olderThan")} value={nOlder} onChange={(e) => setNOlder(e.currentTarget.value)} />}
          <Switch label={t("jobs.dryRun")} checked={nDry} onChange={(e) => setNDry(e.currentTarget.checked)} />
          <FormFooter onCancel={() => setNewOpen(false)} loading={busy} disabled={!nReg} onSubmit={enqueue}>
            {t("jobs.enqueue")}
          </FormFooter>
        </Stack>
      </Modal>

      <Modal opened={schedOpen} onClose={() => setSchedOpen(false)} title={t("jobs.addSchedule")}>
        <Stack>
          {formErr && <ErrorAlert mb="0">{formErr}</ErrorAlert>}
          <Select label={t("jobs.registryHeader")} data={regOptions} value={sReg} onChange={setSReg} allowDeselect={false} />
          <Select label={t("jobs.kindHeader")} data={kindOptions} value={sKind} onChange={setSKind} allowDeselect={false} />
          <TextInput label={t("jobs.interval")} value={sEvery} onChange={(e) => setSEvery(e.currentTarget.value)} />
          <Switch label={t("jobs.enabledHeader")} checked={sEnabled} onChange={(e) => setSEnabled(e.currentTarget.checked)} />
          <FormFooter onCancel={() => setSchedOpen(false)} loading={busy} disabled={!sReg} onSubmit={saveSchedule}>
            {t("jobs.saveSchedule")}
          </FormFooter>
        </Stack>
      </Modal>

      <Modal opened={!!detail} onClose={() => setDetail(null)} title={detail ? t("jobs.details", { id: detail.id }) : ""} size="lg">
        {detail && (
          <Stack gap="xs">
            <Group gap="xs">
              {statusBadge(detail)}
              <Text size="sm">{detail.registry} · {t(`jobs.kind_${detail.kind}`)}</Text>
              <Text size="sm" c="dimmed">{duration(detail)}</Text>
            </Group>
            {detail.error && <Alert color="red">{detail.error}</Alert>}
            {detail.result ? (
              <ScrollArea.Autosize mah={360}>
                <Code block>{JSON.stringify(detail.result, null, 2)}</Code>
              </ScrollArea.Autosize>
            ) : (
              !detail.error && <Text size="sm" c="dimmed">—</Text>
            )}
          </Stack>
        )}
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("jobs.deleteScheduleTitle")}
        message={confirmDel ? t("jobs.deleteScheduleBody", { kind: t(`jobs.kind_${confirmDel.kind}`), registry: confirmDel.registry }) : ""}
        danger
        onConfirm={() => {
          const s = confirmDel!;
          setConfirmDel(null);
          api.scheduleDelete(s.registry, s.kind).then(() => load(true)).catch((e) => setErr(e.message));
        }}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
