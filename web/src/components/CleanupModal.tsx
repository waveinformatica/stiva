import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Modal, Stack, Switch, NumberInput, TextInput, Text, Group, Button, Alert, Badge, DataTable, ErrorAlert, Code,
} from "./ui";
import { api } from "../lib/api";

type Policy = { enabled: boolean; keep_last: number; max_age_days: number; protect: string[] };
const EMPTY: Policy = { enabled: false, keep_last: 0, max_age_days: 0, protect: [] };

// Retention policy editor for one hosted OCI registry: edit, save, preview
// (dry run), then delete. Deleting always follows a preview the user can see.
export default function CleanupModal({ name, onClose }: { name: string | null; onClose: () => void }) {
  const { t } = useTranslation();
  const [policy, setPolicy] = useState<Policy>(EMPTY);
  const [protect, setProtect] = useState("");
  const [report, setReport] = useState<any>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
    if (!name) return;
    setReport(null);
    setErr(null);
    setNote(null);
    api
      .cleanupGet(name)
      .then((p: Policy) => {
        setPolicy({ ...EMPTY, ...p });
        setProtect((p.protect || []).join(", "));
      })
      .catch((e) => setErr(e.message));
  }, [name]);

  const body = (): Policy => ({
    ...policy,
    protect: protect.split(",").map((s) => s.trim()).filter(Boolean),
  });

  const save = async () => {
    if (!name) return false;
    setErr(null);
    setNote(null);
    try {
      await api.cleanupSet(name, body());
      setNote(t("cleanup.saved"));
      return true;
    } catch (e: any) {
      setErr(e.message);
      return false;
    }
  };

  const run = async (dryRun: boolean) => {
    if (!name) return;
    setBusy(true);
    setErr(null);
    setNote(null);
    try {
      if (!(await save())) return;
      const r = await api.cleanupRun(name, dryRun);
      setReport(r);
      if (!dryRun) setNote(t("cleanup.deleted", { tags: r.tag_count, manifests: r.manifest_count }));
    } catch (e: any) {
      setErr(e.message);
      setReport(null);
    } finally {
      setBusy(false);
    }
  };

  const num = (v: string | number) => (typeof v === "number" ? Math.max(0, Math.floor(v)) : 0);

  return (
    <Modal opened={name !== null} onClose={onClose} title={t("cleanup.title", { name: name || "" })} size="lg">
      <Stack>
        <Text size="sm" c="dimmed">{t("cleanup.intro")}</Text>
        {err && <ErrorAlert mb="0">{err}</ErrorAlert>}
        <Switch
          label={t("cleanup.enabled")}
          checked={policy.enabled}
          onChange={(e) => setPolicy({ ...policy, enabled: e.currentTarget.checked })}
        />
        <Group grow align="flex-start">
          <NumberInput
            label={t("cleanup.keepLast")}
            description={t("cleanup.zeroOff")}
            min={0}
            value={policy.keep_last}
            onChange={(v) => setPolicy({ ...policy, keep_last: num(v) })}
          />
          <NumberInput
            label={t("cleanup.maxAge")}
            description={t("cleanup.zeroOff")}
            min={0}
            value={policy.max_age_days}
            onChange={(v) => setPolicy({ ...policy, max_age_days: num(v) })}
          />
        </Group>
        <TextInput
          label={t("cleanup.protect")}
          description={t("cleanup.protectNote")}
          placeholder="latest, v*"
          value={protect}
          onChange={(e) => setProtect(e.currentTarget.value)}
        />

        {note && <Alert color="teal">{note}</Alert>}

        {report && (
          <Stack gap="xs">
            <Badge
              size="lg"
              color={report.tag_count > 0 ? "yellow" : "green"}
              variant="light"
              style={{ alignSelf: "flex-start" }}
            >
              {report.tag_count > 0
                ? t(report.dry_run ? "cleanup.wouldDelete" : "cleanup.deleted", {
                    tags: report.tag_count,
                    manifests: report.manifest_count,
                  })
                : t("cleanup.nothing")}
            </Badge>
            {(report.repos || []).length > 0 && (
              <DataTable
                rowKey={(r: any) => r.repo}
                rows={report.repos}
                columns={[
                  { header: t("cleanup.repoHeader"), render: (r: any) => <Code>{r.repo}</Code> },
                  { header: t("cleanup.keptHeader"), render: (r: any) => r.kept, width: 90 },
                  {
                    header: t("cleanup.tagsHeader"),
                    render: (r: any) => (
                      <Text size="xs">{(r.tags || []).slice(0, 12).join(", ")}{(r.tags || []).length > 12 ? ` … +${r.tags.length - 12}` : ""}</Text>
                    ),
                  },
                ]}
              />
            )}
            {(report.errors || []).length > 0 && <ErrorAlert mb="0">{report.errors.join("; ")}</ErrorAlert>}
          </Stack>
        )}

        <Group justify="space-between" mt="xs">
          <Button variant="default" onClick={save} disabled={busy}>
            {t("cleanup.save")}
          </Button>
          <Group>
            <Button variant="default" loading={busy} onClick={() => run(true)}>
              {t("cleanup.preview")}
            </Button>
            <Button
              color="orange"
              loading={busy}
              disabled={!report || !report.dry_run || report.tag_count === 0}
              onClick={() => {
                if (name && window.confirm(t("cleanup.confirm", { tags: report.tag_count, name }))) run(false);
              }}
            >
              {t("cleanup.run")}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  );
}
