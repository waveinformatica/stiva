import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Tabs, Pill } from "@mantine/core";
import {
  PageHeader,
  Card,
  Group,
  Text,
  Badge,
  Button,
  TextInput,
  Select,
  Loader,
  Alert,
  Stack,
  Code,
} from "../components/ui";
import { api } from "../lib/api";
import RepoBrowser, { registryEndpoint, type FocusRepo, type Reg } from "../components/RepoBrowser";
import { IconSearch, IconRefresh, IconFolder } from "../components/icons";

type Entry = { path: string; name: string; dir: boolean };
type SearchResult = {
  registry: string;
  format: string;
  path: string;
  name: string;
  kind: string;
};

// describeArtifact derives a human-friendly package/version/file triple from a
// stored object path, per format. The goal is a coherent "UI per formato" view
// rather than a raw flat file list.
function describeArtifact(format: string, p: string) {
  const segs = p.split("/").filter(Boolean);
  const file = segs[segs.length - 1] || p;
  let pkg = segs.length > 1 ? segs.slice(0, -1).join("/") : p;
  let version = "";
  if (format === "maven") {
    if (segs.length >= 3) {
      const group = segs.slice(0, segs.length - 3).join(".");
      const art = segs[segs.length - 3];
      pkg = group === "" ? art : group + ":" + art;
      version = segs[segs.length - 2];
    }
  } else if (
    format === "npm" ||
    format === "pypi" ||
    format === "nuget" ||
    format === "composer" ||
    format === "cran" ||
    format === "go" ||
    format === "conda" ||
    format === "rubygems"
  ) {
    if (segs.length >= 2) {
      pkg = segs.slice(0, -1).join("/");
      version = segs[segs.length - 2] || "";
    }
  } else if (format === "helm") {
    const m = file.match(/^(.*?)-([\d].*)\.(tgz|tar\.gz)$/);
    if (m) {
      pkg = m[1];
      version = m[2];
    }
  } else if (segs.length >= 2) {
    pkg = segs.slice(0, -1).join("/");
  }
  return { pkg, version, file };
}

export default function Explorer() {
  const { t } = useTranslation();
  const [regs, setRegs] = useState<Reg[]>([]);
  const [registry, setRegistry] = useState("");
  const [tab, setTab] = useState<string | null>("browse");
  const [focus, setFocus] = useState<FocusRepo | null>(null);

  useEffect(() => {
    api
      .registries()
      .then((r: any) => {
        setRegs(r.registries);
        if (!registry && r.registries.length) setRegistry(r.registries[0].name);
      })
      .catch(() => {});
  }, []);

  const selected = regs.find((r) => r.name === registry);
  const isOci = selected?.format === "oci";

  return (
    <div>
      <PageHeader
        title={t("explorer.title")}
        actions={
          <Select
            w={240}
            size="xs"
            placeholder={t("explorer.registryPlaceholder")}
            data={regs.map((r) => ({
              value: r.name,
              label: `${r.name} (${r.format})`,
            }))}
            value={registry}
            onChange={(v) => {
              if (!v) return;
              setRegistry(v);
              setFocus(null);
            }}
            allowDeselect={false}
          />
        }
      />
      <Tabs value={tab} onChange={setTab}>
        <Tabs.List mb="md">
          <Tabs.Tab value="browse" leftSection={<IconFolder size={14} />}>
            {t("explorer.browseTab")}
          </Tabs.Tab>
          <Tabs.Tab value="search" leftSection={<IconSearch size={14} />}>
            {t("explorer.searchTab")}
          </Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="browse">
          {selected ? (
            isOci ? (
              <RepoBrowser registry={registry} endpoint={registryEndpoint(selected)} focusRepo={focus} />
            ) : (
              <ArtifactBrowser
                registry={registry}
                format={selected.format}
                endpoint={registryEndpoint(selected)}
                basePath={selected.base_path}
              />
            )
          ) : (
            <Text c="dimmed" size="sm">
              {t("explorer.selectRegistry")}
            </Text>
          )}
        </Tabs.Panel>
        <Tabs.Panel value="search">
          <GlobalSearch
            regs={regs}
            onOpenRegistry={setRegistry}
            onBrowse={(repo) => {
              if (repo) setFocus({ repo, n: Date.now() });
              setTab("browse");
            }}
          />
        </Tabs.Panel>
      </Tabs>
    </div>
  );
}

function ArtifactBrowser({
  registry,
  format,
  endpoint,
  basePath,
}: {
  registry: string;
  format: string;
  endpoint?: string;
  basePath?: string;
}) {
  const { t } = useTranslation();
  const [entries, setEntries] = useState<Entry[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const load = () => {
    setLoading(true);
    setErr(null);
    api
      .browse(registry)
      .then((r: any) => setEntries(r.entries || []))
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, [registry]);

  const grouped = useMemo(() => {
    const map = new Map<string, { version: string; file: string; path: string }[]>();
    for (const e of entries) {
      const { pkg, version, file } = describeArtifact(format, e.path);
      if (!map.has(pkg)) map.set(pkg, []);
      map.get(pkg)!.push({ version, file, path: e.path });
    }
    return Array.from(map.entries()).sort((a, b) => a[0].localeCompare(b[0]));
  }, [entries, format]);

  return (
    <div>
      <Group mb="md">
        <Badge color="blue">{format}</Badge>
        {endpoint ? (
          <Code style={{ overflowWrap: "anywhere" }}>
            https://{endpoint}
            {basePath ? `/${basePath}` : ""}/
          </Code>
        ) : null}
        <Button variant="default" size="xs" leftSection={<IconRefresh size={14} />} onClick={load}>
          {t("common.refresh")}
        </Button>
      </Group>
      {err && <Alert color="red" mb="md" title={t("common.error")}>{err}</Alert>}
      {loading && <Loader />}
      {!loading && grouped.length === 0 && (
        <Text c="dimmed" size="sm">
          {t("explorer.noArtifacts")}
        </Text>
      )}
      <Stack gap="xs">
        {grouped.map(([pkg, files]) => (
          <Card key={pkg} withBorder p="xs">
            <Group justify="space-between" mb={4}>
              <Text fw={600} size="sm">
                {pkg}
              </Text>
              <Badge variant="light" color="gray">
                {t("explorer.fileCount", { count: files.length })}
              </Badge>
            </Group>
            <Stack gap={2}>
              {files.map((f) => (
                <Group key={f.path} gap="xs" justify="space-between">
                  <Group gap="xs">
                    {f.version && <Pill size="sm">{f.version}</Pill>}
                    <Code>{f.file}</Code>
                  </Group>
                  <Button
                    component="a"
                    href={api.artifactUrl(registry, f.path)}
                    size="compact-xs"
                    variant="subtle"
                    target="_blank"
                  >
                    {t("common.download")}
                  </Button>
                </Group>
              ))}
            </Stack>
          </Card>
        ))}
      </Stack>
    </div>
  );
}

function GlobalSearch({
  regs,
  onOpenRegistry,
  onBrowse,
}: {
  regs: Reg[];
  onOpenRegistry: (name: string) => void;
  onBrowse: (repo?: string) => void;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [format, setFormat] = useState<string | null>(null);
  const [results, setResults] = useState<SearchResult[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [searched, setSearched] = useState(false);

  const formats = useMemo(() => {
    const set = new Set(regs.map((r) => r.format));
    return Array.from(set).sort();
  }, [regs]);

  const run = () => {
    if (!q.trim()) return;
    setLoading(true);
    setErr(null);
    setSearched(true);
    api
      .search(q.trim(), format || undefined)
      .then((r: any) => setResults(r.results || []))
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };

  return (
    <div>
      <Group mb="md">
        <TextInput
          style={{ flex: 1, minWidth: 200 }}
          placeholder={t("explorer.searchPlaceholder")}
          leftSection={<IconSearch size={16} />}
          value={q}
          onChange={(e) => setQ(e.currentTarget.value)}
          onKeyDown={(e) => e.key === "Enter" && run()}
        />
        <Select
          w={180}
          size="xs"
          placeholder={t("explorer.formatPlaceholder")}
          clearable
          data={formats}
          value={format}
          onChange={setFormat}
        />
        <Button size="xs" onClick={run} leftSection={<IconSearch size={14} />}>
          {t("common.search")}
        </Button>
      </Group>
      {err && <Alert color="red" mb="md" title={t("common.error")}>{err}</Alert>}
      {loading && <Loader />}
      {searched && !loading && results.length === 0 && (
        <Text c="dimmed" size="sm">
          {t("explorer.noMatches", { query: q })}
        </Text>
      )}
      <Stack gap="xs">
        {results.map((r, i) => (
          <Card key={i} withBorder p="xs">
            <Group justify="space-between">
              <Group gap="xs">
                <Badge color="blue">{r.format}</Badge>
                <Badge variant="light" color="gray">
                  {r.registry}
                </Badge>
                <Code style={{ overflowWrap: "anywhere" }}>{r.name}</Code>
              </Group>
              {r.kind === "artifact" ? (
                <Button
                  component="a"
                  href={api.artifactUrl(r.registry, r.path)}
                  size="compact-xs"
                  variant="subtle"
                  target="_blank"
                >
                  {t("common.download")}
                </Button>
              ) : (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  onClick={() => {
                    onOpenRegistry(r.registry);
                    onBrowse(r.kind === "repo" ? r.name : undefined);
                  }}
                >
                  {t("common.browse")}
                </Button>
              )}
            </Group>
            <Text size="xs" c="dimmed">
              {r.path}
            </Text>
          </Card>
        ))}
      </Stack>
    </div>
  );
}
