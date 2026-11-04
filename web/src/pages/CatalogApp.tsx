import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  ActionIcon, Anchor, Badge, Button, Card, Code, Collapse, CopyButton, Group, Image, Modal, ScrollArea, Stack, Text,
  Title, Tooltip,
} from "@mantine/core";
import { api, downloadArtifact, fetchImageUrl } from "../lib/api";
import { EmptyState, ErrorAlert, PageLoader } from "../components/ui";
import { IconCopy } from "../components/icons";
import AppIcon from "../components/AppIcon";
import Markdown from "../components/Markdown";

type FileV = { path: string; size: number };
type VersionV = { version: string; created_at: number; primary: string; files: FileV[] };
type PackageV = { name: string; latest: VersionV; others?: VersionV[] };
type Artifact = {
  registry: string;
  format: string;
  type: string;
  ref: string;
  tag: string;
  label: string;
  kind: "image" | "files";
  pull_host?: string;
  latest_tag?: string;
  tags?: { tag: string; digest: string; pushed_at: number }[];
  packages?: PackageV[];
  files?: FileV[];
  more_files?: number;
};
type Shot = { id: number; caption: string };
type AppDetail = {
  slug: string;
  name: string;
  tagline: string;
  description: string;
  icon: string;
  category: string;
  homepage: string;
  artifacts: Artifact[];
  screenshots: Shot[];
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

const base = (p: string) => p.split("/").pop() || p;
const isPre = (v: string) => /[-][A-Za-z]/.test(v.replace(/^v/i, ""));
const pullCmd = (a: Artifact) => `docker pull ${a.pull_host}/${a.ref}:${a.latest_tag || a.tag || "latest"}`;

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

function VersionRow({ registry, v, onError }: { registry: string; v: VersionV; onError: (m: string) => void }) {
  const { t } = useTranslation();
  return (
    <Group justify="space-between" wrap="nowrap" gap="xs">
      <Stack gap={0} style={{ minWidth: 0 }}>
        <Group gap={6}>
          <Text size="sm" fw={500}>
            {v.version}
          </Text>
          {isPre(v.version) && (
            <Badge size="xs" variant="outline" color="gray">
              {t("catalog.prerelease")}
            </Badge>
          )}
        </Group>
        <Text size="xs" c="dimmed" truncate>
          {base(v.primary)}
          {v.files.find((f) => f.path === v.primary)?.size ? ` · ${size(v.files.find((f) => f.path === v.primary)!.size)}` : ""}
        </Text>
      </Stack>
      <Button size="compact-xs" variant="light" onClick={() => downloadArtifact(registry, v.primary).catch((e) => onError(e.message))}>
        {t("catalog.download")}
      </Button>
    </Group>
  );
}

function PackageBlock({ registry, p, showName, onError }: { registry: string; p: PackageV; showName: boolean; onError: (m: string) => void }) {
  const { t } = useTranslation();
  const [more, setMore] = useState(false);
  const extra = p.latest.files.filter((f) => f.path !== p.latest.primary && !/\.(sha\d+|md5|asc|sig)$/.test(f.path));
  return (
    <Stack gap="xs">
      {showName && (
        <Text size="sm" fw={600}>
          {base(p.name)}
        </Text>
      )}
      <Card padding="sm" withBorder style={{ background: "var(--mantine-color-default-hover)" }}>
        <Text size="xs" c="dimmed" mb={4}>
          {t("catalog.latestVersion")}
        </Text>
        <VersionRow registry={registry} v={p.latest} onError={onError} />
        {extra.length > 0 && (
          <Stack gap={2} mt="xs">
            <Text size="xs" c="dimmed">
              {t("catalog.allFilesOfVersion")}
            </Text>
            {extra.map((f) => (
              <Group key={f.path} justify="space-between" wrap="nowrap">
                <Text size="xs" truncate>
                  {base(f.path)} {f.size ? <Text span c="dimmed">· {size(f.size)}</Text> : null}
                </Text>
                <Button size="compact-xs" variant="subtle" onClick={() => downloadArtifact(registry, f.path).catch((e) => onError(e.message))}>
                  {t("catalog.download")}
                </Button>
              </Group>
            ))}
          </Stack>
        )}
      </Card>
      {(p.others?.length ?? 0) > 0 && (
        <>
          <Anchor component="button" type="button" size="xs" onClick={() => setMore(!more)} style={{ alignSelf: "flex-start" }}>
            {more ? t("catalog.hideVersions") : t("catalog.showVersions", { count: p.others!.length })}
          </Anchor>
          <Collapse in={more}>
            <Stack gap="xs" pl="xs">
              {p.others!.map((v) => (
                <VersionRow key={v.version} registry={registry} v={v} onError={onError} />
              ))}
            </Stack>
          </Collapse>
        </>
      )}
    </Stack>
  );
}

function ArtifactCard({ a }: { a: Artifact }) {
  const { t } = useTranslation();
  const [err, setErr] = useState<string | null>(null);
  return (
    <Card padding="md">
      <Group justify="space-between" mb="xs" wrap="nowrap">
        <Stack gap={0} style={{ minWidth: 0 }}>
          <Text fw={600} truncate>
            {a.label || a.ref}
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
              <CopyLine value={pullCmd(a)} />
            </>
          ) : (
            <Text size="sm" c="dimmed">
              {t("catalog.noPullAddress")} <Code>{a.ref}</Code>
            </Text>
          )}
          {a.latest_tag && (
            <Group gap={6}>
              <Text size="xs" c="dimmed">
                {t("catalog.latestTag")}
              </Text>
              <Badge variant="filled" color="teal">
                {a.latest_tag}
              </Badge>
            </Group>
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
      ) : a.packages && a.packages.length > 0 ? (
        <Stack gap="md">
          {a.packages.map((p) => (
            <PackageBlock key={p.name} registry={a.registry} p={p} showName={a.packages!.length > 1} onError={setErr} />
          ))}
        </Stack>
      ) : (
        <Stack gap={4}>
          {(a.files || []).map((f) => (
            <Group key={f.path} justify="space-between" wrap="nowrap" gap="xs">
              <Text size="sm" truncate>
                {base(f.path)}
              </Text>
              <Button size="compact-xs" variant="light" onClick={() => downloadArtifact(a.registry, f.path).catch((e) => setErr(e.message))}>
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

// The one thing most visitors came for: the newest version, one click away.
function PrimaryAction({ app, onError }: { app: AppDetail; onError: (m: string) => void }) {
  const { t } = useTranslation();
  for (const a of app.artifacts) {
    const p = a.packages?.[0];
    if (a.kind === "files" && p) {
      return (
        <Button size="md" onClick={() => downloadArtifact(a.registry, p.latest.primary).catch((e) => onError(e.message))}>
          {t("catalog.downloadVersion", { version: p.latest.version })}
        </Button>
      );
    }
  }
  const img = app.artifacts.find((a) => a.kind === "image" && a.pull_host);
  if (img) {
    return (
      <CopyButton value={pullCmd(img)} timeout={1800}>
        {({ copied, copy }) => (
          <Button size="md" color={copied ? "teal" : undefined} onClick={copy}>
            {copied ? t("catalog.copied") : t("catalog.copyPull")}
          </Button>
        )}
      </CopyButton>
    );
  }
  return null;
}

// Screenshots are fetched with the caller's credentials, one blob per image.
function Gallery({ slug, shots }: { slug: string; shots: Shot[] }) {
  const { t } = useTranslation();
  const [urls, setUrls] = useState<Record<number, string>>({});
  const [open, setOpen] = useState<number | null>(null);

  useEffect(() => {
    let live = true;
    const made: string[] = [];
    shots.forEach((s) =>
      fetchImageUrl(slug, s.id)
        .then((u) => {
          if (!live) return URL.revokeObjectURL(u);
          made.push(u);
          setUrls((cur) => ({ ...cur, [s.id]: u }));
        })
        .catch(() => {}),
    );
    return () => {
      live = false;
      made.forEach((u) => URL.revokeObjectURL(u));
    };
  }, [slug, shots.map((s) => s.id).join(",")]);

  const go = (d: number) => setOpen((i) => (i === null ? i : (i + d + shots.length) % shots.length));

  return (
    <section>
      <Title order={4} mb="sm">
        {t("catalog.screenshots")}
      </Title>
      <ScrollArea type="auto" offsetScrollbars>
        <Group gap="md" wrap="nowrap" pb="xs" align="flex-start">
          {shots.map((s, i) => (
            <Stack key={s.id} gap={4} style={{ flex: "0 0 auto" }}>
              <button
                type="button"
                onClick={() => setOpen(i)}
                aria-label={s.caption || `${t("catalog.screenshots")} ${i + 1}`}
                style={{ all: "unset", cursor: "zoom-in", display: "block", borderRadius: 8, overflow: "hidden", border: "1px solid var(--mantine-color-default-border)" }}
              >
                {urls[s.id] ? (
                  <Image src={urls[s.id]} alt={s.caption} h={200} w="auto" fit="contain" />
                ) : (
                  <div style={{ height: 200, width: 320, background: "var(--mantine-color-default-hover)" }} />
                )}
              </button>
              {s.caption && (
                <Text size="xs" c="dimmed" maw={320} lineClamp={2}>
                  {s.caption}
                </Text>
              )}
            </Stack>
          ))}
        </Group>
      </ScrollArea>

      <Modal
        opened={open !== null}
        onClose={() => setOpen(null)}
        size="auto"
        centered
        withCloseButton
        title={open !== null ? shots[open].caption || `${open + 1} / ${shots.length}` : ""}
        onKeyDown={(e) => {
          if (e.key === "ArrowRight") go(1);
          if (e.key === "ArrowLeft") go(-1);
        }}
      >
        {open !== null && (
          <Stack align="center" gap="sm">
            {urls[shots[open].id] && <Image src={urls[shots[open].id]} alt={shots[open].caption} mah="70vh" maw="86vw" fit="contain" />}
            {shots.length > 1 && (
              <Group>
                <Button variant="default" size="xs" onClick={() => go(-1)}>
                  ← {t("catalog.previous")}
                </Button>
                <Text size="xs" c="dimmed">
                  {open + 1} / {shots.length}
                </Text>
                <Button variant="default" size="xs" onClick={() => go(1)}>
                  {t("catalog.next")} →
                </Button>
              </Group>
            )}
          </Stack>
        )}
      </Modal>
    </section>
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
    setApp(null);
    api
      .catalogApp(slug)
      .then((a: AppDetail) => setApp({ ...a, screenshots: a.screenshots || [] }))
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
          <ErrorAlert onClose={() => setErr(null)}>{err}</ErrorAlert>
        </div>
      )}
      {missing && <EmptyState message={t("catalog.notFound")} />}
      {app && (
        <Stack gap="xl" mt="md">
          <Group wrap="nowrap" align="flex-start" gap="lg">
            <AppIcon name={app.name} icon={app.icon} size={88} />
            <Stack gap={8} style={{ minWidth: 0 }}>
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
              <div style={{ marginTop: 4 }}>
                <PrimaryAction app={app} onError={setErr} />
              </div>
            </Stack>
          </Group>

          {app.screenshots.length > 0 && <Gallery slug={app.slug} shots={app.screenshots} />}

          <div style={{ display: "grid", gap: "var(--mantine-spacing-xl)", gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))", alignItems: "start" }}>
            <Stack gap="sm">
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
