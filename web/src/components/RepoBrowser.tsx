import { useEffect, useMemo, useState } from "react";
import { useTree, type TreeNodeData } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
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

function CopyAction({ value, label }: { value: string; label?: string }) {
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={label ?? (copied ? "Copied" : "Copy")} withArrow>
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
  focusRepo,
}: {
  registry: string;
  endpoint?: string;
  focusRepo?: FocusRepo | null;
}) {
  const [repos, setRepos] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const [repo, setRepo] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const [tag, setTag] = useState("");
  const [manifest, setManifest] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [showRaw, setShowRaw] = useState(false);
  const tree = useTree();
  const narrow = useMediaQuery("(max-width: 48em)");

  const host = endpoint || registry;
  const repoRef = repo ? `${host}/${repo}` : host;

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
    setShowRaw(false);
    setErr(null);
    setLoading(true);
    api
      .tags(registry, name)
      .then((t: any) => setTags(t.tags || []))
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

  const openTag = (t: string) => {
    if (t === tag) {
      setTag("");
      setManifest(null);
      return;
    }
    setTag(t);
    setManifest(null);
    setShowRaw(false);
    setErr(null);
    setLoading(true);
    api
      .manifest(registry, repo, t)
      .then((m: any) => setManifest(m))
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };

  const layers: any[] = manifest?.manifest?.layers || [];
  const childManifests: any[] = manifest?.manifest?.manifests || [];
  const config = manifest?.manifest?.config;

  return (
    <Group gap="md" align="flex-start" wrap={narrow ? "wrap" : "nowrap"}>
      <Card withBorder padding="sm" w={narrow ? "100%" : 300} style={{ flexShrink: 0 }}>
        <TextInput
          size="xs"
          placeholder="Filter repositories…"
          leftSection={<IconSearch size={14} />}
          value={filter}
          onChange={(e) => setFilter(e.currentTarget.value)}
        />
        <Group justify="space-between" mt="xs" mb={4}>
          <Text size="xs" c="dimmed">
            {repos.length} repositor{repos.length === 1 ? "y" : "ies"}
            {filter.trim() ? ` · ${countRepos(nodes)} matching` : ""}
          </Text>
          <Button
            size="compact-xs"
            variant="subtle"
            leftSection={<IconRefresh size={12} />}
            onClick={loadRepos}
            loading={loading}
          >
            Refresh
          </Button>
        </Group>
        <ScrollArea h={narrow ? 300 : 480} type="auto" offsetScrollbars>
          {loading && repos.length === 0 ? (
            <Loader size="sm" />
          ) : data.length === 0 ? (
            <Text size="sm" c="dimmed" p="xs">
              {filter.trim() ? `No repository matches “${filter.trim()}”.` : "No repositories yet."}
            </Text>
          ) : (
            <Tree
              data={data}
              tree={tree}
              selectOnClick
              expandOnClick
              levelOffset="sm"
              aria-label="Repositories"
            />
          )}
        </ScrollArea>
      </Card>

      <Card withBorder padding="md" style={{ flex: 1, minWidth: narrow ? "100%" : 0 }}>
        {!repo ? (
          <EmptyState message="Select a repository on the left to see its tags." />
        ) : (
          <Stack gap="sm">
            <Group justify="space-between" wrap="wrap">
              <Group gap="xs" wrap="wrap" style={{ minWidth: 0 }}>
                <IconBox size={16} />
                <Text fw={600} truncate>
                  {repo}
                </Text>
                <Badge variant="light" color="gray">
                  {tags.length} tag{tags.length === 1 ? "" : "s"}
                </Badge>
              </Group>
              <Group gap={4} wrap="wrap">
                <Code>{repoRef}</Code>
                <CopyAction value={repoRef} />
              </Group>
            </Group>

            {err && <Alert color="red" title="Error">{err}</Alert>}

            {loading && tags.length === 0 ? (
              <Loader size="sm" />
            ) : tags.length === 0 ? (
              <Text size="sm" c="dimmed">
                This repository has no tags. Push an image to create one.
              </Text>
            ) : (
              <DataTable
                empty="No tags in this repository."
                rowKey={(t) => t}
                rows={tags}
                columns={[
                  {
                    header: "Tag",
                    render: (t) => (
                      <Button
                        variant={t === tag ? "light" : "subtle"}
                        size="compact-sm"
                        leftSection={<IconTag size={12} />}
                        onClick={() => openTag(t)}
                      >
                        {t}
                      </Button>
                    ),
                  },
                  {
                    header: "Pull",
                    width: "45%",
                    render: (t) => (
                      <Group gap={4} wrap="wrap">
                        <Code style={{ flex: 1 }}>{`${repoRef}:${t}`}</Code>
                        <CopyAction value={`${repoRef}:${t}`} />
                      </Group>
                    ),
                  },
                ]}
              />
            )}

            {tag && manifest && (
              <Card withBorder padding="sm">
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
                    <CopyAction value={manifest.digest} label="Copy digest" />
                  </Group>
                </Group>

                {config && (
                  <Group gap="xs" mb={4} wrap="wrap">
                    <Text size="xs" c="dimmed" w={70}>
                      config
                    </Text>
                    <Text size="xs" c="dimmed">
                      {humanSize(config.size)}
                    </Text>
                    <Code>{shortDigest(config.digest)}</Code>
                    <CopyAction value={config.digest} />
                  </Group>
                )}

                {layers.length > 0 && (
                  <Stack gap={2} mt={4}>
                    {layers.map((l, i) => (
                      <Group key={l.digest || i} gap="xs" wrap="wrap">
                        <Text size="xs" c="dimmed" w={70}>
                          layer {i}
                        </Text>
                        <Badge size="xs" variant="light" color="gray" w={110} style={{ flexShrink: 0 }}>
                          {l.mediaType?.split(".").pop()}
                        </Badge>
                        <Text size="xs" w={70} style={{ flexShrink: 0 }}>
                          {humanSize(l.size)}
                        </Text>
                        <Code style={{ flex: 1 }}>{shortDigest(l.digest)}</Code>
                        <CopyAction value={l.digest} />
                      </Group>
                    ))}
                    <Group gap="xs" justify="flex-end">
                      <Text size="xs" c="dimmed">
                        total {humanSize(layers.reduce((a: number, l: any) => a + (l.size || 0), 0))}
                      </Text>
                    </Group>
                  </Stack>
                )}

                {childManifests.length > 0 && (
                  <Stack gap={2} mt={4}>
                    {childManifests.map((m: any) => (
                      <Group key={m.digest} gap="xs" wrap="wrap">
                        <Text size="xs" c="dimmed" w={70}>
                          manifest
                        </Text>
                        <Badge size="xs" variant="light" color="blue">
                          {m.platform ? `${m.platform.os}/${m.platform.architecture}` : "—"}
                        </Badge>
                        <Text size="xs" w={70} style={{ flexShrink: 0 }}>
                          {humanSize(m.size)}
                        </Text>
                        <Code style={{ flex: 1 }}>{shortDigest(m.digest)}</Code>
                        <CopyAction value={m.digest} />
                      </Group>
                    ))}
                  </Stack>
                )}

                <Group gap="xs" mt="xs">
                  <Button size="compact-xs" variant="subtle" onClick={() => setShowRaw(!showRaw)}>
                    {showRaw ? "Hide raw manifest" : "Show raw manifest"}
                  </Button>
                </Group>
                {showRaw && (
                  <ScrollArea.Autosize mah={320} mt={4}>
                    <Code block>{JSON.stringify(manifest.manifest, null, 2)}</Code>
                  </ScrollArea.Autosize>
                )}
              </Card>
            )}
          </Stack>
        )}
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