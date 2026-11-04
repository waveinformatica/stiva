import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Button, Card, Code, Group, Loader, ScrollArea, Stack, Tabs, Text, TextInput } from "@mantine/core";
import { api } from "../lib/api";
import { ErrorAlert } from "./ui";
import { IconRefresh, IconSearch } from "./icons";
import { SnippetTabs, VersionList, fmtSize, isPre, type Snippet, type VersionV } from "./ArtifactParts";

type Row = { name: string; latest: string; versions: number; files: number; size: number; updated_at: number };
type Detail = {
  name: string;
  format: string;
  endpoint: string;
  setup: Snippet[];
  versions: VersionV[];
  total_versions: number;
};

// Browser for the packages of a path-addressed registry (Maven, npm, PyPI, Go,
// Helm, APT … and raw files). Packages and versions come from the server, which
// reads the stored paths the way each format lays them out, so the same view
// fits every format: packages on the left; versions, files and how to use them
// on the right.
export default function PackageBrowser({ registry, format }: { registry: string; format: string }) {
  const { t } = useTranslation();
  const [rows, setRows] = useState<Row[]>([]);
  const [endpoint, setEndpoint] = useState("");
  const [q, setQ] = useState("");
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const load = () => {
    setLoading(true);
    setErr(null);
    api
      .packages(registry)
      .then((r: any) => {
        setRows(r.packages || []);
        setEndpoint(r.endpoint || "");
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(() => {
    setSelected(null);
    setDetail(null);
    load();
  }, [registry]);

  useEffect(() => {
    if (!selected) {
      setDetail(null);
      return;
    }
    let live = true;
    setDetailLoading(true);
    api
      .package(registry, selected)
      .then((d: Detail) => live && setDetail(d))
      .catch((e) => live && setErr(e.message))
      .finally(() => live && setDetailLoading(false));
    return () => {
      live = false;
    };
  }, [registry, selected]);

  const shown = useMemo(() => {
    const n = q.trim().toLowerCase();
    return n ? rows.filter((r) => r.name.toLowerCase().includes(n)) : rows;
  }, [rows, q]);

  const latest = detail?.versions[0];

  return (
    <div>
      <Group mb="md" gap="sm">
        <Badge color="blue">{format}</Badge>
        {endpoint && <Code style={{ overflowWrap: "anywhere" }}>{endpoint}/</Code>}
        <Button variant="default" size="xs" leftSection={<IconRefresh size={14} />} onClick={load} loading={loading}>
          {t("common.refresh")}
        </Button>
      </Group>
      {err && <ErrorAlert onClose={() => setErr(null)}>{err}</ErrorAlert>}

      <div style={{ display: "grid", gap: "var(--mantine-spacing-md)", gridTemplateColumns: "minmax(240px, 340px) minmax(0, 1fr)", alignItems: "start" }} className="stiva-pkg-grid">
        <Card padding="sm">
          <TextInput
            size="xs"
            leftSection={<IconSearch size={14} />}
            placeholder={t("explorer.searchPackages")}
            value={q}
            onChange={(e) => setQ(e.currentTarget.value)}
            mb="xs"
          />
          <Text size="xs" c="dimmed" mb={4}>
            {t("explorer.packagesCount", { count: shown.length })}
          </Text>
          {loading && rows.length === 0 ? (
            <Loader size="sm" />
          ) : shown.length === 0 ? (
            <Text size="sm" c="dimmed">
              {t("explorer.noPackages")}
            </Text>
          ) : (
            <ScrollArea.Autosize mah="70vh">
              <Stack gap={2}>
                {shown.map((r) => (
                  <button
                    key={r.name}
                    type="button"
                    onClick={() => setSelected(r.name)}
                    style={{
                      all: "unset",
                      cursor: "pointer",
                      padding: "6px 8px",
                      borderRadius: 6,
                      background: selected === r.name ? "var(--mantine-primary-color-light)" : undefined,
                    }}
                  >
                    <Text size="sm" fw={selected === r.name ? 650 : 500} style={{ wordBreak: "break-all" }}>
                      {r.name}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {r.latest} · {t("explorer.versionsCount", { count: r.versions })}
                      {r.size ? ` · ${fmtSize(r.size)}` : ""}
                    </Text>
                  </button>
                ))}
              </Stack>
            </ScrollArea.Autosize>
          )}
        </Card>

        <Card padding="md" miw={0}>
          {!selected ? (
            <Text size="sm" c="dimmed">
              {t("explorer.selectPackage")}
            </Text>
          ) : detailLoading && !detail ? (
            <Loader size="sm" />
          ) : detail ? (
            <Stack gap="sm">
              <Group justify="space-between" wrap="nowrap" align="flex-start">
                <Stack gap={2} style={{ minWidth: 0 }}>
                  <Text fw={650} fz="lg" style={{ wordBreak: "break-all" }}>
                    {detail.name}
                  </Text>
                  <Group gap="xs">
                    {latest && (
                      <Badge color="teal" variant="light">
                        {t("explorer.latestVersion", { version: latest.version })}
                      </Badge>
                    )}
                    {latest && isPre(latest.version) && (
                      <Badge variant="outline" color="gray">
                        {t("catalog.prerelease")}
                      </Badge>
                    )}
                    <Text size="xs" c="dimmed">
                      {t("explorer.versionsCount", { count: detail.total_versions })}
                    </Text>
                  </Group>
                </Stack>
              </Group>
              <Tabs defaultValue="versions" keepMounted={false}>
                <Tabs.List mb="sm">
                  <Tabs.Tab value="versions">{t("explorer.tabVersions")}</Tabs.Tab>
                  <Tabs.Tab value="integrate">{t("catalog.tabIntegrate")}</Tabs.Tab>
                </Tabs.List>
                <Tabs.Panel value="versions">
                  <VersionList registry={registry} versions={detail.versions} onError={setErr} />
                  {detail.total_versions > detail.versions.length && (
                    <Text size="xs" c="dimmed" mt="xs">
                      {t("explorer.moreVersions", { shown: detail.versions.length, total: detail.total_versions })}
                    </Text>
                  )}
                </Tabs.Panel>
                <Tabs.Panel value="integrate">
                  <Stack gap="lg">
                    {detail.setup.length > 0 && (
                      <Stack gap="xs">
                        <Text fw={600} size="sm">
                          {t("catalog.setupTitle")}
                        </Text>
                        <SnippetTabs items={detail.setup} />
                      </Stack>
                    )}
                    {latest && (latest.usage?.length ?? 0) > 0 && (
                      <Stack gap="xs">
                        <Text fw={600} size="sm">
                          {t("catalog.usageTitle", { name: detail.name.split("/").pop(), version: latest.version })}
                        </Text>
                        <SnippetTabs items={latest.usage!} />
                      </Stack>
                    )}
                    {detail.setup.length === 0 && (latest?.usage?.length ?? 0) === 0 && (
                      <Text size="sm" c="dimmed">
                        {t("catalog.noIntegration")}
                      </Text>
                    )}
                  </Stack>
                </Tabs.Panel>
              </Tabs>
            </Stack>
          ) : null}
        </Card>
      </div>
    </div>
  );
}
