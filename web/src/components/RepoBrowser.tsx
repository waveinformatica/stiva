import { useEffect, useMemo, useState } from "react";
import { useTree, type TreeNodeData } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { useTranslation } from "react-i18next";
import {
  Alert,
  Badge,
  Button,
  Card,
  Code,
  CopyButton,
  DataTable,
  EmptyState,
  Group,
  Loader,
  ScrollArea,
  Stack,
  Text,
  TextInput,
  Tooltip,
  Tree,
  ActionIcon,
} from "./ui";
import { api } from "../lib/api";
import { IconBox, IconCopy, IconFolder, IconRefresh, IconSearch, IconTag } from "./icons";

export type Reg = {
  name: string;
  type: string;
  format: string;
  online: boolean;
  hosts?: string[];
  port?: number;
  base_path?: string;
};

// The address a client uses to pull from a registry: a virtual host when one is
// configured, otherwise the registry name, plus the dedicated port when it has
// one and the host does not carry a port already. Docker needs exactly this to
// build the pull reference.
export function registryEndpoint(r?: Reg): string {
  if (!r) return "";
  const host = r.hosts?.[0] || r.name;
  if (r.port && r.port > 0 && !host.includes(":")) return `${host}:${r.port}`;
  return host;
}

type Node = {
  value: string;
  label: string;
  repo?: string;
  children: Node[];
};

// Repository names are flat strings, so "team/api" and "team/api/v2" can both
// exist: a node is therefore both a repository and a folder at once. Nodes
// without a repository are pure structure and never selectable.
function buildTree(repos: string[], filter: string): Node[] {
  const q = filter.trim().toLowerCase();
  const root: Node = { value: "", label: "", children: [] };

  for (const r of repos) {
    if (q && !r.toLowerCase().includes(q)) continue;
    let node = root;
    let path = "";
    for (const seg of r.split("/")) {
      if (!seg) continue;
      path = path ? `${path}/${seg}` : seg;
      let child = node.children.find((c) => c.label === seg);
      if (!child) {
        child = { value: path, label: seg, children: [] };
        node.children.push(child);
      }
      node = child;
    }
    node.repo = r;
  }

  const prune = (n: Node): boolean => {
    n.children = n.children.filter(prune);
    n.children.sort((a, b) => {
      const af = a.repo === undefined ? 0 : 1;
      const bf = b.repo === undefined ? 0 : 1;
      if (af !== bf) return af - bf;
      return a.label.localeCompare(b.label);
    });
    return n.value === "" || n.repo !== undefined || n.children.length > 0;
  };
  prune(root);
  return root.children;
}

function toTreeData(nodes: Node[]): TreeNodeData[] {
  return nodes.map((n) => ({
    value: n.value,
    label: (
      <Group gap={6} wrap="nowrap" component="span" style={{ minWidth: 0 }}>
        {n.repo !== undefined ? <IconBox size={13} /> : <IconFolder size={13} />}
        <Text size="sm" truncate>
          {n.label}
        </Text>
        {n.repo !== undefined && n.children.length > 0 ? (
          <Badge size="xs" variant="light" color="gray">
            +{n.children.length}
          </Badge>
        ) : null}
      </Group>
    ),
    children: n.children.length ? toTreeData(n.children) : undefined,
  }));
}

function humanSize(n?: number) {
  if (n === undefined || n === null) return "—";
  if (n < 1024) return `${n} B`;
  const units = ["kB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

const shortDigest = (d?: string) => (d ? (d.startsWith("sha256:") ? d.slice(7, 19) : d.slice(0, 12)) : "—");

// Tags read as-is; digests shorten to the familiar 12 hex chars.
const shortRef = (r: string) => (r.includes(":") ? shortDigest(r) : r);

// Scroll pane that fills its flex-column parent on desktop and stays a
// fixed 300px box on narrow screens, where the columns stack. Both panes
// use it, so they share the row height and scroll independently — the page
// itself never scrolls.
function PaneScroll({ narrow, children }: { narrow: boolean; children: React.ReactNode }) {
  if (narrow) {
    return (
      <ScrollArea h={300} type="auto" offsetScrollbars>
        {children}
      </ScrollArea>
    );
  }
  return (
    <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
      <ScrollArea h="100%" type="auto" offsetScrollbars>
        {children}
      </ScrollArea>
    </div>
  );
}

function CopyAction({ value, label }: { value: string; label?: string }) {
  const { t } = useTranslation();
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={label ?? (copied ? t("browser.copied") : t("browser.copy"))} withArrow>
          <ActionIcon size="sm" variant="subtle" color={copied ? "teal" : "gray"} onClick={copy}>
            <IconCopy size={13} />
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  );
}

export type FocusRepo = { repo: string; n: number };

export default function RepoBrowser({
  registry,
  endpoint,
  pullHost,
  focusRepo,
}: {
  registry: string;
  endpoint?: string;
  // Reachable pull host resolved server-side (own host, containing group or
  // browsed host). Null while loading, "" when no route reaches it.
  pullHost?: string | null;
  focusRepo?: FocusRepo | null;
}) {
  const { t } = useTranslation();
  const [repos, setRepos] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const [repo, setRepo] = useState("");
  const [tags, setTags] = useState<{ tag: string; pushed_at: number }[]>([]);
  const [tag, setTag] = useState("");
  const [manifest, setManifest] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [showRaw, setShowRaw] = useState(false);
  const [showDf, setShowDf] = useState(false);
  const [dockerfile, setDockerfile] = useState<{ content: string; source: string } | null>(null);
  const [dfLoading, setDfLoading] = useState(false);
  // Breadcrumb trail from the tag down to the manifest on screen. Empty
  // means the tag itself; otherwise the last entry is the current ref and
  // the earlier ones are its ancestors (tag first).
  const [path, setPath] = useState<string[]>([]);
  // Total image size per child manifest digest: null while loading,
  // -1 when the child could not be read. Keyed by digest, which identifies
  // a manifest immutably, so late responses stay valid after navigating.
  const [childSizes, setChildSizes] = useState<Record<string, number | null>>({});
  // Tags whose image contains a layer digest: null while loading. Same
  // digest-keyed cache as child sizes.
  const [layerTags, setLayerTags] = useState<Record<string, { tag: string; pushed_at: number }[] | null>>({});
  const tree = useTree();
  const narrow = useMediaQuery("(max-width: 48em)");

  const host = pullHost ?? endpoint ?? registry;
  const repoRef = repo && host ? `${host}/${repo}` : host;

  const expandTo = (value: string) => {
    const segs = value.split("/");
    for (let i = 1; i < segs.length; i++) tree.expand(segs.slice(0, i).join("/"));
  };

  const loadRepos = () => {
    if (!registry) return;
    setLoading(true);
    setErr(null);
    api
      .repos(registry)
      .then((r: any) => {
        const list: string[] = r.repositories || [];
        setRepos(list);
        // Landing on an empty detail pane helps nobody: with nothing selected
        // the first repository opens, and the filter drops out so the
        // selection is actually visible in the tree. Skipped when an external
        // focus is pending: it selects its own repository below.
        if (list.length && !tree.selectedState.length && !focusRepo) {
          setFilter("");
          expandTo(list[0]);
          tree.select(list[0]);
        }
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    setFilter("");
    setRepo("");
    setTags([]);
    setTag("");
    setManifest(null);
    setShowDf(false);
    setDockerfile(null);
    setPath([]);
    setRepos([]);
    tree.clearSelected();
    loadRepos();
  }, [registry]);

  const nodes = useMemo(() => buildTree(repos, filter), [repos, filter]);
  const data = useMemo(() => toTreeData(nodes), [nodes]);

  useEffect(() => {
    if (filter.trim()) tree.expandAllNodes();
  }, [filter, data]);

  const selectRepo = (name: string) => {
    setRepo(name);
    setTag("");
    setManifest(null);
    setPath([]);
    setShowRaw(false);
    setShowDf(false);
    setDockerfile(null);
    setErr(null);
    setLoading(true);
    api
      .tags(registry, name)
      .then((t: any) =>
        setTags(
          (t.tags || []).map((x: any) =>
            typeof x === "string" ? { tag: x, pushed_at: 0 } : x,
          ),
        ),
      )
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };

  const selectedValue = tree.selectedState[0] || "";
  useEffect(() => {
    if (!selectedValue) return;
    const leaf = findRepo(nodes, selectedValue);
    if (leaf && leaf !== repo) selectRepo(leaf);
  }, [selectedValue, nodes]);

  // External selection, e.g. jumping from a search hit: waits for the repo
  // list when it has not loaded yet, and drops the filter so the target is
  // reachable in the tree.
  useEffect(() => {
    if (!focusRepo) return;
    if (!repos.includes(focusRepo.repo)) return;
    setFilter("");
    expandTo(focusRepo.repo);
    if (tree.selectedState[0] !== focusRepo.repo) tree.select(focusRepo.repo);
  }, [focusRepo, repos]);

  // Loads any manifest ref (tag or digest) and records how we got there.
  // A single-platform index opens straight on its platform manifest, so a
  // plain buildx push lands on the layers with one click less; the index
  // stays one "back" away.
  const loadRef = (ref: string, trail: string[], autoDrill: boolean) => {
    setManifest(null);
    setShowRaw(false);
    setShowDf(false);
    setDockerfile(null);
    setErr(null);
    setLoading(true);
    api
      .manifest(registry, repo, ref)
      .then((m: any) => {
        const kids: any[] = m?.manifest?.manifests || [];
        const ls: any[] = m?.manifest?.layers || [];
        const plats = kids.filter((k: any) => k?.platform?.os && k?.platform?.architecture);
        if (autoDrill && ls.length === 0 && plats.length === 1 && plats[0].digest) {
          const nt = [...trail, ref];
          setPath(nt);
          loadRef(plats[0].digest, nt, false);
          return;
        }
        setPath(trail);
        setManifest(m);
        // Which tags share each layer: one lookup per layer digest, cached
        // by digest like everything else content-addressed here.
        const layerDigests = Array.from(
          new Set(ls.map((l: any) => l?.digest).filter((d: string) => d)),
        );
        if (layerDigests.length) {
          setLayerTags((prev) => {
            const next = { ...prev };
            for (const d of layerDigests) if (!(d in next)) next[d] = null;
            return next;
          });
          for (const d of layerDigests) {
            api
              .blobTags(registry, repo, d)
              .then((r: any) =>
                setLayerTags((prev) => (prev[d] === null ? { ...prev, [d]: r.tags || [] } : prev)),
              )
              .catch(() =>
                setLayerTags((prev) => (prev[d] === null ? { ...prev, [d]: [] } : prev)),
              );
          }
        }
        // Resolve each child manifest to its total image size (layers +
        // config). The index itself only carries the manifest JSON size,
        // which says nothing about the image.
        const fresh = kids.map((k: any) => k?.digest).filter((d: string) => d);
        if (fresh.length) {
          setChildSizes((prev) => {
            const next = { ...prev };
            for (const d of fresh) if (!(d in next)) next[d] = null;
            return next;
          });
          for (const d of fresh) {
            api
              .manifest(registry, repo, d)
              .then((cm: any) => {
                const cls: any[] = cm?.manifest?.layers || [];
                const ccfg = cm?.manifest?.config;
                const total =
                  cls.reduce((a: number, l: any) => a + (l?.size || 0), 0) + (ccfg?.size || 0);
                setChildSizes((prev) => (prev[d] === null ? { ...prev, [d]: total } : prev));
              })
              .catch(() =>
                setChildSizes((prev) => (prev[d] === null ? { ...prev, [d]: -1 } : prev)),
              );
          }
        }
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };

  const openTag = (t: string) => {
    if (t === tag) {
      setTag("");
      setManifest(null);
      setPath([]);
      return;
    }
    setTag(t);
    loadRef(t, [], true);
  };

  const currentRef = path.length ? path[path.length - 1] : tag;

  // pushed_at is UnixNano like every timestamp in the store; 0 means the tag
  // predates tracking (or the backfill found no manifest for it).
  const formatPushTime = (ns: number) => {
    if (!ns) return "—";
    const d = new Date(ns / 1e6);
    return isNaN(d.getTime()) ? "—" : d.toLocaleString();
  };

  // image_created is the RFC3339 instant baked into the image config.
  const formatImageTime = (s?: string) => {
    if (!s) return "";
    const d = new Date(s);
    return isNaN(d.getTime()) ? s : d.toLocaleString();
  };

  const childSizeText = (d: string) => {
    const v = childSizes[d];
    if (v === undefined || v === null) return "…";
    if (v < 0) return "—";
    return humanSize(v);
  };

  const drillInto = (d: string) => {
    if (!d) return;
    loadRef(d, [...path, currentRef], false);
  };

  const goBack = () => {
    if (!path.length) return;
    const nt = path.slice(0, -1);
    loadRef(nt.length ? nt[nt.length - 1] : tag, nt, false);
  };

  const toggleDockerfile = () => {
    if (showDf) {
      setShowDf(false);
      return;
    }
    setShowDf(true);
    if (dockerfile || dfLoading) return;
    setDfLoading(true);
    api
      .dockerfile(registry, repo, currentRef)
      .then((d: any) => setDockerfile({ content: d.dockerfile || "", source: d.source || "" }))
      .catch(() => setDockerfile({ content: "", source: "" }))
      .finally(() => setDfLoading(false));
  };

  const layers: any[] = manifest?.manifest?.layers || [];
  const childManifests: any[] = manifest?.manifest?.manifests || [];
  const config = manifest?.manifest?.config;

  const treeBody =
    loading && repos.length === 0 ? (
      <Loader size="sm" />
    ) : data.length === 0 ? (
      <Text size="sm" c="dimmed" p="xs">
        {filter.trim() ? t("browser.noMatch", { filter: filter.trim() }) : t("browser.noRepos")}
      </Text>
    ) : (
      <Tree
        data={data}
        tree={tree}
        selectOnClick
        expandOnClick
        levelOffset="sm"
        aria-label={t("browser.repositories")}
      />
    );

  // Row height = viewport minus app header, page header, tabs and paddings.
  const rowH = narrow ? undefined : "calc(100dvh - 200px)";

  return (
    <Group
      gap="md"
      align={narrow ? "flex-start" : "stretch"}
      wrap={narrow ? "wrap" : "nowrap"}
      h={rowH}
      mih={narrow ? undefined : 320}
    >
      <Card
        withBorder
        padding="sm"
        w={narrow ? "100%" : 300}
        style={{ flexShrink: 0, display: "flex", flexDirection: "column", minHeight: 0 }}
      >
        <TextInput
          size="xs"
          placeholder={t("browser.filterPlaceholder")}
          leftSection={<IconSearch size={14} />}
          value={filter}
          onChange={(e) => setFilter(e.currentTarget.value)}
        />
        <Group justify="space-between" mt="xs" mb={4}>
          <Text size="xs" c="dimmed">
            {t("browser.repoCount", { count: repos.length })}
            {filter.trim() ? ` · ${t("browser.matchingCount", { count: countRepos(nodes) })}` : ""}
          </Text>
          <Button
            size="compact-xs"
            variant="subtle"
            leftSection={<IconRefresh size={12} />}
            onClick={loadRepos}
            loading={loading}
          >
            {t("common.refresh")}
          </Button>
        </Group>
        <PaneScroll narrow={narrow}>{treeBody}</PaneScroll>
      </Card>

      <Card
        withBorder
        padding="md"
        style={{ flex: 1, minWidth: narrow ? "100%" : 0, display: "flex", flexDirection: "column", minHeight: 0 }}
      >
        <PaneScroll narrow={narrow}>
          {!repo ? (
            <EmptyState message={t("browser.selectRepo")} />
          ) : (
            <Stack gap="sm">
            <Group justify="space-between" wrap="wrap">
              <Group gap="xs" wrap="wrap" style={{ minWidth: 0 }}>
                <IconBox size={16} />
                <Text fw={600} truncate>
                  {repo}
                </Text>
                <Badge variant="light" color="gray">
                  {t("browser.tagCount", { count: tags.length })}
                </Badge>
              </Group>
              <Group gap={4} wrap="wrap">
                {host ? (
                  <>
                    <Code>{repoRef}</Code>
                    <CopyAction value={repoRef} />
                  </>
                ) : (
                  <Text size="xs" c="dimmed">
                    {t("browser.noRoute")}
                  </Text>
                )}
              </Group>
            </Group>

            {err && <Alert color="red" title={t("common.error")}>{err}</Alert>}

            {loading && tags.length === 0 ? (
              <Loader size="sm" />
            ) : tags.length === 0 ? (
              <Text size="sm" c="dimmed">
                {t("browser.noTags")}
              </Text>
            ) : (
              <DataTable
                empty={t("browser.noTagsInRepo")}
                rowKey={(o) => o.tag}
                rows={tags}
                columns={[
                  {
                    header: t("browser.tagHeader"),
                    render: (o) => (
                      <Button
                        variant={o.tag === tag ? "light" : "subtle"}
                        size="compact-sm"
                        leftSection={<IconTag size={12} />}
                        onClick={() => openTag(o.tag)}
                      >
                        {o.tag}
                      </Button>
                    ),
                  },
                  {
                    header: t("browser.pushedHeader"),
                    width: 170,
                    render: (o) => <Text size="xs">{formatPushTime(o.pushed_at)}</Text>,
                  },
                  {
                    header: t("browser.pullHeader"),
                    width: "45%",
                    render: (o) =>
                      host ? (
                        <Group gap={4} wrap="wrap">
                          <Code style={{ flex: 1 }}>{`${repoRef}:${o.tag}`}</Code>
                          <CopyAction value={`${repoRef}:${o.tag}`} />
                        </Group>
                      ) : (
                        <Text size="xs" c="dimmed">
                          {t("browser.noRoute")}
                        </Text>
                      ),
                  },
                ]}
              />
            )}

            {tag && manifest && (
              <Card withBorder padding="sm">
                {path.length > 0 && (
                  <Group gap="xs" mb="xs">
                    <Button size="compact-xs" variant="subtle" onClick={goBack}>
                      {t("browser.backTo", { ref: shortRef(path[path.length - 1]) })}
                    </Button>
                  </Group>
                )}
                <Group justify="space-between" mb="xs" wrap="wrap">
                  <Group gap="xs" wrap="wrap">
                    <Badge color="teal">{tag}</Badge>
                    <Text size="xs" c="dimmed">
                      {manifest.media_type}
                    </Text>
                  </Group>
                  <Group gap={4} wrap="wrap">
                    <Text size="xs" c="dimmed">
                      {manifest.digest}
                    </Text>
                    <CopyAction value={manifest.digest} label={t("browser.copyDigest")} />
                  </Group>
                </Group>

                {manifest.author && (
                  <Group gap="xs" mb={4} wrap="wrap">
                    <Text size="xs" c="dimmed" w={70}>
                      {t("browser.authorLabel")}
                    </Text>
                    <Text size="xs">{manifest.author}</Text>
                  </Group>
                )}
                {manifest.image_created && (
                  <Group gap="xs" mb={4} wrap="wrap">
                    <Text size="xs" c="dimmed" w={70}>
                      {t("browser.createdLabel")}
                    </Text>
                    <Text size="xs">{formatImageTime(manifest.image_created)}</Text>
                  </Group>
                )}

                {config && (
                  <Group gap="xs" mb={4} wrap="wrap">
                    <Text size="xs" c="dimmed" w={70}>
                      {t("browser.config")}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {humanSize(config.size)}
                    </Text>
                    <Code>{shortDigest(config.digest)}</Code>
                    <CopyAction value={config.digest} />
                  </Group>
                )}

                {layers.length > 0 && (
                  <>
                    <DataTable
                      rows={layers.map((l: any, i: number) => ({ ...l, n: i }))}
                      rowKey={(l: any) => l.digest || String(l.n)}
                      columns={[
                        {
                          header: t("browser.layerHeader"),
                          width: 80,
                          render: (l: any) => (
                            <Text size="xs" c="dimmed">
                              {l.n}
                            </Text>
                          ),
                        },
                        {
                          header: t("browser.typeHeader"),
                          width: 150,
                          render: (l: any) => (
                            <Badge size="xs" variant="light" color="gray">
                              {l.mediaType?.split(".").pop() || "—"}
                            </Badge>
                          ),
                        },
                        {
                          header: t("registries.sizeHeader"),
                          width: 90,
                          render: (l: any) => <Text size="xs">{humanSize(l.size)}</Text>,
                        },
                        {
                          header: t("registries.digestHeader"),
                          render: (l: any) => (
                            <Group gap="xs" wrap="nowrap">
                              <Code style={{ flex: 1 }}>{shortDigest(l.digest)}</Code>
                              <CopyAction value={l.digest} />
                            </Group>
                          ),
                        },
                        {
                          header: t("browser.tagsLabel"),
                          width: 220,
                          render: (l: any) => {
                            const ts = layerTags[l.digest];
                            if (ts === undefined || ts === null)
                              return (
                                <Text size="xs" c="dimmed">
                                  …
                                </Text>
                              );
                            if (!ts.length)
                              return (
                                <Text size="xs" c="dimmed">
                                  —
                                </Text>
                              );
                            return (
                              <Group gap={4}>
                                {ts.slice(0, 3).map((x) => (
                                  <Button
                                    key={x.tag}
                                    size="compact-xs"
                                    variant={x.tag === tag ? "light" : "subtle"}
                                    onClick={() => openTag(x.tag)}
                                  >
                                    {x.tag}
                                  </Button>
                                ))}
                                {ts.length > 3 && (
                                  <Text size="xs" c="dimmed">
                                    +{ts.length - 3}
                                  </Text>
                                )}
                              </Group>
                            );
                          },
                        },
                      ]}
                    />
                    <Group gap="xs" justify="flex-end" mt={4}>
                      <Text size="xs" c="dimmed">
                        {t("browser.totalSize", { size: humanSize(layers.reduce((a: number, l: any) => a + (l.size || 0), 0)) })}
                      </Text>
                    </Group>
                  </>
                )}

                {childManifests.length > 0 && (
                  <DataTable
                    rows={childManifests}
                    rowKey={(m: any) => m.digest}
                    columns={[
                      {
                        header: t("browser.platform"),
                        width: 150,
                        render: (m: any) => (
                          <Badge size="xs" variant="light" color="blue">
                            {m.platform ? `${m.platform.os}/${m.platform.architecture}` : "—"}
                          </Badge>
                        ),
                      },
                      {
                        header: t("browser.imageSize"),
                        width: 110,
                        render: (m: any) => <Text size="xs">{childSizeText(m.digest)}</Text>,
                      },
                      {
                        header: t("registries.digestHeader"),
                        render: (m: any) => (
                          <Group gap="xs" wrap="nowrap">
                            <Code style={{ flex: 1 }}>{shortDigest(m.digest)}</Code>
                            <CopyAction value={m.digest} />
                          </Group>
                        ),
                      },
                      {
                        header: "",
                        width: 80,
                        render: (m: any) => (
                          <Button size="compact-xs" variant="subtle" onClick={() => drillInto(m.digest)}>
                            {t("browser.open")}
                          </Button>
                        ),
                      },
                    ]}
                  />
                )}

                <Group gap="xs" mt="xs">
                  <Button size="compact-xs" variant="subtle" onClick={() => setShowRaw(!showRaw)}>
                    {showRaw ? t("browser.hideRaw") : t("browser.showRaw")}
                  </Button>
                  <Button size="compact-xs" variant="subtle" onClick={toggleDockerfile} loading={dfLoading}>
                    {showDf ? t("browser.hideDockerfile") : t("browser.showDockerfile")}
                  </Button>
                </Group>
                {showRaw && (
                  <ScrollArea.Autosize mah={320} mt={4}>
                    <Code block>{JSON.stringify(manifest.manifest, null, 2)}</Code>
                  </ScrollArea.Autosize>
                )}
                {showDf && dockerfile && (
                  <ScrollArea.Autosize mah={320} mt={4}>
                    {!dockerfile.content ? (
                      <Text size="sm" c="dimmed">
                        {t("browser.noDockerfile")}
                      </Text>
                    ) : (
                      <>
                        {dockerfile.source === "history" && (
                          <Text size="xs" c="dimmed" mb={4}>
                            {t("browser.dockerfileReconstructed")}
                          </Text>
                        )}
                        <Code block>{dockerfile.content}</Code>
                      </>
                    )}
                  </ScrollArea.Autosize>
                )}
              </Card>
            )}
            </Stack>
          )}
        </PaneScroll>
      </Card>
    </Group>
  );
}

function findRepo(nodes: Node[], value: string): string | null {
  for (const n of nodes) {
    if (n.value === value) return n.repo ?? null;
    const hit = findRepo(n.children, value);
    if (hit) return hit;
  }
  return null;
}

function countRepos(nodes: Node[]): number {
  return nodes.reduce((acc, n) => acc + (n.repo ? 1 : 0) + countRepos(n.children), 0);
}