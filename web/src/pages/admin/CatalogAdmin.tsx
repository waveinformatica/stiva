import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  PageHeader, DataTable, Badge, Button, Modal, TextInput, Textarea, Switch, Select, NumberInput, Group, Stack,
  Text, ConfirmModal, ErrorAlert, PageLoader, AddButton, DeleteButton, FormFooter, Alert, ActionIcon,
} from "../../components/ui";
import { api, fetchImageUrl } from "../../lib/api";
import AppIcon from "../../components/AppIcon";
import { IconPlus } from "../../components/icons";

type Artifact = { registry: string; ref: string; tag: string; label: string; position?: number };
type Shot = { id: number; caption: string; position: number };
type App = {
  id: number;
  screenshots: Shot[];
  slug: string;
  name: string;
  tagline: string;
  description: string;
  icon: string;
  category: string;
  homepage: string;
  featured: boolean;
  position: number;
  artifacts: Artifact[];
};
type Reg = { name: string; format: string; type: string };

const EMPTY: App = {
  id: 0, slug: "", name: "", tagline: "", description: "", icon: "", category: "", homepage: "",
  featured: false, position: 0, artifacts: [], screenshots: [],
};

const slugify = (s: string) =>
  s.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60);

// Shrink an uploaded image to a 128×128 PNG so an icon stays a few kilobytes.
function toIcon(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    const url = URL.createObjectURL(file);
    img.onload = () => {
      const c = document.createElement("canvas");
      c.width = c.height = 128;
      const g = c.getContext("2d");
      if (!g) return reject(new Error("canvas unavailable"));
      const s = Math.min(img.width, img.height);
      g.drawImage(img, (img.width - s) / 2, (img.height - s) / 2, s, s, 0, 0, 128, 128);
      URL.revokeObjectURL(url);
      resolve(c.toDataURL("image/png"));
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("not an image"));
    };
    img.src = url;
  });
}

// Downscale a screenshot to at most 1600 px wide and keep it under the server's
// 512 KB limit by lowering the JPEG quality until it fits.
function toShot(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    const url = URL.createObjectURL(file);
    img.onload = () => {
      URL.revokeObjectURL(url);
      const scale = Math.min(1, 1600 / img.width);
      const c = document.createElement("canvas");
      c.width = Math.round(img.width * scale);
      c.height = Math.round(img.height * scale);
      const g = c.getContext("2d");
      if (!g) return reject(new Error("canvas unavailable"));
      g.fillStyle = "#fff";
      g.fillRect(0, 0, c.width, c.height);
      g.drawImage(img, 0, 0, c.width, c.height);
      for (const q of [0.85, 0.72, 0.6, 0.48]) {
        const data = c.toDataURL("image/jpeg", q);
        if ((data.length * 3) / 4 < 500 * 1024) return resolve(data);
      }
      reject(new Error("too large"));
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("not an image"));
    };
    img.src = url;
  });
}

function Thumb({ slug, id }: { slug: string; id: number }) {
  const [src, setSrc] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    let made = "";
    fetchImageUrl(slug, id)
      .then((u) => {
        if (!live) return URL.revokeObjectURL(u);
        made = u;
        setSrc(u);
      })
      .catch(() => {});
    return () => {
      live = false;
      if (made) URL.revokeObjectURL(made);
    };
  }, [slug, id]);
  return (
    <div style={{ width: 96, height: 60, borderRadius: 6, overflow: "hidden", background: "var(--mantine-color-default-hover)", flex: "0 0 auto" }}>
      {src && <img src={src} alt="" style={{ width: "100%", height: "100%", objectFit: "cover" }} />}
    </div>
  );
}

// Screenshots are changed immediately, apart from the rest of the form, so an
// image upload is never resent when the entry is saved.
function ShotsManager({ app, onChanged, onError }: { app: App; onChanged: () => Promise<void>; onError: (m: string) => void }) {
  const { t } = useTranslation();
  const ref = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const shots = app.screenshots || [];

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
      await onChanged();
    } catch (e: any) {
      onError(e.message);
    } finally {
      setBusy(false);
    }
  };
  const add = (files: FileList | null) => {
    if (!files) return;
    run(async () => {
      for (const f of Array.from(files).slice(0, Math.max(0, 8 - shots.length))) {
        await api.adminScreenshotAdd(app.id, await toShot(f), "");
      }
    });
  };
  const move = (i: number, d: number) => {
    const ids = shots.map((s) => s.id);
    [ids[i], ids[i + d]] = [ids[i + d], ids[i]];
    run(() => api.adminScreenshotOrder(app.id, ids));
  };

  return (
    <Stack gap="xs">
      <Group justify="space-between">
        <div>
          <Text fw={600}>{t("catalogAdmin.screenshotsTitle")}</Text>
          <Text size="xs" c="dimmed">
            {t("catalogAdmin.screenshotsDesc")} {t("catalogAdmin.shotNote")}
          </Text>
        </div>
        {app.id > 0 && (
          <>
            <input ref={ref} type="file" multiple accept="image/png,image/jpeg,image/webp,image/gif" hidden onChange={(e) => { add(e.target.files); e.target.value = ""; }} />
            <Button size="xs" variant="default" leftSection={<IconPlus size={14} />} disabled={busy || shots.length >= 8} onClick={() => ref.current?.click()}>
              {t("catalogAdmin.addScreenshot")}
            </Button>
          </>
        )}
      </Group>
      {app.id === 0 && <Alert color="blue" variant="light">{t("catalogAdmin.saveFirst")}</Alert>}
      {shots.map((s, i) => (
        <Group key={s.id} gap="xs" wrap="nowrap" align="center">
          <Thumb slug={app.slug} id={s.id} />
          <TextInput
            style={{ flex: 1 }}
            size="xs"
            placeholder={t("catalogAdmin.captionLabel")}
            defaultValue={s.caption}
            maxLength={200}
            onBlur={(e) => e.currentTarget.value !== s.caption && run(() => api.adminScreenshotCaption(app.id, s.id, e.currentTarget.value))}
          />
          <ActionIcon variant="subtle" disabled={busy || i === 0} aria-label={t("catalogAdmin.moveUp")} onClick={() => move(i, -1)}>↑</ActionIcon>
          <ActionIcon variant="subtle" disabled={busy || i === shots.length - 1} aria-label={t("catalogAdmin.moveDown")} onClick={() => move(i, 1)}>↓</ActionIcon>
          <ActionIcon color="red" variant="subtle" disabled={busy} aria-label={t("common.delete")} onClick={() => run(() => api.adminScreenshotDelete(app.id, s.id))}>×</ActionIcon>
        </Group>
      ))}
    </Stack>
  );
}

export default function CatalogAdmin() {
  const { t } = useTranslation();
  const [apps, setApps] = useState<App[]>([]);
  const [regs, setRegs] = useState<Reg[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<App>(EMPTY);
  const [slugTouched, setSlugTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [formErr, setFormErr] = useState<string | null>(null);
  const [confirmDel, setConfirmDel] = useState<App | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const load = () =>
    Promise.all([api.adminCatalog(), api.adminRegistries()])
      .then(([c, r]: any[]) => {
        setApps(c.apps || []);
        setRegs(r.registries || []);
        setErr(null);
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  useEffect(() => {
    load();
  }, []);

  // Reload the list, and keep the open form in step with the screenshots that changed.
  const reloadShots = async () => {
    const c: any = await api.adminCatalog();
    setApps(c.apps || []);
    const fresh = (c.apps || []).find((a: App) => a.id === form.id);
    if (fresh) setForm((f) => ({ ...f, screenshots: fresh.screenshots || [] }));
  };

  const fmt = (name: string) => regs.find((r) => r.name === name)?.format || "";
  const openNew = () => {
    setForm({ ...EMPTY, artifacts: [{ registry: regs[0]?.name || "", ref: "", tag: "", label: "" }] });
    setSlugTouched(false);
    setFormErr(null);
    setOpen(true);
  };
  const openEdit = (a: App) => {
    setForm({ ...EMPTY, ...a, artifacts: a.artifacts.map((x) => ({ ...x })), screenshots: a.screenshots || [] });
    setSlugTouched(true);
    setFormErr(null);
    setOpen(true);
  };
  const setArt = (i: number, patch: Partial<Artifact>) =>
    setForm({ ...form, artifacts: form.artifacts.map((x, j) => (j === i ? { ...x, ...patch } : x)) });

  const save = async (): Promise<void> => {
    setBusy(true);
    setFormErr(null);
    try {
      const { id, ...body } = form;
      const saved: App = await api.adminCatalogSave(
        { ...body, artifacts: form.artifacts.filter((a) => a.ref.trim()) },
        id || undefined,
      );
      await load();
      // A new entry stays open, now with an id, so screenshots can be added to it.
      if (!id && saved?.id) openEdit(saved);
      else setOpen(false);
    } catch (e: any) {
      setFormErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const pickIcon = async (f: File | undefined) => {
    if (!f) return;
    try {
      setForm({ ...form, icon: await toIcon(f) });
      setFormErr(null);
    } catch {
      setFormErr(t("catalogAdmin.iconBad"));
    }
  };

  if (loading) return <PageLoader />;

  return (
    <div>
      <PageHeader
        title={t("catalogAdmin.title")}
        description={t("catalogAdmin.description")}
        actions={<AddButton onClick={openNew}>{t("catalogAdmin.newApp")}</AddButton>}
      />
      {err && <ErrorAlert onClose={() => setErr(null)}>{err}</ErrorAlert>}
      <Alert color="blue" variant="light" mb="md">
        {t("catalogAdmin.visibleHint")}
      </Alert>

      <DataTable<App>
        rows={apps}
        rowKey={(a) => String(a.id)}
        empty={t("catalogAdmin.empty")}
        columns={[
          {
            header: t("catalogAdmin.nameLabel"),
            render: (a) => (
              <Group gap="sm" wrap="nowrap">
                <AppIcon name={a.name} icon={a.icon} size={36} />
                <Stack gap={0}>
                  <Group gap="xs">
                    <Text fw={500}>{a.name}</Text>
                    {a.featured && <Badge color="yellow">★</Badge>}
                  </Group>
                  <Text size="xs" c="dimmed">/catalog/{a.slug}</Text>
                </Stack>
              </Group>
            ),
          },
          { header: t("catalogAdmin.categoryLabel"), render: (a) => a.category || "—" },
          { header: t("catalogAdmin.artifactsTitle"), render: (a) => a.artifacts.length, width: 100 },
          {
            header: "",
            render: (a) => (
              <Group gap={4} justify="flex-end">
                <Button size="compact-xs" variant="light" color="gray" component={Link} to={`/catalog/${a.slug}`}>
                  {t("catalogAdmin.view")}
                </Button>
                <Button size="compact-xs" variant="default" onClick={() => openEdit(a)}>
                  {t("common.edit")}
                </Button>
                <DeleteButton onClick={() => setConfirmDel(a)} />
              </Group>
            ),
          },
        ]}
      />

      <Modal opened={open} onClose={() => setOpen(false)} title={form.id ? t("catalogAdmin.editTitle") : t("catalogAdmin.newApp")} size="xl">
        <Stack>
          {formErr && <ErrorAlert mb="0">{formErr}</ErrorAlert>}

          <Group align="flex-start" wrap="nowrap" gap="md">
            <Stack gap={6} align="center" w={120}>
              <AppIcon name={form.name || "?"} icon={form.icon} size={88} />
              <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden onChange={(e) => pickIcon(e.target.files?.[0])} />
              <Button size="compact-xs" variant="default" onClick={() => fileRef.current?.click()}>
                {t("catalogAdmin.iconUpload")}
              </Button>
              {form.icon && (
                <Button size="compact-xs" variant="subtle" color="red" onClick={() => setForm({ ...form, icon: "" })}>
                  {t("catalogAdmin.iconRemove")}
                </Button>
              )}
            </Stack>
            <Stack gap="sm" style={{ flex: 1 }}>
              <Group grow align="flex-start">
                <TextInput
                  label={t("catalogAdmin.nameLabel")}
                  value={form.name}
                  onChange={(e) => {
                    const name = e.currentTarget.value;
                    setForm({ ...form, name, slug: slugTouched ? form.slug : slugify(name) });
                  }}
                />
                <TextInput
                  label={t("catalogAdmin.slugLabel")}
                  value={form.slug}
                  onChange={(e) => {
                    setSlugTouched(true);
                    setForm({ ...form, slug: e.currentTarget.value });
                  }}
                />
              </Group>
              <TextInput
                label={t("catalogAdmin.taglineLabel")}
                value={form.tagline}
                maxLength={200}
                onChange={(e) => setForm({ ...form, tagline: e.currentTarget.value })}
              />
              <Text size="xs" c="dimmed">{t("catalogAdmin.iconNote")}</Text>
              {!form.icon.startsWith("data:") && (
                <TextInput
                  size="xs"
                  label={t("catalogAdmin.iconUrl")}
                  placeholder="https://…"
                  value={form.icon}
                  onChange={(e) => setForm({ ...form, icon: e.currentTarget.value })}
                />
              )}
            </Stack>
          </Group>

          <Textarea
            label={t("catalogAdmin.descriptionLabel")}
            description={t("catalogAdmin.descriptionDesc")}
            autosize
            minRows={5}
            maxRows={14}
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.currentTarget.value })}
          />

          <Group grow align="flex-start">
            <TextInput label={t("catalogAdmin.categoryLabel")} value={form.category} maxLength={40} onChange={(e) => setForm({ ...form, category: e.currentTarget.value })} />
            <TextInput label={t("catalogAdmin.homepageLabel")} placeholder="https://…" value={form.homepage} onChange={(e) => setForm({ ...form, homepage: e.currentTarget.value })} />
            <NumberInput label={t("catalogAdmin.positionLabel")} value={form.position} onChange={(v) => setForm({ ...form, position: typeof v === "number" ? v : 0 })} />
          </Group>
          <Switch label={t("catalogAdmin.featuredLabel")} checked={form.featured} onChange={(e) => setForm({ ...form, featured: e.currentTarget.checked })} />

          <Stack gap="xs">
            <Group justify="space-between">
              <div>
                <Text fw={600}>{t("catalogAdmin.artifactsTitle")}</Text>
                <Text size="xs" c="dimmed">{t("catalogAdmin.artifactsDesc")}</Text>
              </div>
              <Button size="xs" variant="default" leftSection={<IconPlus size={14} />} onClick={() => setForm({ ...form, artifacts: [...form.artifacts, { registry: regs[0]?.name || "", ref: "", tag: "", label: "" }] })}>
                {t("catalogAdmin.addArtifact")}
              </Button>
            </Group>
            {form.artifacts.length === 0 && <Alert color="yellow" variant="light">{t("catalogAdmin.noArtifacts")}</Alert>}
            {form.artifacts.map((a, i) => (
              <Group key={i} gap="xs" align="flex-end" wrap="nowrap">
                <Select
                  label={i === 0 ? t("catalogAdmin.registryLabel") : undefined}
                  w={170}
                  data={regs.map((r) => ({ value: r.name, label: `${r.name} (${r.format})` }))}
                  value={a.registry}
                  onChange={(v) => setArt(i, { registry: v || "" })}
                  allowDeselect={false}
                />
                <TextInput
                  style={{ flex: 2 }}
                  label={i === 0 ? t("catalogAdmin.refLabel") : undefined}
                  placeholder={fmt(a.registry) === "oci" ? "acme/app" : "downloads/acme/"}
                  value={a.ref}
                  onChange={(e) => setArt(i, { ref: e.currentTarget.value })}
                />
                {fmt(a.registry) === "oci" && (
                  <TextInput
                    w={110}
                    label={i === 0 ? t("catalogAdmin.tagLabel") : undefined}
                    value={a.tag}
                    onChange={(e) => setArt(i, { tag: e.currentTarget.value })}
                  />
                )}
                <TextInput
                  style={{ flex: 1 }}
                  label={i === 0 ? t("catalogAdmin.labelLabel") : undefined}
                  value={a.label}
                  onChange={(e) => setArt(i, { label: e.currentTarget.value })}
                />
                <ActionIcon color="red" variant="subtle" aria-label={t("common.delete")} onClick={() => setForm({ ...form, artifacts: form.artifacts.filter((_, j) => j !== i) })}>
                  ×
                </ActionIcon>
              </Group>
            ))}
          </Stack>

          <ShotsManager app={form} onChanged={reloadShots} onError={setFormErr} />

          <FormFooter onCancel={() => setOpen(false)} loading={busy} onSubmit={save}>
            {t("common.save")}
          </FormFooter>
        </Stack>
      </Modal>

      <ConfirmModal
        opened={!!confirmDel}
        title={t("catalogAdmin.deleteTitle")}
        message={confirmDel ? t("catalogAdmin.deleteBody", { name: confirmDel.name }) : ""}
        danger
        onConfirm={() => {
          const a = confirmDel!;
          setConfirmDel(null);
          api.adminCatalogDelete(a.id).then(load).catch((e) => setErr(e.message));
        }}
        onCancel={() => setConfirmDel(null)}
      />
    </div>
  );
}
