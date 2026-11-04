import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ActionIcon, Anchor, Badge, Button, Card, Code, CopyButton, Group, ScrollArea, Stack, Table, Tabs, Text, TextInput,
  Tooltip,
} from "@mantine/core";
import { downloadArtifact } from "../lib/api";
import { IconCopy } from "./icons";

export type Snippet = { title: string; lang: string; code: string; note?: string };
export type FileV = { path: string; size: number; created_at?: number };
export type VersionV = { version: string; created_at: number; primary: string; files: FileV[]; usage?: Snippet[] };

export function fmtSize(n: number) {
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

export const baseName = (p: string) => p.split("/").pop() || p;
export const isPre = (v: string) => /-[A-Za-z]/.test(v.replace(/^v/i, ""));

export function CopyIcon({ value }: { value: string }) {
  const { t } = useTranslation();
  return (
    <CopyButton value={value} timeout={1500}>
      {({ copied, copy }) => (
        <Tooltip label={copied ? t("catalog.copied") : t("catalog.copy")} withArrow>
          <ActionIcon variant="subtle" color={copied ? "teal" : "gray"} onClick={copy} aria-label={t("catalog.copy")}>
            <IconCopy size={15} />
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  );
}

/** One code block with a copy button. */
export function CopyBlock({ code }: { code: string }) {
  return (
    <Group gap={6} wrap="nowrap" align="flex-start">
      <Code block style={{ flex: 1, overflowX: "auto", whiteSpace: "pre", fontSize: 12 }}>
        {code}
      </Code>
      <CopyIcon value={code} />
    </Group>
  );
}

/** Ready-to-paste snippets: one tab per variant (Maven / Gradle, npm / yarn…). */
export function SnippetTabs({ items }: { items: Snippet[] }) {
  if (!items || items.length === 0) return null;
  if (items.length === 1) {
    return (
      <Stack gap={4}>
        <Text size="xs" c="dimmed">
          {items[0].title}
        </Text>
        <CopyBlock code={items[0].code} />
        {items[0].note && (
          <Text size="xs" c="dimmed">
            {items[0].note}
          </Text>
        )}
      </Stack>
    );
  }
  return (
    <Tabs defaultValue="0" variant="outline" keepMounted={false}>
      <Tabs.List>
        {items.map((s, i) => (
          <Tabs.Tab key={i} value={String(i)}>
            {s.title}
          </Tabs.Tab>
        ))}
      </Tabs.List>
      {items.map((s, i) => (
        <Tabs.Panel key={i} value={String(i)} pt="xs">
          <CopyBlock code={s.code} />
          {s.note && (
            <Text size="xs" c="dimmed" mt={4}>
              {s.note}
            </Text>
          )}
        </Tabs.Panel>
      ))}
    </Tabs>
  );
}

/** A table of files with a download button each, filterable, paged by 50. */
export function FilesTable({
  registry,
  files,
  onError,
  filterable = true,
}: {
  registry: string;
  files: FileV[];
  onError: (m: string) => void;
  filterable?: boolean;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [limit, setLimit] = useState(50);
  const needle = q.trim().toLowerCase();
  const rows = files.filter((f) => !needle || f.path.toLowerCase().includes(needle));
  return (
    <Stack gap="xs">
      {filterable && files.length > 8 && (
        <TextInput
          size="xs"
          w={260}
          placeholder={t("catalog.filterFiles")}
          value={q}
          onChange={(e) => {
            setQ(e.currentTarget.value);
            setLimit(50);
          }}
        />
      )}
      {rows.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("catalog.noMatch")}
        </Text>
      ) : (
        <ScrollArea>
          <Table verticalSpacing={6} horizontalSpacing="md" highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("catalog.fileHeader")}</Table.Th>
                <Table.Th>{t("catalog.sizeHeader")}</Table.Th>
                <Table.Th>{t("catalog.dateHeader")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {rows.slice(0, limit).map((f) => (
                <Table.Tr key={f.path}>
                  <Table.Td>
                    <Text size="sm" style={{ wordBreak: "break-all" }}>
                      {f.path}
                    </Text>
                  </Table.Td>
                  <Table.Td style={{ whiteSpace: "nowrap" }}>{fmtSize(f.size) || "—"}</Table.Td>
                  <Table.Td style={{ whiteSpace: "nowrap" }}>{f.created_at ? new Date(f.created_at).toLocaleDateString() : "—"}</Table.Td>
                  <Table.Td style={{ textAlign: "right" }}>
                    <Button size="compact-xs" variant="light" onClick={() => downloadArtifact(registry, f.path).catch((e) => onError(e.message))}>
                      {t("catalog.download")}
                    </Button>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </ScrollArea>
      )}
      {rows.length > limit && (
        <Anchor component="button" type="button" size="xs" onClick={() => setLimit(limit + 100)} style={{ alignSelf: "flex-start" }}>
          {t("catalog.showMore")} ({rows.length - limit})
        </Anchor>
      )}
    </Stack>
  );
}

/** Versions of one package, newest first; each opens to its files and usage. */
export function VersionList({ registry, versions, onError }: { registry: string; versions: VersionV[]; onError: (m: string) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState<string | null>(versions[0]?.version ?? null);
  return (
    <Stack gap="xs">
      {versions.map((v) => {
        const isOpen = open === v.version;
        const main = v.files.find((f) => f.path === v.primary);
        return (
          <Card key={v.version} padding="sm" withBorder>
            <Group justify="space-between" wrap="nowrap">
              <Group gap="xs" wrap="nowrap" style={{ cursor: "pointer", minWidth: 0 }} onClick={() => setOpen(isOpen ? null : v.version)}>
                <Text size="sm" fw={600}>
                  {isOpen ? "▾" : "▸"} {v.version}
                </Text>
                {isPre(v.version) && (
                  <Badge size="xs" variant="outline" color="gray">
                    {t("catalog.prerelease")}
                  </Badge>
                )}
                <Text size="xs" c="dimmed" truncate>
                  {v.created_at ? new Date(v.created_at).toLocaleDateString() : ""} · {t("catalog.filesCount", { count: v.files.length })}
                </Text>
              </Group>
              {v.primary && (
                <Button size="compact-xs" variant="light" onClick={() => downloadArtifact(registry, v.primary).catch((e) => onError(e.message))}>
                  {t("catalog.download")} {main && fmtSize(main.size) ? `· ${fmtSize(main.size)}` : ""}
                </Button>
              )}
            </Group>
            {isOpen && (
              <Stack gap="sm" mt="sm">
                <FilesTable registry={registry} files={v.files} onError={onError} filterable={false} />
                {v.usage && v.usage.length > 0 && <SnippetTabs items={v.usage} />}
              </Stack>
            )}
          </Card>
        );
      })}
    </Stack>
  );
}
