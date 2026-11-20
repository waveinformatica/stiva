import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Anchor, SegmentedControl, Tabs } from "@mantine/core";
import {
  Alert,
  Badge,
  Button,
  Card,
  Code,
  DataTable,
  EmptyState,
  Group,
  Loader,
  PageHeader,
  Section,
  Stack,
  Text,
  TextInput,
} from "../components/ui";
import { IconSearch } from "../components/icons";
import {
  AdvisoryLink,
  FilterableImages,
  ImageFindingsPanel,
  ImagesTable,
  SeverityBadge,
  SeverityCounts,
  VulnerabilityLink,
  packagePath,
  setPath,
  severityRank,
} from "../components/Findings";
import {
  api,
  type PackageDetail,
  type PackageSummary,
  type VulnerabilityDetail,
  type VulnerabilitySet,
  type VulnerabilitySetDetail,
  type VulnerabilitySummary,
} from "../lib/api";

// The findings pages: vulnerability sets of the running images (what alerts
// and tickets are raised on), the search of vulnerabilities and packages, and
// the detail of a set, a vulnerability, a package and an image.

function useLoad<T>(load: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    let live = true;
    setLoading(true);
    setErr(null);
    load()
      .then((d) => live && setData(d))
      .catch((e) => live && (setErr(e.message), setData(null)))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, deps);
  return { data, err, loading };
}

function BackLink() {
  const { t } = useTranslation();
  return (
    <Anchor component={Link} to="/vulnerabilities" size="sm">
      ← {t("findings.back")}
    </Anchor>
  );
}

export function VulnerabilitiesHome() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab = params.get("tab") || "sets";
  const set = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next, { replace: true });
  };
  return (
    <div>
      <PageHeader title={t("findings.title")} description={t("findings.description")} />
      <Tabs value={tab} onChange={(v) => set("tab", v === "sets" ? "" : v || "")} keepMounted={false}>
        <Tabs.List mb="md">
          <Tabs.Tab value="sets">{t("findings.tabSets")}</Tabs.Tab>
          <Tabs.Tab value="vulnerabilities">{t("findings.tabVulnerabilities")}</Tabs.Tab>
          <Tabs.Tab value="packages">{t("findings.tabPackagesSearch")}</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="sets">
          <SetsTab level={params.get("level") || ""} onLevel={(l) => set("level", l)} />
        </Tabs.Panel>
        <Tabs.Panel value="vulnerabilities">
          <VulnerabilitySearch
            q={params.get("q") || ""}
            severity={params.get("severity") ?? "Critical"}
            onChange={(q, s) => {
              const next = new URLSearchParams(params);
              q ? next.set("q", q) : next.delete("q");
              next.set("severity", s);
              setParams(next, { replace: true });
            }}
          />
        </Tabs.Panel>
        <Tabs.Panel value="packages">
          <PackageSearch q={params.get("q") || ""} onChange={(q) => set("q", q)} />
        </Tabs.Panel>
      </Tabs>
    </div>
  );
}

function SetsTab({ level, onLevel }: { level: string; onLevel: (l: string) => void }) {
  const { t } = useTranslation();
  const { data, err, loading } = useLoad(() => api.vulnerabilitySets(), []);
  const sets: VulnerabilitySet[] = [...(data?.sets || [])]
    .filter((s) => !level || s.severity === level)
    .sort(
      (a, b) =>
        severityRank(a.severity) - severityRank(b.severity) ||
        b.containers - a.containers ||
        b.images - a.images ||
        a.set.localeCompare(b.set),
    );
  const all = data?.sets || [];
  return (
    <Stack gap="sm">
      <Group justify="space-between" wrap="wrap">
        <SegmentedControl
          size="xs"
          value={level}
          onChange={onLevel}
          data={[
            { value: "", label: t("findings.allSets", { count: all.length }) },
            { value: "Critical", label: t("findings.withCritical", { count: all.filter((s) => s.severity === "Critical").length }) },
            { value: "High", label: t("findings.onlyHigh", { count: all.filter((s) => s.severity === "High").length }) },
          ]}
        />
        <Text size="xs" c="dimmed">
          {t("findings.setsHint")}
        </Text>
      </Group>
      {err && <Alert color="red">{err}</Alert>}
      {loading && !data ? (
        <Loader size="sm" />
      ) : (
        <DataTable
          empty={t("findings.noSets")}
          rows={sets}
          rowKey={(s) => s.set}
          columns={[
            {
              header: t("findings.packages"),
              render: (s) => (
                <Stack gap={2}>
                  <Anchor component={Link} to={setPath(s.set)} size="sm" fw={600} lineClamp={2}>
                    {s.packages.join(", ")}
                  </Anchor>
                  <Text size="xs" c="dimmed" ff="monospace">
                    {s.set.slice(0, 12)}
                  </Text>
                </Stack>
              ),
            },
            {
              header: t("findings.vulnerabilitiesLabel"),
              width: 130,
              render: (s) => <SeverityCounts counts={s.counts} />,
            },
            { header: t("findings.images"), width: 90, render: (s) => <Text size="sm">{s.images}</Text> },
            { header: t("findings.containers"), width: 100, render: (s) => <Text size="sm">{s.containers}</Text> },
            {
              header: t("findings.namespaces"),
              render: (s) => (
                <Text size="xs" c="dimmed">
                  {s.namespaces.join(", ")}
                </Text>
              ),
            },
          ]}
        />
      )}
    </Stack>
  );
}

function SearchBox({ value, onSubmit, placeholder }: { value: string; onSubmit: (q: string) => void; placeholder: string }) {
  const { t } = useTranslation();
  const [q, setQ] = useState(value);
  useEffect(() => setQ(value), [value]);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(q.trim());
      }}
    >
      <Group gap="xs" wrap="nowrap">
        <TextInput
          style={{ flex: 1 }}
          maw={420}
          placeholder={placeholder}
          leftSection={<IconSearch size={14} />}
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
        />
        <Button type="submit">{t("findings.search")}</Button>
      </Group>
    </form>
  );
}

function VulnerabilitySearch({
  q,
  severity,
  onChange,
}: {
  q: string;
  severity: string;
  onChange: (q: string, severity: string) => void;
}) {
  const { t } = useTranslation();
  const enabled = q.length >= 2 || severity !== "";
  const { data, err, loading } = useLoad(
    () => (enabled ? api.findingsVulnerabilities(q, severity) : Promise.resolve({ vulnerabilities: [] })),
    [q, severity],
  );
  const rows: VulnerabilitySummary[] = data?.vulnerabilities || [];
  return (
    <Stack gap="sm">
      <Group gap="sm" wrap="wrap" align="flex-end">
        <SearchBox value={q} onSubmit={(nq) => onChange(nq, severity)} placeholder={t("findings.searchVulnerabilities")} />
        <SegmentedControl
          size="xs"
          value={severity}
          onChange={(s) => onChange(q, s)}
          data={[
            { value: "Critical", label: t("findings.severity.Critical") },
            { value: "High", label: t("findings.severity.High") },
            { value: "Medium", label: t("findings.severity.Medium") },
            { value: "Low", label: t("findings.severity.Low") },
            { value: "", label: t("findings.all") },
          ]}
        />
      </Group>
      {!enabled && (
        <Text size="sm" c="dimmed">
          {t("findings.searchHint")}
        </Text>
      )}
      {err && <Alert color="red">{err}</Alert>}
      {loading ? (
        <Loader size="sm" />
      ) : (
        enabled && (
          <DataTable
            empty={t("findings.noVulnerabilities")}
            rows={rows}
            rowKey={(v) => v.id + v.severity}
            columns={[
              { header: t("findings.severityHeader"), width: 110, render: (v) => <SeverityBadge severity={v.severity} size="xs" /> },
              {
                header: t("findings.vulnerability"),
                render: (v) => (
                  <Stack gap={2}>
                    <VulnerabilityLink id={v.id} />
                    <Text size="xs" c="dimmed" lineClamp={2}>
                      {v.title}
                    </Text>
                  </Stack>
                ),
              },
              { header: t("findings.packages"), width: 90, render: (v) => <Text size="sm">{v.packages}</Text> },
              { header: t("findings.images"), width: 90, render: (v) => <Text size="sm">{v.images}</Text> },
              { header: t("findings.containers"), width: 100, render: (v) => <Text size="sm">{v.containers}</Text> },
            ]}
          />
        )
      )}
    </Stack>
  );
}

function PackageSearch({ q, onChange }: { q: string; onChange: (q: string) => void }) {
  const { t } = useTranslation();
  const enabled = q.length >= 2;
  const { data, err, loading } = useLoad(
    () => (enabled ? api.findingsPackages(q) : Promise.resolve({ packages: [] })),
    [q],
  );
  const rows: PackageSummary[] = data?.packages || [];
  return (
    <Stack gap="sm">
      <SearchBox value={q} onSubmit={onChange} placeholder={t("findings.searchPackages")} />
      {!enabled && (
        <Text size="sm" c="dimmed">
          {t("findings.searchPackagesHint")}
        </Text>
      )}
      {err && <Alert color="red">{err}</Alert>}
      {loading ? (
        <Loader size="sm" />
      ) : (
        enabled && (
          <DataTable
            empty={t("findings.noPackages")}
            rows={rows}
            rowKey={(p) => p.key}
            columns={[
              {
                header: t("findings.package"),
                render: (p) => (
                  <Stack gap={2}>
                    <Anchor component={Link} to={packagePath(p.key)} size="sm" fw={600}>
                      {p.name}
                    </Anchor>
                    <Text size="xs" c="dimmed" style={{ wordBreak: "break-all" }}>
                      {p.purl}
                    </Text>
                  </Stack>
                ),
              },
              { header: t("findings.version"), render: (p) => <Text size="xs" ff="monospace">{p.version}</Text> },
              { header: t("findings.type"), width: 110, render: (p) => <Badge size="xs" variant="light" color="gray">{p.type || "—"}</Badge> },
              { header: t("findings.vulnerabilitiesLabel"), width: 170, render: (p) => <SeverityCounts counts={p.vulnerabilities} /> },
              { header: t("findings.images"), width: 90, render: (p) => <Text size="sm">{p.images}</Text> },
              { header: t("findings.containers"), width: 100, render: (p) => <Text size="sm">{p.containers}</Text> },
            ]}
          />
        )
      )}
    </Stack>
  );
}

export function SetPage() {
  const { t } = useTranslation();
  const { set = "" } = useParams();
  const { data, err, loading } = useLoad<VulnerabilitySetDetail>(() => api.vulnerabilitySet(set), [set]);
  return (
    <div>
      <BackLink />
      <PageHeader
        title={data ? data.packages.join(", ") : t("findings.setTitle")}
        description={t("findings.setDescription", { set: set.slice(0, 12) })}
      />
      {err && (
        <Alert color="gray" mb="md">
          {err === "no running image carries this set" ? t("findings.setGone") : err}
        </Alert>
      )}
      {loading && !data && <Loader size="sm" />}
      {data && (
        <>
          <Group gap="lg" mb="md" wrap="wrap">
            <SeverityBadge severity={data.severity} size="md" />
            <SeverityCounts counts={data.counts} />
            <Text size="sm">{t("findings.imagesCount", { count: data.images })}</Text>
            <Text size="sm">{t("findings.containersCount", { count: data.containers })}</Text>
            <Text size="sm" c="dimmed">
              {data.namespaces.join(", ")}
            </Text>
          </Group>
          <Section title={t("findings.packagesAndFixes")} description={t("findings.setFixHint")}>
            <DataTable
              rows={data.detail
                .flatMap((p) => p.vulnerabilities.map((v) => ({ p, v })))
                .sort((a, b) => severityRank(a.v.severity) - severityRank(b.v.severity) || a.p.name.localeCompare(b.p.name))}
              rowKey={(r) => r.p.purl + r.v.id}
              columns={[
                {
                  header: t("findings.package"),
                  render: (r) => (
                    <Stack gap={0}>
                      <Text size="sm" fw={600}>
                        {r.p.name}
                      </Text>
                      <Text size="xs" ff="monospace">
                        {r.p.version}
                      </Text>
                    </Stack>
                  ),
                },
                {
                  header: t("findings.severityHeader"),
                  width: 110,
                  render: (r) => <SeverityBadge severity={r.v.severity} size="xs" />,
                },
                {
                  header: t("findings.vulnerability"),
                  render: (r) => (
                    <Stack gap={2}>
                      <Group gap={6}>
                        <VulnerabilityLink id={r.v.id} />
                        <AdvisoryLink id={r.v.id} />
                      </Group>
                      <Text size="xs" c="dimmed" lineClamp={2}>
                        {r.v.title}
                      </Text>
                    </Stack>
                  ),
                },
                {
                  header: t("findings.fixedInHeader"),
                  width: 200,
                  render: (r) =>
                    r.v.fixed ? (
                      <Code fz="xs">{r.v.fixed}</Code>
                    ) : (
                      <Text size="xs" c="dimmed">
                        {t("findings.noFix")}
                      </Text>
                    ),
                },
              ]}
            />
          </Section>
          <Section title={t("findings.whereRunning")}>
            <ImagesTable images={data.images_of} />
          </Section>
        </>
      )}
    </div>
  );
}

export function VulnerabilityPage() {
  const { t } = useTranslation();
  const { id = "" } = useParams();
  const { data, err, loading } = useLoad<VulnerabilityDetail>(() => api.findingsVulnerability(id), [id]);
  return (
    <div>
      <BackLink />
      <PageHeader title={id} description={data?.title} actions={<AdvisoryLink id={id} />} />
      {err && <Alert color="gray" mb="md">{err}</Alert>}
      {loading && !data && <Loader size="sm" />}
      {data && (
        <>
          <Group gap="lg" mb="md">
            <SeverityBadge severity={data.severity} size="md" />
            <Text size="sm">{t("findings.packagesCount", { count: data.packages.length })}</Text>
            <Text size="sm">{t("findings.imagesCount", { count: data.image_count })}</Text>
          </Group>
          <Section title={t("findings.affectedPackages")}>
            <DataTable
              rows={data.packages}
              rowKey={(p) => p.key}
              columns={[
                {
                  header: t("findings.package"),
                  render: (p) => (
                    <Anchor component={Link} to={packagePath(p.key)} size="sm" fw={600}>
                      {p.name}
                    </Anchor>
                  ),
                },
                { header: t("findings.version"), render: (p) => <Text size="xs" ff="monospace">{p.version}</Text> },
                { header: t("findings.type"), width: 110, render: (p) => <Badge size="xs" variant="light" color="gray">{p.type || "—"}</Badge> },
                {
                  header: t("findings.fixedInHeader"),
                  render: (p) =>
                    p.fixed ? <Code fz="xs">{p.fixed}</Code> : <Text size="xs" c="dimmed">{t("findings.noFix")}</Text>,
                },
              ]}
            />
          </Section>
          <Section title={t("findings.images")}>
            <FilterableImages images={data.images_of} total={data.image_count} limit={data.image_limit} />
          </Section>
        </>
      )}
    </div>
  );
}

export function PackagePage() {
  const { t } = useTranslation();
  const { key = "" } = useParams();
  const { data, err, loading } = useLoad<PackageDetail>(() => api.findingsPackage(key), [key]);
  return (
    <div>
      <BackLink />
      <PageHeader title={data ? `${data.name} ${data.version}` : t("findings.package")} description={data?.purl} />
      {err && <Alert color="gray" mb="md">{err}</Alert>}
      {loading && !data && <Loader size="sm" />}
      {data && (
        <>
          <Group gap="lg" mb="md" wrap="wrap">
            <Badge variant="light" color="gray">
              {data.type || "—"}
            </Badge>
            <SeverityCounts counts={data.vulnerabilities} />
            <Text size="sm">{t("findings.imagesCount", { count: data.images })}</Text>
            <Text size="xs" c="dimmed">
              {t("findings.matchedAt", {
                at: new Date(data.matched_at).toLocaleString(),
                db: new Date(data.database_updated_at).toLocaleString(),
              })}
            </Text>
          </Group>
          <Section title={t("findings.vulnerabilitiesLabel")}>
            <DataTable
              empty={t("findings.noVulnerabilities")}
              rows={[...data.matched].sort((a, b) => severityRank(a.severity) - severityRank(b.severity) || a.id.localeCompare(b.id))}
              rowKey={(v) => v.id}
              columns={[
                { header: t("findings.severityHeader"), width: 110, render: (v) => <SeverityBadge severity={v.severity} size="xs" /> },
                {
                  header: t("findings.vulnerability"),
                  render: (v) => (
                    <Stack gap={2}>
                      <Group gap={6}>
                        <VulnerabilityLink id={v.id} />
                        <AdvisoryLink id={v.id} />
                      </Group>
                      <Text size="xs" c="dimmed" lineClamp={2}>
                        {v.title}
                      </Text>
                    </Stack>
                  ),
                },
                {
                  header: t("findings.fixedInHeader"),
                  width: 200,
                  render: (v) => (v.fixed ? <Code fz="xs">{v.fixed}</Code> : <Text size="xs" c="dimmed">{t("findings.noFix")}</Text>),
                },
              ]}
            />
          </Section>
          <Section title={t("findings.images")}>
            <FilterableImages images={data.images_of} total={data.images} limit={data.image_limit} />
          </Section>
        </>
      )}
    </div>
  );
}

export function ImagePage() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const target = { registry: params.get("registry") || "", repo: params.get("repo") || "", digest: params.get("digest") || "" };
  if (!target.registry || !target.repo || !target.digest) {
    return <EmptyState message={t("findings.noImageSelected")} />;
  }
  return (
    <div>
      <BackLink />
      <PageHeader title={target.repo} description={`${target.registry} · ${target.digest}`} />
      <Card padding="md">
        <ImageFindingsPanel target={target} />
      </Card>
    </div>
  );
}
