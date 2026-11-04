import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Anchor, Badge, Button, Card, Code, CopyButton, Group, Stack, Text, Title, Tooltip, ActionIcon } from "@mantine/core";
import { api, downloadArtifact } from "../lib/api";
import { EmptyState, ErrorAlert, PageLoader } from "../components/ui";
import { IconCopy } from "../components/icons";
import AppIcon from "../components/AppIcon";
import Markdown from "../components/Markdown";

type Artifact = {
  registry: string;
  format: string;
  type: string;
  ref: string;
  tag: string;
  label: string;
  kind: "image" | "files";
  pull_host?: string;
  tags?: { tag: string; digest: string; pushed_at: number }[];
  files?: { path: string; size: number }[];
  more_files?: number;
};

type AppDetail = {
  slug: string;
  name: string;
  tagline: string;
  description: string;
  icon: string;
  category: string;
  homepage: string;
  artifacts: Artifact[];
};

function size(n: number) {
  if (!n) return "";
  if (n < 1024) return `${n} B`;
  const u = ["kB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${u[i]}`;
}

function CopyLine({ value }: { value: string }) {
  const { t } = useTranslation();
  return (
    <Group gap={6} wrap="nowrap" align="center">
      <Code block style={{ flex: 1, overflowX: "auto", whiteSpace: "pre" }}>
        {value}
      </Code>
      <CopyButton value={value} timeout={1500}>
        {({ copied, copy }) => (
          <Tooltip label={copied ? t("catalog.copied") : t("catalog.copy")} withArrow>
            <ActionIcon variant="subtle" color={copied ? "teal" : "gray"} onClick={copy} aria-label={t("catalog.copy")}>
              <IconCopy size={15} />
            </ActionIcon>
          </Tooltip>
        )}
      </CopyButton>
    </Group>
  );
}

function ArtifactCard({ a }: { a: Artifact }) {
  const { t } = useTranslation();
  const [err, setErr] = useState<string | null>(null);
  const title = a.label || a.ref;
  return (
    <Card padding="md">
      <Group justify="space-between" mb="xs" wrap="nowrap">
        <Stack gap={0} style={{ minWidth: 0 }}>
          <Text fw={600} truncate>
            {title}
          </Text>
          <Text size="xs" c="dimmed">
            {t(a.kind === "image" ? "catalog.kindImage" : "catalog.kindFiles")} · {a.registry}
          </Text>
        </Stack>
        <Badge variant="outline" color="gray">
          {a.format}
        </Badge>
      </Group>

      {err && <ErrorAlert onClose={() => setErr(null)}>{err}</ErrorAlert>}

      {a.kind === "image" ? (
        <Stack gap="xs">
          {a.pull_host ? (
            <>
              <Text size="xs" c="dimmed">
                {t("catalog.pullCommand")}
              </Text>
              <CopyLine value={`docker pull ${a.pull_host}/${a.ref}:${a.tag || a.tags?.[0]?.tag || "latest"}`} />
            </>
          ) : (
            <Text size="sm" c="dimmed">
              {t("catalog.noPullAddress")} <Code>{a.ref}</Code>
            </Text>
          )}
          {(a.tags?.length ?? 0) > 0 && (
            <>
              <Text size="xs" c="dimmed">
                {t("catalog.recentTags")}
              </Text>
              <Group gap={6}>
                {a.tags!.map((x) => (
                  <Badge key={x.tag} variant="light" color="teal">
                    {x.tag}
                  </Badge>
                ))}
              </Group>
            </>
          )}
        </Stack>
      ) : (
        <Stack gap={4}>
          {(a.files || []).map((f) => (
            <Group key={f.path} justify="space-between" wrap="nowrap" gap="xs">
              <Stack gap={0} style={{ minWidth: 0 }}>
                <Text size="sm" truncate>
                  {f.path.split("/").pop()}
                </Text>
                {f.size > 0 && (
                  <Text size="xs" c="dimmed">
                    {size(f.size)}
                  </Text>
                )}
              </Stack>
              <Button
                size="compact-xs"
                variant="light"
                onClick={() => downloadArtifact(a.registry, f.path).catch((e) => setErr(e.message))}
              >
                {t("catalog.download")}
              </Button>
            </Group>
          ))}
          {!!a.more_files && (
            <Text size="xs" c="dimmed">
              {t("catalog.moreFiles", { count: a.more_files })}
            </Text>
          )}
        </Stack>
      )}
    </Card>
  );
}

export default function CatalogApp() {
  const { slug = "" } = useParams();
  const { t } = useTranslation();
  const [app, setApp] = useState<AppDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [missing, setMissing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    setLoading(true);
    setMissing(false);
    api
      .catalogApp(slug)
      .then(setApp)
      .catch((e) => (e.status === 404 ? setMissing(true) : setErr(e.message)))
      .finally(() => setLoading(false));
  }, [slug]);

  if (loading) return <PageLoader />;

  return (
    <div>
      <Anchor component={Link} to="/" size="sm">
        ← {t("catalog.back")}
      </Anchor>
      {err && (
        <div style={{ marginTop: "var(--mantine-spacing-md)" }}>
          <ErrorAlert>{err}</ErrorAlert>
        </div>
      )}
      {missing && <EmptyState message={t("catalog.notFound")} />}
      {app && (
        <Stack gap="xl" mt="md">
          <Group wrap="nowrap" align="flex-start" gap="lg">
            <AppIcon name={app.name} icon={app.icon} size={88} />
            <Stack gap={6} style={{ minWidth: 0 }}>
              <Title order={2}>{app.name}</Title>
              {app.tagline && <Text c="dimmed">{app.tagline}</Text>}
              <Group gap="xs">
                {app.category && (
                  <Badge variant="light" color="indigo">
                    {app.category}
                  </Badge>
                )}
                {app.homepage && (
                  <Anchor href={app.homepage} target="_blank" rel="noopener noreferrer" size="sm">
                    {t("catalog.website")} ↗
                  </Anchor>
                )}
              </Group>
            </Stack>
          </Group>

          <div style={{ display: "grid", gap: "var(--mantine-spacing-xl)", gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))", alignItems: "start" }}>
            <Stack gap="sm" style={{ gridColumn: "span 1" }}>
              <Title order={4}>{t("catalog.about")}</Title>
              {app.description ? <Markdown source={app.description} /> : <Text c="dimmed">—</Text>}
            </Stack>
            <Stack gap="sm">
              <Title order={4}>{t("catalog.getIt")}</Title>
              {app.artifacts.map((a, i) => (
                <ArtifactCard key={i} a={a} />
              ))}
            </Stack>
          </div>
        </Stack>
      )}
    </div>
  );
}
