import { useEffect, useState, type ReactElement } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Button, Checkbox, Group, Loader, Modal, ScrollArea, Stack, Text, TextInput } from "@mantine/core";
import { api } from "../lib/api";

export type Reg = { name: string; format: string; type: string };
export type Pick = { registry: string; ref: string };

type Node = { name: string; path: string; selectable: boolean; isDir: boolean; children: Node[] };
type Loaded = { loading: boolean; error?: string; root?: Node[]; truncated?: boolean };

const SEP = "\u0000";
const key = (registry: string, ref: string) => registry + SEP + ref;

// An image repository is addressed as written; a folder of a path-addressed
// registry ends with "/" so it matches only what is inside it.
const refOf = (n: Node, oci: boolean) => (!oci && n.isDir ? n.path + "/" : n.path);

/** Fold stored paths into a tree. OCI repositories are the leaves; for other
 *  formats every folder and file can be picked. */
function buildTree(paths: string[], oci: boolean): Node[] {
  const root: Node = { name: "", path: "", selectable: false, isDir: true, children: [] };
  for (const p of paths) {
    const segs = p.split("/").filter(Boolean);
    let cur = root;
    segs.forEach((seg, i) => {
      const path = segs.slice(0, i + 1).join("/");
      let next = cur.children.find((c) => c.name === seg);
      if (!next) {
        next = { name: seg, path, selectable: !oci, isDir: true, children: [] };
        cur.children.push(next);
      }
      if (i === segs.length - 1) {
        // The last segment is a repository (OCI) or a file.
        next.selectable = true;
        if (!oci) next.isDir = next.children.length > 0;
      }
      cur = next;
    });
  }
  const sort = (n: Node) => {
    n.children.sort((a, b) => Number(b.children.length > 0) - Number(a.children.length > 0) || a.name.localeCompare(b.name));
    n.children.forEach(sort);
  };
  sort(root);
  return root.children;
}

function matches(n: Node, needle: string): boolean {
  return n.path.toLowerCase().includes(needle) || n.children.some((c) => matches(c, needle));
}

/**
 * A tree of the registries and what they hold, with a checkbox on each thing a
 * catalog entry can point at. Registries load when opened. Checking a folder
 * covers everything under it, so its contents are shown checked and locked.
 */
export default function ArtifactPicker({
  opened,
  registries,
  selected,
  onApply,
  onClose,
}: {
  opened: boolean;
  registries: Reg[];
  selected: Pick[];
  onApply: (checked: Pick[], browsed: string[]) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [data, setData] = useState<Record<string, Loaded>>({});
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [q, setQ] = useState("");

  const isOci = (name: string) => registries.find((r) => r.name === name)?.format === "oci";

  const load = (reg: string, reveal: string[] = []) => {
    if (data[reg]) return;
    setData((d) => ({ ...d, [reg]: { loading: true } }));
    api
      .browseAll(reg)
      .then((r: any) => {
        const entries: { path: string }[] = r.entries || [];
        setData((d) => ({
          ...d,
          [reg]: { loading: false, root: buildTree(entries.map((e) => e.path), isOci(reg)), truncated: entries.length >= 2000 },
        }));
        // Unfold the folders above each already chosen item.
        setOpen((cur) => {
          const next = new Set(cur);
          for (const ref of reveal) {
            const segs = ref.replace(/\/+$/, "").split("/");
            for (let i = 1; i < segs.length; i++) next.add(reg + "::" + segs.slice(0, i).join("/"));
          }
          return next;
        });
      })
      .catch((e) => setData((d) => ({ ...d, [reg]: { loading: false, error: e.message } })));
  };

  // Each time the dialog opens, start from what the entry already points at and
  // open the registries holding a choice so it is in view.
  useEffect(() => {
    if (!opened) return;
    setChecked(new Set(selected.map((s) => key(s.registry, s.ref))));
    const regs = [...new Set(selected.map((s) => s.registry))];
    setOpen(new Set(regs));
    regs.forEach((r) => load(r, selected.filter((s) => s.registry === r).map((s) => s.ref)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opened]);

  const toggleOpen = (k: string, reg?: string) => {
    const next = new Set(open);
    if (next.has(k)) next.delete(k);
    else {
      next.add(k);
      if (reg) load(reg);
    }
    setOpen(next);
  };

  // A node is covered when an ancestor folder is checked.
  const covered = (reg: string, path: string, oci: boolean) => {
    if (oci) return false;
    const segs = path.split("/");
    for (let i = 1; i < segs.length; i++) {
      if (checked.has(key(reg, segs.slice(0, i).join("/") + "/"))) return true;
    }
    return false;
  };

  const toggleCheck = (reg: string, n: Node, oci: boolean) => {
    const ref = refOf(n, oci);
    const k = key(reg, ref);
    const next = new Set(checked);
    if (next.has(k)) next.delete(k);
    else {
      next.add(k);
      if (!oci && ref.endsWith("/")) {
        // The folder now includes whatever was checked inside it.
        for (const other of [...next]) {
          const [r, p] = other.split(SEP);
          if (r === reg && p !== ref && p.startsWith(ref)) next.delete(other);
        }
      }
    }
    setChecked(next);
  };

  const needle = q.trim().toLowerCase();

  const renderNode = (reg: string, n: Node, oci: boolean, depth: number): ReactElement | null => {
    if (needle && !matches(n, needle)) return null;
    const k = reg + "::" + n.path;
    const hasKids = n.children.length > 0;
    const isOpen = open.has(k) || (!!needle && hasKids);
    const ref = refOf(n, oci);
    const implied = covered(reg, n.path, oci);
    return (
      <div key={k}>
        <Group gap={6} wrap="nowrap" pl={depth * 18} py={2}>
          <span
            role={hasKids ? "button" : undefined}
            onClick={() => hasKids && toggleOpen(k)}
            style={{ width: 14, cursor: hasKids ? "pointer" : "default", userSelect: "none", opacity: 0.7 }}
          >
            {hasKids ? (isOpen ? "▾" : "▸") : ""}
          </span>
          {n.selectable ? (
            <Checkbox
              size="xs"
              label={n.name + (!oci && n.isDir ? "/" : "")}
              checked={implied || checked.has(key(reg, ref))}
              disabled={implied}
              onChange={() => toggleCheck(reg, n, oci)}
            />
          ) : (
            <Text size="sm" c="dimmed" onClick={() => hasKids && toggleOpen(k)} style={{ cursor: "pointer" }}>
              {n.name}/
            </Text>
          )}
        </Group>
        {isOpen && n.children.map((c) => renderNode(reg, c, oci, depth + 1))}
      </div>
    );
  };

  const browsed = Object.entries(data)
    .filter(([, v]) => v.root)
    .map(([k]) => k);

  return (
    <Modal opened={opened} onClose={onClose} title={t("catalogAdmin.pickTitle")} size="lg">
      <Stack gap="sm">
        <Text size="xs" c="dimmed">
          {t("catalogAdmin.pickHint")}
        </Text>
        <TextInput size="xs" placeholder={t("catalogAdmin.pickFilter")} value={q} onChange={(e) => setQ(e.currentTarget.value)} />
        <ScrollArea.Autosize mah="52vh" offsetScrollbars>
          {registries.map((r) => {
            const k = r.name;
            const isOpen = open.has(k) || (!!needle && !!data[r.name]?.root);
            const d = data[r.name];
            return (
              <div key={r.name}>
                <Group gap={6} wrap="nowrap" py={3} style={{ cursor: "pointer" }} onClick={() => toggleOpen(k, r.name)}>
                  <span style={{ width: 14, opacity: 0.7 }}>{isOpen ? "▾" : "▸"}</span>
                  <Text size="sm" fw={600}>
                    {r.name}
                  </Text>
                  <Badge size="xs" variant="outline" color="gray">
                    {r.format}
                  </Badge>
                  {d?.loading && <Loader size={12} />}
                </Group>
                {isOpen && (
                  <div>
                    {d?.error && (
                      <Text size="xs" c="red" pl={32}>
                        {t("catalogAdmin.pickError", { error: d.error })}
                      </Text>
                    )}
                    {d?.root && d.root.length === 0 && (
                      <Text size="xs" c="dimmed" pl={32}>
                        {t("catalogAdmin.pickEmpty")}
                      </Text>
                    )}
                    {d?.root?.map((n) => renderNode(r.name, n, r.format === "oci", 1))}
                    {d?.truncated && (
                      <Text size="xs" c="dimmed" pl={32}>
                        {t("catalogAdmin.pickTruncated", { count: 2000 })}
                      </Text>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </ScrollArea.Autosize>
        <Group justify="space-between">
          <Text size="xs" c="dimmed">
            {t("catalogAdmin.pickSelected", { count: checked.size })}
          </Text>
          <Group>
            <Button variant="default" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button
              onClick={() => {
                onApply(
                  [...checked].map((k) => {
                    const [registry, ref] = k.split(SEP);
                    return { registry, ref };
                  }),
                  browsed,
                );
                onClose();
              }}
            >
              {t("catalogAdmin.pickApply")}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  );
}
