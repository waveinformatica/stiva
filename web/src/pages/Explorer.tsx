import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Tabs } from "@mantine/core";
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
  
  Stack,
  Code, ErrorAlert,
} from "../components/ui";
import { api, downloadArtifact } from "../lib/api";
import PackageBrowser from "../components/PackageBrowser";
import RepoBrowser, { registryEndpoint, type FocusRepo, type Reg } from "../components/RepoBrowser";
import { IconSearch, IconFolder } from "../components/icons";

type SearchResult = {
  registry: string;
  format: string;
  path: string;
  name: string;
  kind: string;
};

export default function Explorer({ anonymous = false }: { anonymous?: boolean }) {
  const { t } = useTranslation();
  const [regs, setRegs] = useState<Reg[]>([]);
  const [registry, setRegistry] = useState("");
  const [tab, setTab] = useState<string | null>("browse");
  const [focus, setFocus] = useState<FocusRepo | null>(null);
  // Reachable pull host resolved server-side; null while loading, "" when no
  // route reaches the registry (then no pull reference is shown at all).
  const [pullHost, setPullHost] = useState<string | null>(null);

  useEffect(() => {
    api
      .registries()
      .then((r: any) => {
        setRegs(r.registries);
        if (!registry && r.registries.length) setRegistry(r.registries[0].name);
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!registry) {
      setPullHost(null);
      return;
    }
    let live = true;
    setPullHost(null);
    api
      .pullRoute(registry)
      .then((r: any) => {
        if (live) setPullHost(r.host || "");
      })
      .catch(() => {
        if (live) setPullHost("");
      });
    return () => {
      live = false;
    };
  }, [registry]);

  const selected = regs.find((r) => r.name === registry);
  const isOci = selected?.format === "oci";

  return (
    <div>
      <PageHeader
        title={t("explorer.title")}
        description={t("explorer.description")}
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
              <RepoBrowser
                registry={registry}
                endpoint={registryEndpoint(selected)}
                pullHost={pullHost}
                focusRepo={focus}
              />
            ) : (
              <PackageBrowser registry={registry} format={selected.format} anonymous={anonymous} />
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
      {err && <ErrorAlert>{err}</ErrorAlert>}
      {loading && <Loader />}
      {searched && !loading && results.length === 0 && (
        <Text c="dimmed" size="sm">
          {t("explorer.noMatches", { query: q })}
        </Text>
      )}
      <Stack gap="xs">
        {results.map((r, i) => (
          <Card key={i} padding="sm">
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
                  size="compact-xs"
                  variant="subtle"
                  onClick={() => downloadArtifact(r.registry, r.path).catch((e) => setErr(e.message))}
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
