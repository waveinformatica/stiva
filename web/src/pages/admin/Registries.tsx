import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Card,
  PageHeader,
  DataTable,
  Badge,
  Button,
  Modal,
  Group,
  TextInput,
  PasswordInput,
  Select,
  MultiSelect,
  Switch,
  Textarea,
  Alert,
  Loader,
  ActionIcon,
  Stack,
  ScrollArea,
  Code,
  Text,
} from "../../components/ui";
import { api } from "../../lib/api";
import { BlobStoreForm, emptyStore, type BlobStore } from "../../components/BlobStoreForm";
import { IconPlus, IconTrash } from "../../components/icons";

type Reg = {
  name: string;
  format: string;
  type: string;
  online: boolean;
  default?: boolean;
  hosts?: string[];
  port?: number;
  blob?: any;
  blob_store?: string;
  remote_url?: string;
  remote_user?: string;
  remote_pass?: string;
  remote_token?: string;
  proxy_allow_write?: boolean;
  cache_default_upstream?: string;
  cache_upstreams?: Record<string, { url: string; user?: string; pass?: string; token?: string; insecure?: boolean }>;
  members: string[];
  write_member: string;
  base_path: string;
};

// Parse the "host=url user pass" per-line upstream textarea into the map.
function parseUpstreams(text: string): Record<string, any> {
  const out: Record<string, any> = {};
  text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean)
    .forEach((line) => {
      const eq = line.indexOf("=");
      if (eq < 0) return;
      const host = line.slice(0, eq).trim();
      const rest = line
        .slice(eq + 1)
        .trim()
        .split(/\s+/);
      const def: any = { url: rest[0] };
      if (rest[1]) def.user = rest[1];
      if (rest[2]) def.pass = rest[2];
      out[host] = def;
    });
  return out;
}

function serializeUpstreams(map?: Record<string, any>): string {
  if (!map) return "";
  return Object.entries(map)
    .map(([h, d]) => (d.user ? `${h}=${d.url} ${d.user} ${d.pass || ""}` : `${h}=${d.url}`))
    .join("\n");
}

const emptyForm = (): Reg => ({
  name: "",
  format: "oci",
  type: "hosted",
  online: true,
  default: false,
  hosts: [],
  port: 0,
  blob: { type: "file", root: "" },
  remote_url: "",
  proxy_allow_write: false,
  cache_default_upstream: "",
  cache_upstreams: {},
  members: [],
  write_member: "",
  base_path: "",
});

export default function Registries() {
  const { t } = useTranslation();
  const [list, setList] = useState<Reg[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [editName, setEditName] = useState<string | null>(null);
  const [form, setForm] = useState<Reg>(emptyForm());
  // Storage is a reference to a named store, never configuration typed in here.
  const [storeName, setStoreName] = useState<string>("");
  const [storeList, setStoreList] = useState<any[]>([]);
  const [newStore, setNewStore] = useState<BlobStore | null>(null);
  const [hostsText, setHostsText] = useState("");
  const [portText, setPortText] = useState("");
  const [upstreamsText, setUpstreamsText] = useState("");
  const [gcName, setGcName] = useState<string | null>(null);
  const [gcOlder, setGcOlder] = useState("1h");
  const [gcReport, setGcReport] = useState<any>(null);
  const [gcBusy, setGcBusy] = useState(false);
  const [aptKey, setAptKey] = useState<any>(null);
  const [aptBusy, setAptBusy] = useState(false);

  const load = () => {
    setLoading(true);
    setErr(null);
    api
      .adminRegistries()
      .then((r) => setList(r.registries))
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, []);
  useEffect(() => {
    api
      .blobStores()
      .then((r: any) => setStoreList(r.blob_stores || []))
      .catch(() => setStoreList([]));
  }, []);

  const openNew = () => {
    setEditName(null);
    setForm(emptyForm());
    setStoreName("");
    setNewStore(null);
    setHostsText("");
    setPortText("");
    setUpstreamsText("");
    setAptKey(null);
    setOpen(true);
  };

  const openEdit = (r: Reg) => {
    setEditName(r.name);
    setForm({ ...emptyForm(), ...r });
    setStoreName(r.blob_store || "");
    setNewStore(null);
    setHostsText((r.hosts || []).join(", "));
    setPortText(r.port ? String(r.port) : "");
    setUpstreamsText(serializeUpstreams(r.cache_upstreams));
    setAptKey(null);
    if (r.format === "apt" && r.type === "hosted") loadAptKey(r.name);
    setOpen(true);
  };

  const loadAptKey = (name: string) => {
    setAptBusy(true);
    api
      .aptKey(name)
      .then((k) => setAptKey(k))
      .catch(() => setAptKey({ missing: true }))
      .finally(() => setAptBusy(false));
  };

  const save = () => {
    const payload: any = {
      ...form,
      hosts: hostsText.split(",").map((s) => s.trim()).filter(Boolean),
      port: portText ? parseInt(portText, 10) : 0,
    };
    delete payload.blob;
    if (payload.type === "hosted" || payload.type === "proxy" || payload.type === "cache") {
      payload.blob_store = storeName;
    }
    if (payload.type !== "proxy") {
      delete payload.remote_url;
      delete payload.remote_user;
      delete payload.remote_pass;
      delete payload.remote_token;
      delete payload.proxy_allow_write;
    }
    if (payload.type !== "group") {
      delete payload.members;
      delete payload.write_member;
    }
    if (payload.type === "cache") {
      payload.cache_default_upstream = form.cache_default_upstream || undefined;
      payload.cache_upstreams = parseUpstreams(upstreamsText);
    } else {
      delete payload.cache_default_upstream;
      delete payload.cache_upstreams;
    }
    const call = editName
      ? api.adminUpdateRegistry(editName, payload)
      : api.adminCreateRegistry(payload);
    call
      .then(() => {
        setOpen(false);
        load();
      })
      .catch((e) => setErr(e.message));
  };

  const del = (name: string) => {
    if (!confirm(t("registries.deletePrompt", { name }))) return;
    api.adminDeleteRegistry(name).then(load).catch((e) => setErr(e.message));
  };

  const openGC = (name: string) => {
    setGcName(name);
    setGcReport(null);
    setErr(null);
    runGC(name, gcOlder, true);
  };

  const runGC = (name: string, olderThan: string, dryRun: boolean) => {
    setGcBusy(true);
    setErr(null);
    api
      .adminGC({ registry: name, older_than: olderThan, dry_run: dryRun })
      .then((r) => setGcReport(r.registries?.[0] || r))
      .catch((e) => setErr(e.message))
      .finally(() => setGcBusy(false));
  };

  const aptClientSnippet = () => {
    const host = hostsText.split(",").map((s) => s.trim()).filter(Boolean)[0] || form.name || "host";
    const base = form.base_path ? `/${form.base_path}` : "";
    const repo = `https://${host}${base}/`;
    const keyring = `/usr/share/keyrings/${form.name || "registry"}.gpg`;
    return `curl -fsSL ${repo}KEY.gpg | gpg --dearmor | sudo tee ${keyring} > /dev/null\ndeb [signed-by=${keyring}] ${repo} ./`;
  };

  if (loading) return <Loader />;
  if (err) return <Alert color="red" title={t("common.error")}>{err}</Alert>;

  return (
    <div>
      <PageHeader
        title={t("registries.title")}
        actions={
          <Button size="xs" leftSection={<IconPlus size={14} />} onClick={openNew}>
            {t("registries.newRegistry")}
          </Button>
        }
      />
      {err && <Alert color="red" mb="md" title={t("common.error")}>{err}</Alert>}
      <DataTable
        rowKey={(r) => r.name}
        rows={list}
        columns={[
          { header: t("registries.nameHeader"), render: (r) => <b>{r.name}</b> },
          {
            header: t("registries.typeHeader"),
            render: (r) => (
              <Badge color={r.type === "group" ? "grape" : r.type === "proxy" ? "orange" : r.type === "cache" ? "cyan" : "indigo"}>
                {r.type}
              </Badge>
            ),
          },
          {
            header: t("registries.formatHeader"),
            render: (r) => <Badge variant="outline">{r.format}</Badge>,
          },
          {
            header: t("registries.statusHeader"),
            render: (r) => (
              <Group gap={4}>
                {r.online ? <Badge color="green">{t("registries.online")}</Badge> : <Badge color="gray">{t("registries.offline")}</Badge>}
                {r.default && <Badge color="teal">{t("registries.defaultBadge")}</Badge>}
                {(r.port ?? 0) > 0 && <Badge variant="outline">:{r.port ?? 0}</Badge>}
              </Group>
            ),
          },
          {
            header: "",
            render: (r) => (
              <Group gap={4} justify="flex-end">
                {r.type === "cache" && (
                  <Button
                    size="compact-xs"
                    variant="light"
                    color="cyan"
                    onClick={() => {
                      const img = window.prompt(t("registries.warmPrompt"));
                      if (img) api.adminWarmRegistry(r.name, img).then(load).catch((e) => setErr(e.message));
                    }}
                  >
                    Warm
                  </Button>
                )}
                <Button size="compact-xs" variant="light" color="gray" onClick={() => openGC(r.name)}>
                  GC
                </Button>
                <Button size="compact-xs" variant="default" onClick={() => openEdit(r)}>
                  {t("common.edit")}
                </Button>
                <ActionIcon color="red" variant="subtle" onClick={() => del(r.name)}>
                  <IconTrash size={14} />
                </ActionIcon>
              </Group>
            ),
          },
        ]}
      />

      <Modal opened={open} onClose={() => setOpen(false)} title={editName ? t("registries.editTitle", { name: editName }) : t("registries.newTitle")} size="lg">
        <Group gap="sm" grow>
          <TextInput
            label={t("registries.nameLabel")}
            value={form.name}
            disabled={!!editName}
            onChange={(e) => setForm({ ...form, name: e.currentTarget.value })}
          />
          <Select
            label={t("registries.typeLabel")}
            data={form.format === "oci" ? ["hosted", "proxy", "group", "cache"] : ["hosted", "proxy", "group"]}
            value={form.type}
            onChange={(v) => setForm({ ...form, type: v || "hosted" })}
            allowDeselect={false}
          />
          <Select
            label={t("registries.formatLabel")}
            data={[
              { value: "oci", label: "oci (container images)" },
              { value: "helm", label: "helm (chart repo)" },
              { value: "maven", label: "maven (jar/pom repo)" },
              { value: "npm", label: "npm (package repo)" },
              { value: "pypi", label: "pypi (python packages)" },
              { value: "go", label: "go (modules)" },
              { value: "raw", label: "raw (binary store)" },
              { value: "nuget", label: "nuget (.nupkg)" },
              { value: "rubygems", label: "rubygems (.gem)" },
              { value: "composer", label: "composer (php)" },
              { value: "conda", label: "conda (packages)" },
              { value: "apt", label: "apt (debian .deb)" },
              { value: "yum", label: "yum (rpm)" },
              { value: "conan", label: "conan (C/C++)" },
              { value: "cocoapods", label: "cocoapods (iOS)" },
              { value: "cran", label: "cran (R)" },
              { value: "elpa", label: "elpa (emacs)" },
              { value: "p2", label: "p2 (eclipse)" },
              { value: "opkg", label: "opkg (embedded)" },
              { value: "chef", label: "chef (cookbooks)" },
              { value: "puppet", label: "puppet (modules)" },
              { value: "vagrant", label: "vagrant (boxes)" },
              { value: "sbt", label: "sbt (scala)" },
              { value: "ivy", label: "ivy" },
              { value: "gradle", label: "gradle" },
              { value: "git-lfs", label: "git-lfs" },
            ]}
            value={form.format}
            onChange={(v) => {
              const f = v || "oci";
              setForm({
                ...form,
                format: f,
                base_path: f === "oci" ? "" : form.base_path || form.name,
              });
            }}
            allowDeselect={false}
            w={200}
          />
        </Group>

        <Group gap="sm" mt="sm">
          <Switch label={t("registries.onlineLabel")} checked={form.online} onChange={(e) => setForm({ ...form, online: e.currentTarget.checked })} />
          <Switch label={t("registries.defaultLabel")} checked={!!form.default} onChange={(e) => setForm({ ...form, default: e.currentTarget.checked })} />
        </Group>

        <Group gap="sm" mt="sm">
          <TextInput
            label={t("registries.hostsLabel")}
            placeholder={t("registries.hostsPlaceholder")}
            value={hostsText}
            onChange={(e) => setHostsText(e.currentTarget.value)}
            style={{ flex: 1 }}
          />
          <TextInput
            label={t("registries.portLabel")}
            placeholder={t("registries.portPlaceholder")}
            value={portText}
            onChange={(e) => setPortText(e.currentTarget.value)}
            w={160}
          />
        </Group>

        {form.format !== "oci" ? (
          <TextInput
            label={t("registries.basePathLabel")}
            description={t("registries.basePathDesc")}
            placeholder={form.name ? t("registries.basePathPlaceholder", { name: form.name }) : t("registries.basePathPlaceholderEmpty")}
            mt="sm"
            value={form.base_path || ""}
            onChange={(e) => setForm({ ...form, base_path: e.currentTarget.value })}
          />
        ) : null}

        {form.type === "hosted" || form.type === "proxy" || form.type === "cache" ? (
          <div style={{ marginTop: "0.75rem" }}>
            <Group gap="sm" align="flex-end">
              <Select
                label={t("registries.blobStoreLabel")}
                placeholder={storeList.length ? t("registries.blobStorePlaceholder") : t("registries.blobStoreEmpty")}
                data={storeList.map((s: any) => ({
                  value: s.name,
                  label: `${s.name} (${s.kind})`,
                }))}
                value={storeName || null}
                onChange={(v) => {
                  setStoreName(v || "");
                  setNewStore(null);
                }}
                allowDeselect={false}
                style={{ flex: 1 }}
              />
              <Button
                variant={newStore ? "filled" : "default"}
                onClick={() => setNewStore(newStore ? null : emptyStore())}
              >
                {newStore ? t("common.cancel") : t("registries.newStore")}
              </Button>
            </Group>

            {newStore && (
              <Card withBorder mt="sm" padding="md">
                <BlobStoreForm
                  value={newStore}
                  onChange={setNewStore}
                  submitLabel={t("registries.createAndUse")}
                  onSaved={(saved) => {
                    // Created here, selected here: the registry form never asks
                    // for storage details of its own.
                    setStoreName(saved.name);
                    setNewStore(null);
                    api.blobStores().then((r: any) => setStoreList(r.blob_stores || []));
                  }}
                />
              </Card>
            )}

            <Text size="xs" c="dimmed" mt={6}>
              {t("registries.storeNote")}
            </Text>
          </div>
        ) : null}

        {form.type === "proxy" ? (
          <div>
            <TextInput
              label={t("registries.remoteUrlLabel")}
              placeholder="https://registry-1.docker.io"
              mt="sm"
              value={form.remote_url || ""}
              onChange={(e) => setForm({ ...form, remote_url: e.currentTarget.value })}
            />
            <Group gap="sm" mt="sm">
              <TextInput label={t("registries.remoteUserLabel")} value={form.remote_user || ""} onChange={(e) => setForm({ ...form, remote_user: e.currentTarget.value })} style={{ flex: 1 }} />
              <PasswordInput label={t("registries.remotePasswordLabel")} value={form.remote_pass || ""} onChange={(e) => setForm({ ...form, remote_pass: e.currentTarget.value })} style={{ flex: 1 }} />
            </Group>
            <Group gap="sm" mt="sm">
              <TextInput label={t("registries.remoteTokenLabel")} value={form.remote_token || ""} onChange={(e) => setForm({ ...form, remote_token: e.currentTarget.value })} style={{ flex: 1 }} />
              <Switch mt="lg" label={t("registries.allowWritesLabel")} checked={!!form.proxy_allow_write} onChange={(e) => setForm({ ...form, proxy_allow_write: e.currentTarget.checked })} />
            </Group>
          </div>
        ) : null}

        {form.type === "cache" ? (
          <div>
            <TextInput
              label={t("registries.defaultUpstreamLabel")}
              placeholder="https://registry-1.docker.io"
              mt="sm"
              value={form.cache_default_upstream || ""}
              onChange={(e) => setForm({ ...form, cache_default_upstream: e.currentTarget.value })}
            />
            <Textarea
              label={t("registries.upstreamsLabel")}
              placeholder={"gcr.io=https://gcr.io\nquay.io=https://quay.io\nmyreg:5000=http://myreg:5000 insecure"}
              mt="sm"
              autosize
              minRows={3}
              value={upstreamsText}
              onChange={(e) => setUpstreamsText(e.currentTarget.value)}
            />
            <Alert color="blue" mt="sm" title={t("registries.cacheTitle")}>
              {t("registries.cacheBody")}
            </Alert>
          </div>
        ) : null}

        {form.type === "group" ? (
          <div>
            <MultiSelect
              label={t("registries.membersLabel")}
              description={t("registries.membersDesc")}
              mt="sm"
              data={list.filter((r) => r.name !== editName).map((r) => r.name)}
              value={form.members || []}
              onChange={(v) =>
                setForm({
                  ...form,
                  members: v,
                  // The write target must be one of the members, otherwise the
                  // definition is rejected on save.
                  write_member: v.includes(form.write_member) ? form.write_member : v[0] || "",
                })
              }
              placeholder={list.length > 1 ? t("registries.membersPlaceholder") : t("registries.membersEmpty")}
              searchable
              clearable
              nothingFoundMessage={t("registries.noMatch")}
            />
            <Select
              label={t("registries.writeMemberLabel")}
              mt="sm"
              data={(form.members || []).filter((m) => list.find((r) => r.name === m)?.type !== "group")}
              value={form.write_member || null}
              onChange={(v) => setForm({ ...form, write_member: v ?? "" })}
              allowDeselect
            />
            {!(form.members || []).length ? (
              <Text size="xs" c="dimmed" mt={6}>
                {t("registries.groupNeedsMember")}
              </Text>
            ) : null}
          </div>
        ) : null}

        {form.format === "apt" && form.type === "hosted" ? (
          <div>
            <Text fw={600} mt="md">{t("registries.signingTitle")}</Text>
            {!editName ? (
              <Text size="sm" c="dimmed">{t("registries.saveFirstSigning")}</Text>
            ) : aptBusy && !aptKey ? (
              <Loader size="sm" />
            ) : aptKey?.missing ? (
              <Group gap="sm" mt="xs" align="flex-end">
                <Text size="sm" c="dimmed" style={{ flex: 1 }}>
                  {t("registries.noSigningKey")}
                </Text>
                <Button
                  size="xs"
                  loading={aptBusy}
                  onClick={() => api.aptKeyCreate(editName).then(() => loadAptKey(editName)).catch((e) => setErr(e.message))}
                >
                  {t("registries.generateKey")}
                </Button>
              </Group>
            ) : aptKey ? (
              <Stack gap="xs" mt="xs">
                <Group gap="xs">
                  <Text size="sm" c="dimmed">{t("registries.fingerprint")}</Text>
                  <Code>{aptKey.fingerprint}</Code>
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="red"
                    loading={aptBusy}
                    onClick={() => {
                      if (editName && confirm(t("registries.deleteKeyPrompt", { name: editName }))) {
                        api.aptKeyDelete(editName).then(() => loadAptKey(editName)).catch((e) => setErr(e.message));
                      }
                    }}
                  >
                    Delete
                  </Button>
                </Group>
                <Text size="xs" c="dimmed">{t("registries.pointApt")}</Text>
                <Code block style={{ overflowWrap: "anywhere" }}>{aptClientSnippet()}</Code>
              </Stack>
            ) : null}
          </div>
        ) : null}

        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setOpen(false)}>
            {t("common.cancel")}
          </Button>
          <Button onClick={save}>{t("common.save")}</Button>
        </Group>
      </Modal>

      <Modal opened={gcName !== null} onClose={() => setGcName(null)} title={t("registries.gcTitle", { name: gcName || "" })} size="lg">
        <Stack gap="sm">
          <Text size="sm" c="dimmed">
            {t("registries.gcIntro")}
          </Text>
          <Group gap="sm" align="flex-end">
            <TextInput
              label={t("registries.graceLabel")}
              description={t("registries.graceDesc")}
              value={gcOlder}
              onChange={(e) => setGcOlder(e.currentTarget.value)}
              w={200}
            />
            <Button variant="default" size="xs" loading={gcBusy} onClick={() => gcName && runGC(gcName, gcOlder, true)}>
              {t("registries.preview")}
            </Button>
          </Group>
          {gcBusy && !gcReport && <Loader size="sm" />}
          {gcReport?.skipped ? (
            <Text size="sm" c="dimmed">{gcReport.skipped}.</Text>
          ) : gcReport ? (
            <div>
              <Group gap="xs" mb="xs">
                <Badge color={(gcReport.orphans ?? 0) > 0 ? "yellow" : "green"}>
                  {t("registries.orphanCount", { count: gcReport.orphans ?? 0 })}
                </Badge>
                <Text size="sm" c="dimmed">{humanSize(gcReport.orphan_bytes ?? 0)}</Text>
                {(gcReport.deleted ?? 0) > 0 && (
                  <Badge color="teal">
                    {t("registries.deletedCount", { count: gcReport.deleted, size: humanSize(gcReport.deleted_bytes ?? 0) })}
                  </Badge>
                )}
                {gcReport.truncated && <Badge variant="light">{t("registries.truncated")}</Badge>}
              </Group>
              {(gcReport.blobs || []).length > 0 && (
                <ScrollArea.Autosize mah={240}>
                  <DataTable
                    empty={t("registries.noOrphans")}
                    rowKey={(b: any) => b.digest}
                    rows={gcReport.blobs}
                    columns={[
                      { header: t("registries.digestHeader"), render: (b: any) => <Code>{shortDigest(b.digest)}</Code> },
                      { header: t("registries.sizeHeader"), render: (b: any) => humanSize(b.size) },
                    ]}
                  />
                </ScrollArea.Autosize>
              )}
              {(gcReport.errors || []).length > 0 && (
                <Alert color="red" mt="sm" title={t("registries.errorsTitle")}>
                  {(gcReport.errors || []).join("; ")}
                </Alert>
              )}
              <Group justify="flex-end" mt="md">
                <Button
                  color="orange"
                  loading={gcBusy}
                  disabled={(gcReport.orphans ?? 0) === 0}
                  onClick={() => {
                    if (gcName && confirm(t("registries.deleteOrphansPrompt", { count: gcReport.orphans, size: humanSize(gcReport.orphan_bytes ?? 0), name: gcName }))) {
                      runGC(gcName, gcOlder, false);
                    }
                  }}
                >
                  {t("registries.runGc")}
                </Button>
              </Group>
            </div>
          ) : null}
        </Stack>
      </Modal>
    </div>
  );
}

function humanSize(n: number) {
  if (!n) return "0 B";
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

function shortDigest(d?: string) {
  if (!d) return "—";
  return d.startsWith("sha256:") ? d.slice(7, 19) : d.slice(0, 12);
}
