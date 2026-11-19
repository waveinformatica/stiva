import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { Anchor, SegmentedControl, Tabs } from "@mantine/core";
import {
  Alert,
  Badge,
  Button,
  Code,
  DataTable,
  EmptyState,
  Group,
  Loader,
  Stack,
  Text,
  TextInput,
  Tooltip,
} from "./ui";
import { IconSearch } from "./icons";
import {
  api,
  downloadSBOM,
  type FindingImage,
  type ImageFindings,
  type ImagePackage,
  type Placement,
  type ScanTarget,
  type Severity,
} from "../lib/api";

// Shared pieces of the findings pages (pages/Vulnerabilities.tsx) and of the
// image view of the explorer.

export const SEVERITIES: Severity[] = ["Critical", "High", "Medium", "Low", "Unknown"];

const SEVERITY_COLOR: Record<string, string> = {
  Critical: "red",
  High: "orange",
  Medium: "yellow",
  Low: "blue",
  Unknown: "gray",
};

export const severityRank = (s: string) => {
  const i = SEVERITIES.indexOf(s as Severity);
  return i < 0 ? SEVERITIES.length : i;
};

export function SeverityBadge({ severity, size = "sm" }: { severity: string; size?: "xs" | "sm" | "md" }) {
  const { t } = useTranslation();
  return (
    <Badge size={size} color={SEVERITY_COLOR[severity] || "gray"} variant="light">
      {t(`findings.severity.${severity}`, { defaultValue: severity })}
    </Badge>
  );
}

/** Counts per severity, worst first; nothing is shown for a zero count. */
export function SeverityCounts({ counts, empty }: { counts: Record<string, number>; empty?: string }) {
  const { t } = useTranslation();
  const present = SEVERITIES.filter((s) => (counts?.[s] || 0) > 0);
  if (!present.length) {
    return (
      <Text size="xs" c="dimmed">
        {empty ?? t("findings.none")}
      </Text>
    );
  }
  return (
    <Group gap={4} wrap="wrap">
      {present.map((s) => (
        <Tooltip key={s} label={t(`findings.severity.${s}`)}>
          <Badge size="sm" color={SEVERITY_COLOR[s]} variant={s === "Critical" || s === "High" ? "filled" : "light"}>
            {counts[s]}
          </Badge>
        </Tooltip>
      ))}
    </Group>
  );
}

export const shortDigest = (d: string) => (d.startsWith("sha256:") ? d.slice(7, 19) : d);

export const imagePath = (t: ScanTarget) =>
  `/vulnerabilities/image?registry=${encodeURIComponent(t.registry)}&repo=${encodeURIComponent(t.repo)}&digest=${encodeURIComponent(t.digest)}`;
export const packagePath = (key: string) => `/vulnerabilities/packages/${encodeURIComponent(key)}`;
export const vulnerabilityPath = (id: string) => `/vulnerabilities/cve/${encodeURIComponent(id)}`;
export const setPath = (set: string, severity: string) =>
  `/vulnerabilities/sets/${encodeURIComponent(set)}?severity=${encodeURIComponent(severity)}`;

/** Where the advisory of a vulnerability is published, when its identifier says. */
export function advisoryUrl(id: string): string | null {
  if (id.startsWith("CVE-")) return `https://nvd.nist.gov/vuln/detail/${encodeURIComponent(id)}`;
  if (id.startsWith("GHSA-")) return `https://github.com/advisories/${encodeURIComponent(id)}`;
  return null;
}

export function VulnerabilityLink({ id }: { id: string }) {
  return (
    <Anchor component={Link} to={vulnerabilityPath(id)} size="sm" ff="monospace">
      {id}
    </Anchor>
  );
}

export function AdvisoryLink({ id }: { id: string }) {
  const { t } = useTranslation();
  const url = advisoryUrl(id);
  if (!url) return null;
  return (
    <Anchor href={url} target="_blank" rel="noreferrer" size="xs">
      {t("findings.advisory")}
    </Anchor>
  );
}

/** The workloads running an image, one row per container. */
export function PlacementsTable({ placements }: { placements: Placement[] }) {
  const { t } = useTranslation();
  return (
    <DataTable
      empty={t("findings.notRunning")}
      rows={placements}
      rowKey={(p) => `${p.node}/${p.namespace}/${p.pod}/${p.container}`}
      columns={[
        { header: t("findings.namespace"), render: (p) => <Text size="sm">{p.namespace}</Text> },
        { header: t("findings.pod"), render: (p) => <Text size="sm">{p.pod}</Text> },
        { header: t("findings.container"), render: (p) => <Text size="sm">{p.container}</Text> },
        { header: t("findings.node"), width: 90, render: (p) => <Text size="sm">{p.node}</Text> },
        {
          header: t("findings.imageAsPulled"),
          render: (p) => (
            <Text size="xs" ff="monospace" style={{ wordBreak: "break-all" }}>
              {p.image}
            </Text>
          ),
        },
      ]}
    />
  );
}

/** Summary of placements: namespaces and how many containers. */
export function RunningSummary({ placements }: { placements: Placement[] }) {
  const { t } = useTranslation();
  if (!placements.length) {
    return (
      <Text size="xs" c="dimmed">
        {t("findings.notRunning")}
      </Text>
    );
  }
  const namespaces = Array.from(new Set(placements.map((p) => p.namespace))).sort();
  return (
    <Stack gap={2}>
      <Text size="xs">{t("findings.containersCount", { count: placements.length })}</Text>
      <Text size="xs" c="dimmed">
        {namespaces.join(", ")}
      </Text>
    </Stack>
  );
}

/** Images (manifests) with their tags and where they run. */
export function ImagesTable({ images }: { images: (ScanTarget & { tags?: string[]; placements: Placement[] })[] }) {
  const { t } = useTranslation();
  return (
    <DataTable
      empty={t("findings.noImages")}
      rows={images}
      rowKey={(i) => `${i.registry}/${i.repo}@${i.digest}`}
      columns={[
        {
          header: t("findings.image"),
          render: (i) => (
            <Stack gap={2}>
              <Anchor component={Link} to={imagePath(i)} size="sm" style={{ wordBreak: "break-all" }}>
                {i.repo}
              </Anchor>
              <Group gap={4} wrap="wrap">
                <Badge size="xs" variant="light" color="gray">
                  {i.registry}
                </Badge>
                <Code fz="xs">{shortDigest(i.digest)}</Code>
                {(i.tags || []).slice(0, 4).map((tag) => (
                  <Badge key={tag} size="xs" variant="outline" color="teal">
                    {tag}
                  </Badge>
                ))}
                {(i.tags || []).length > 4 && (
                  <Text size="xs" c="dimmed">
                    +{(i.tags || []).length - 4}
                  </Text>
                )}
              </Group>
            </Stack>
          ),
        },
        { header: t("findings.running"), width: 260, render: (i) => <RunningSummary placements={i.placements} /> },
      ]}
    />
  );
}

function packageWorst(p: ImagePackage): number {
  return p.vulnerabilities.reduce((a, v) => Math.min(a, severityRank(v.severity)), SEVERITIES.length);
}

/** Everything known of one image manifest: vulnerabilities by package,
 *  secrets, all its packages, where it runs and its SBOM. */
export function ImageFindingsPanel({ target, compact }: { target: ScanTarget; compact?: boolean }) {
  const { t } = useTranslation();
  const [f, setF] = useState<ImageFindings | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [minSeverity, setMinSeverity] = useState<string>("High");
  const [filter, setFilter] = useState("");
  const [downloading, setDownloading] = useState(false);

  useEffect(() => {
    let live = true;
    setF(null);
    setErr(null);
    api
      .findingsImage(target.registry, target.repo, target.digest)
      .then((r) => live && setF(r))
      .catch((e) => live && setErr(e.message));
    return () => {
      live = false;
    };
  }, [target.registry, target.repo, target.digest]);

  const vulnerable = useMemo(() => {
    if (!f) return [];
    const limit = minSeverity === "all" ? SEVERITIES.length : severityRank(minSeverity);
    const q = filter.trim().toLowerCase();
    return f.packages
      .map((p) => ({ ...p, vulnerabilities: p.vulnerabilities.filter((v) => severityRank(v.severity) <= limit) }))
      .filter((p) => p.vulnerabilities.length > 0)
      .filter(
        (p) =>
          !q ||
          p.name.toLowerCase().includes(q) ||
          p.vulnerabilities.some((v) => v.id.toLowerCase().includes(q) || v.title.toLowerCase().includes(q)),
      )
      .sort((a, b) => packageWorst(a) - packageWorst(b) || a.name.localeCompare(b.name));
  }, [f, minSeverity, filter]);

  const allPackages = useMemo(() => {
    if (!f) return [];
    const q = filter.trim().toLowerCase();
    return f.packages.filter((p) => !q || p.name.toLowerCase().includes(q) || p.purl.toLowerCase().includes(q));
  }, [f, filter]);

  if (err) return <Alert color="red">{err}</Alert>;
  if (!f) return <Loader size="sm" />;
  if (!f.scanned) {
    return f.failure ? (
      <Alert color="orange" title={t("findings.scanFailed")}>
        <Text size="sm">{f.failure.error}</Text>
        <Text size="xs" c="dimmed" mt={4}>
          {t("findings.failedAt", { at: new Date(f.failure.failed_at).toLocaleString(), analyzer: f.failure.analyzer })}
        </Text>
      </Alert>
    ) : (
      <Text size="sm" c="dimmed">
        {t("findings.notScanned")}
      </Text>
    );
  }

  const secretCount = f.secrets.length;
  return (
    <Stack gap="sm">
      <Group justify="space-between" wrap="wrap">
        <Group gap="md" wrap="wrap">
          <Group gap={6}>
            <Text size="xs" c="dimmed">
              {t("findings.vulnerabilitiesLabel")}
            </Text>
            <SeverityCounts counts={f.vulnerability_counts} />
          </Group>
          <Group gap={6}>
            <Text size="xs" c="dimmed">
              {t("findings.secretsLabel")}
            </Text>
            <SeverityCounts counts={f.secret_counts} />
          </Group>
          <Text size="xs" c="dimmed">
            {t("findings.scannedAt", { at: f.scanned_at ? new Date(f.scanned_at).toLocaleString() : "—", analyzer: f.analyzer })}
          </Text>
        </Group>
        <Group gap="xs">
          {compact && (
            <Button size="compact-xs" variant="subtle" component={Link} to={imagePath(target)}>
              {t("findings.openPage")}
            </Button>
          )}
          <Button
            size="compact-xs"
            variant="light"
            loading={downloading}
            onClick={() => {
              setDownloading(true);
              downloadSBOM(target.registry, target.repo, target.digest)
                .catch((e) => setErr(e.message))
                .finally(() => setDownloading(false));
            }}
          >
            {t("findings.downloadSbom")}
          </Button>
        </Group>
      </Group>

      <Tabs defaultValue="vulnerabilities" keepMounted={false}>
        <Tabs.List>
          <Tabs.Tab value="vulnerabilities">{t("findings.tabVulnerabilities")}</Tabs.Tab>
          <Tabs.Tab value="secrets">{t("findings.tabSecrets", { count: secretCount })}</Tabs.Tab>
          <Tabs.Tab value="packages">{t("findings.tabPackages", { count: f.packages.length })}</Tabs.Tab>
          <Tabs.Tab value="running">{t("findings.tabRunning", { count: f.placements.length })}</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="vulnerabilities" pt="sm">
          <Group gap="sm" mb="sm" wrap="wrap">
            <SegmentedControl
              size="xs"
              value={minSeverity}
              onChange={setMinSeverity}
              data={[
                { value: "Critical", label: t("findings.severity.Critical") },
                { value: "High", label: t("findings.atLeastHigh") },
                { value: "Medium", label: t("findings.atLeastMedium") },
                { value: "all", label: t("findings.all") },
              ]}
            />
            <TextInput
              size="xs"
              placeholder={t("findings.filterPlaceholder")}
              leftSection={<IconSearch size={14} />}
              value={filter}
              onChange={(e) => setFilter(e.currentTarget.value)}
            />
          </Group>
          <DataTable
            empty={t("findings.noVulnerabilities")}
            rows={vulnerable}
            rowKey={(p) => p.key}
            columns={[
              {
                header: t("findings.package"),
                width: 260,
                render: (p) => (
                  <Stack gap={2}>
                    <Anchor component={Link} to={packagePath(p.key)} size="sm" fw={600}>
                      {p.name}
                    </Anchor>
                    <Text size="xs" ff="monospace">
                      {p.version}
                    </Text>
                    <Text size="xs" c="dimmed" style={{ wordBreak: "break-all" }}>
                      {p.targets.join(", ")}
                    </Text>
                  </Stack>
                ),
              },
              {
                header: t("findings.vulnerabilitiesLabel"),
                render: (p) => (
                  <Stack gap={6}>
                    {[...p.vulnerabilities]
                      .sort((a, b) => severityRank(a.severity) - severityRank(b.severity) || a.id.localeCompare(b.id))
                      .map((v) => (
                        <Group key={v.id} gap={6} wrap="nowrap" align="flex-start">
                          <SeverityBadge severity={v.severity} size="xs" />
                          <Stack gap={0} style={{ minWidth: 0 }}>
                            <Group gap={6}>
                              <VulnerabilityLink id={v.id} />
                              {v.fixed ? (
                                <Text size="xs" c="teal">
                                  {t("findings.fixedIn", { version: v.fixed })}
                                </Text>
                              ) : (
                                <Text size="xs" c="dimmed">
                                  {t("findings.noFix")}
                                </Text>
                              )}
                            </Group>
                            <Text size="xs" c="dimmed" lineClamp={2}>
                              {v.title}
                            </Text>
                          </Stack>
                        </Group>
                      ))}
                  </Stack>
                ),
              },
            ]}
          />
        </Tabs.Panel>

        <Tabs.Panel value="secrets" pt="sm">
          <DataTable
            empty={t("findings.noSecrets")}
            rows={f.secrets.map((s, i) => ({ ...s, n: i }))}
            rowKey={(s) => String(s.n)}
            columns={[
              { header: t("findings.severityHeader"), width: 110, render: (s) => <SeverityBadge severity={s.severity} size="xs" /> },
              { header: t("findings.rule"), render: (s) => <Code fz="xs">{s.rule_id}</Code> },
              { header: t("findings.secretTitle"), render: (s) => <Text size="sm">{s.title}</Text> },
              {
                header: t("findings.file"),
                render: (s) => (
                  <Text size="xs" ff="monospace" style={{ wordBreak: "break-all" }}>
                    {s.target}
                    {s.line ? `:${s.line}` : ""}
                  </Text>
                ),
              },
            ]}
          />
        </Tabs.Panel>

        <Tabs.Panel value="packages" pt="sm">
          <TextInput
            size="xs"
            mb="sm"
            maw={320}
            placeholder={t("findings.filterPackages")}
            leftSection={<IconSearch size={14} />}
            value={filter}
            onChange={(e) => setFilter(e.currentTarget.value)}
          />
          <DataTable
            empty={t("findings.noPackages")}
            rows={allPackages}
            rowKey={(p) => p.key}
            columns={[
              {
                header: t("findings.package"),
                render: (p) => (
                  <Anchor component={Link} to={packagePath(p.key)} size="sm">
                    {p.name}
                  </Anchor>
                ),
              },
              { header: t("findings.version"), render: (p) => <Text size="xs" ff="monospace">{p.version}</Text> },
              { header: t("findings.type"), width: 110, render: (p) => <Badge size="xs" variant="light" color="gray">{p.type || "—"}</Badge> },
              {
                header: t("findings.vulnerabilitiesLabel"),
                width: 160,
                render: (p) => (
                  <SeverityCounts
                    counts={p.vulnerabilities.reduce((a: Record<string, number>, v) => ({ ...a, [v.severity]: (a[v.severity] || 0) + 1 }), {})}
                    empty="—"
                  />
                ),
              },
              {
                header: t("findings.where"),
                render: (p) => (
                  <Text size="xs" c="dimmed" style={{ wordBreak: "break-all" }}>
                    {p.targets.join(", ")}
                  </Text>
                ),
              },
            ]}
          />
        </Tabs.Panel>

        <Tabs.Panel value="running" pt="sm">
          <PlacementsTable placements={f.placements} />
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
}

/** A list of images that can be long: filterable by repository or namespace. */
export function FilterableImages({ images, total, limit }: { images: FindingImage[]; total?: number; limit?: number }) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [onlyRunning, setOnlyRunning] = useState("running");
  const running = images.filter((i) => i.placements.length > 0).length;
  const rows = images.filter(
    (i) =>
      (onlyRunning === "all" || i.placements.length > 0) &&
      (!q.trim() ||
        i.repo.toLowerCase().includes(q.trim().toLowerCase()) ||
        i.placements.some((p) => p.namespace.toLowerCase().includes(q.trim().toLowerCase()))),
  );
  return (
    <Stack gap="sm">
      <Group gap="sm" wrap="wrap">
        <SegmentedControl
          size="xs"
          value={onlyRunning}
          onChange={setOnlyRunning}
          data={[
            { value: "running", label: t("findings.runningImages", { count: running }) },
            { value: "all", label: t("findings.allImages", { count: total ?? images.length }) },
          ]}
        />
        <TextInput
          size="xs"
          placeholder={t("findings.filterImages")}
          leftSection={<IconSearch size={14} />}
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
        />
      </Group>
      {total !== undefined && limit !== undefined && total > images.length && onlyRunning === "all" && (
        <Text size="xs" c="dimmed">
          {t("findings.imagesTruncated", { shown: images.length, total })}
        </Text>
      )}
      {rows.length ? <ImagesTable images={rows} /> : <EmptyState message={t("findings.noImages")} />}
    </Stack>
  );
}
