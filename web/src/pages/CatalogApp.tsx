import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Anchor, Badge, Button, Card, Code, Collapse, CopyButton, Group, Image, Modal, ScrollArea, Stack, Tabs, Text, Title } from "@mantine/core";
import { api, downloadArtifact, fetchImageUrl } from "../lib/api";
import { CopyBlock, FilesTable, SnippetTabs, baseName, fmtSize, isPre, type FileV, type Snippet, type VersionV } from "../components/ArtifactParts";
import { EmptyState, ErrorAlert, PageLoader } from "../components/ui";
import AppIcon from "../components/AppIcon";
import Markdown from "../components/Markdown";

type PackageV = {
  name: string;
  latest: VersionV;
  others?: VersionV[];
  usage?: Snippet[];
  download?: Snippet[];
  unversioned?: boolean;
};
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
  all_files?: FileV[];
  total_files?: number;
  endpoint?: string;
  setup?: Snippet[];
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

const size = fmtSize;
const base = baseName;
const pullCmd = (a: Artifact) => `docker pull ${a.pull_host}/${a.ref}:${a.latest_tag || a.tag || "latest"}`;

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
  if (p.unversioned) {
    // Plain files with no version in the name: list them, with no "latest".
    return (
      <Stack gap="xs">
        <Text size="sm" fw={600}>
          {t("catalog.otherFiles")}
        </Text>
        {p.latest.files.map((f) => (
          <Group key={f.path} justify="space-between" wrap="nowrap">
            <Text size="sm" truncate>
              {base(f.path)} {f.size ? <Text span c="dimmed">· {size(f.size)}</Text> : null}
            </Text>
            <Button size="compact-xs" variant="subtle" onClick={() => downloadArtifact(registry, f.path).catch((e) => onError(e.message))}>
              {t("catalog.download")}
            </Button>
          </Group>
        ))}
      </Stack>
    );
  }
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

function TagList({ tags }: { tags: NonNullable<Artifact["tags"]> }) {
  const { t } = useTranslation();
  const [all, setAll] = useState(false);
  const shown = all ? tags : tags.slice(0, 6);
  return (
    <>
      <Text size="xs" c="dimmed">
        {t("catalog.recentTags")}
      </Text>
      <Group gap={6}>
        {shown.map((x) => (
          <Badge key={x.tag} variant="light" color="teal">
            {x.tag}
          </Badge>
        ))}
      </Group>
      {tags.length > 6 && (
        <Anchor component="button" type="button" size="xs" onClick={() => setAll(!all)} style={{ alignSelf: "flex-start" }}>
          {all ? t("catalog.hideAllTags") : t("catalog.showAllTags", { count: tags.length })}
        </Anchor>
      )}
    </>
  );
}

// One section per artifact the visitor can see, with what that kind of artifact
// needs: for an image how to pull and run it, for files the versions to
// download and how to wire the registry into a build tool.
function ArtifactSection({ a }: { a: Artifact }) {
  const { t } = useTranslation();
  const [err, setErr] = useState<string | null>(null);
  const isImage = a.kind === "image";
  const pkgs = a.packages || [];
  const setup = a.setup || [];
  const hasIntegrate = setup.length > 0 || pkgs.some((p) => (p.usage?.length ?? 0) + (p.download?.length ?? 0) > 0);

  return (
    <Card padding="lg">
      <Group justify="space-between" mb="sm" wrap="nowrap" align="flex-start">
        <Stack gap={0} style={{ minWidth: 0 }}>
          <Title order={5}>{a.label || a.ref}</Title>
          <Text size="xs" c="dimmed">
            {t(isImage ? "catalog.kindImage" : "catalog.kindFiles")} · {a.registry}
            {a.type !== "hosted" ? ` (${a.type})` : ""}
          </Text>
        </Stack>
        <Badge variant="outline" color="gray">
          {a.format}
        </Badge>
      </Group>

      {err && <ErrorAlert onClose={() => setErr(null)}>{err}</ErrorAlert>}

      <Tabs defaultValue={isImage ? "pull" : "download"} keepMounted={false}>
        <Tabs.List mb="md">
          {isImage ? (
            <Tabs.Tab value="pull">{t("catalog.tabPull")}</Tabs.Tab>
          ) : (
            <>
              <Tabs.Tab value="download">{t("catalog.tabDownload")}</Tabs.Tab>
              <Tabs.Tab value="files">
                {t("catalog.tabFiles")} ({a.total_files ?? a.all_files?.length ?? 0})
              </Tabs.Tab>
            </>
          )}
          <Tabs.Tab value="integrate">{t("catalog.tabIntegrate")}</Tabs.Tab>
        </Tabs.List>

        {isImage ? (
          <Tabs.Panel value="pull">
            <Stack gap="xs">
              {a.pull_host ? (
                <>
                  <Text size="xs" c="dimmed">
                    {t("catalog.pullCommand")}
                  </Text>
                  <CopyBlock code={pullCmd(a)} />
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
              {(a.tags?.length ?? 0) > 0 && <TagList tags={a.tags!} />}
            </Stack>
          </Tabs.Panel>
        ) : (
          <>
            <Tabs.Panel value="download">
              {pkgs.length > 0 ? (
                <Stack gap="md">
                  {pkgs.map((p) => (
                    <PackageBlock key={p.name} registry={a.registry} p={p} showName={pkgs.length > 1} onError={setErr} />
                  ))}
                </Stack>
              ) : (
                <Text size="sm" c="dimmed">
                  {t("catalog.noMatch")}
                </Text>
              )}
            </Tabs.Panel>
            <Tabs.Panel value="files">
              <FilesTable registry={a.registry} files={a.all_files || []} onError={setErr} />
              {(a.total_files ?? 0) > (a.all_files?.length ?? 0) && (
                <Text size="xs" c="dimmed" mt="xs">
                  {t("catalog.truncatedFiles", { shown: a.all_files!.length, total: a.total_files })}{" "}
                  <Anchor component={Link} to="/explorer" size="xs">
                    {t("catalog.openInExplorer")}
                  </Anchor>
                </Text>
              )}
            </Tabs.Panel>
          </>
        )}

        <Tabs.Panel value="integrate">
          {!hasIntegrate ? (
            <Text size="sm" c="dimmed">
              {t("catalog.noIntegration")}
            </Text>
          ) : (
            <Stack gap="lg">
              {setup.length > 0 && (
                <Stack gap="xs">
                  <Text fw={600} size="sm">
                    {isImage ? t("catalog.runTitle") : t("catalog.setupTitle")}
                  </Text>
                  {a.endpoint && (
                    <Text size="xs" c="dimmed">
                      {t("catalog.registryAddress")}: <Code>{a.endpoint}</Code>
                    </Text>
                  )}
                  <SnippetTabs items={setup} />
                </Stack>
              )}
              {pkgs.slice(0, 3).map((p) => (
                <Stack key={p.name} gap="xs">
                  {((p.usage?.length ?? 0) > 0 || (p.download?.length ?? 0) > 0) && (
                    <Text fw={600} size="sm">
                      {t("catalog.usageTitle", { name: base(p.name), version: p.latest.version })}
                    </Text>
                  )}
                  {(p.usage?.length ?? 0) > 0 && <SnippetTabs items={p.usage!} />}
                  {(p.download?.length ?? 0) > 0 && (
                    <>
                      <Text size="xs" c="dimmed">
                        {t("catalog.directDownload")}
                      </Text>
                      <SnippetTabs items={p.download!} />
                    </>
                  )}
                </Stack>
              ))}
            </Stack>
          )}
        </Tabs.Panel>
      </Tabs>
    </Card>
  );
}

// The one thing most visitors came for: the newest version, one click away.
function PrimaryAction({ app, onError }: { app: AppDetail; onError: (m: string) => void }) {
  const { t } = useTranslation();
  // A real version first; failing that, the main plain file (a lone setup.exe).
  for (const wantVersioned of [true, false]) {
    for (const a of app.artifacts) {
      const p = a.packages?.find((x) => !x.unversioned === wantVersioned);
      if (a.kind === "files" && p && p.latest.primary) {
        return (
          <Button size="md" onClick={() => downloadArtifact(a.registry, p.latest.primary).catch((e) => onError(e.message))}>
            {wantVersioned ? t("catalog.downloadVersion", { version: p.latest.version }) : `${t("catalog.download")} ${base(p.latest.primary)}`}
          </Button>
        );
      }
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

          {app.description && (
            <section>
              <Title order={4} mb="sm">
                {t("catalog.about")}
              </Title>
              <div style={{ maxWidth: 820 }}>
                <Markdown source={app.description} />
              </div>
            </section>
          )}

          <section>
            <Title order={4} mb="sm">
              {t("catalog.getIt")}
            </Title>
            <Stack gap="md">
              {app.artifacts.map((a, i) => (
                <ArtifactSection key={i} a={a} />
              ))}
            </Stack>
          </section>
        </Stack>
      )}
    </div>
  );
}
