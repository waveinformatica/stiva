import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Button, Group, Modal, Progress, Stack, Switch, Text, TextInput } from "@mantine/core";
import { uploadArtifact } from "../lib/api";
import { fmtSize } from "./ArtifactParts";

type Item = { file: File; state: "queued" | "uploading" | "done" | "failed"; progress: number; message?: string };

const HINT: Record<string, string> = {
  npm: "upload.hintNpm",
  pypi: "upload.hintPypi",
  conan: "upload.hintConan",
  maven: "upload.hintMaven",
  gradle: "upload.hintMaven",
  p2: "upload.hintP2",
};
// Formats where the server decides the destination from the file itself.
const AUTO_PLACED = new Set(["npm", "pypi", "conan"]);

/** Upload files into a path-addressed registry from the page, one at a time,
 *  with progress and the server's answer for each. */
export default function UploadModal({
  opened,
  registry,
  format,
  anonymous,
  onClose,
  onUploaded,
}: {
  opened: boolean;
  registry: string;
  format: string;
  anonymous: boolean;
  onClose: () => void;
  onUploaded: () => void;
}) {
  const { t } = useTranslation();
  const [items, setItems] = useState<Item[]>([]);
  const [dir, setDir] = useState("");
  const [overwrite, setOverwrite] = useState(false);
  const [busy, setBusy] = useState(false);
  const [over, setOver] = useState(false);
  const input = useRef<HTMLInputElement>(null);

  const auto = AUTO_PLACED.has(format);
  const blocked = format === "conan";
  const pending = items.filter((i) => i.state === "queued" || i.state === "failed").length;

  const add = (files: FileList | File[] | null) => {
    if (!files) return;
    const fresh = Array.from(files).map((file) => ({ file, state: "queued" as const, progress: 0 }));
    setItems((cur) => [...cur, ...fresh]);
  };
  const patch = (idx: number, p: Partial<Item>) => setItems((cur) => cur.map((it, i) => (i === idx ? { ...it, ...p } : it)));

  const run = async () => {
    setBusy(true);
    let any = false;
    for (let i = 0; i < items.length; i++) {
      if (items[i].state === "done") continue;
      patch(i, { state: "uploading", progress: 0, message: undefined });
      try {
        const r = await uploadArtifact(registry, dir, items[i].file, overwrite, (f) => patch(i, { progress: f }));
        patch(i, { state: "done", progress: 1, message: t("upload.done", { path: r.path }) });
        any = true;
      } catch (e: any) {
        patch(i, { state: "failed", message: t("upload.failed", { error: e.message }) });
      }
    }
    setBusy(false);
    if (any) onUploaded();
  };

  const close = () => {
    if (busy) return;
    setItems([]);
    onClose();
  };

  return (
    <Modal opened={opened} onClose={close} title={t("upload.title", { registry })} size="lg">
      <Stack gap="sm">
        {anonymous && <Alert color="yellow">{t("upload.signIn")}</Alert>}
        <Text size="xs" c="dimmed">
          {t(HINT[format] ?? "upload.hintDefault")}
        </Text>

        {!auto && (
          <TextInput
            label={t("upload.destination")}
            description={t("upload.destinationDesc")}
            placeholder="builds/linux/"
            value={dir}
            onChange={(e) => setDir(e.currentTarget.value)}
          />
        )}

        {!blocked && (
          <div
            onDragOver={(e) => {
              e.preventDefault();
              setOver(true);
            }}
            onDragLeave={() => setOver(false)}
            onDrop={(e) => {
              e.preventDefault();
              setOver(false);
              add(e.dataTransfer.files);
            }}
            style={{
              border: `2px dashed ${over ? "var(--mantine-primary-color-filled)" : "var(--mantine-color-default-border)"}`,
              borderRadius: 8,
              padding: "20px 12px",
              textAlign: "center",
            }}
          >
            <Text size="sm" c="dimmed">
              {t("upload.drop")}{" "}
              <Button variant="subtle" size="compact-sm" onClick={() => input.current?.click()}>
                {t("upload.choose")}
              </Button>
            </Text>
            <input ref={input} type="file" multiple hidden onChange={(e) => { add(e.target.files); e.target.value = ""; }} />
          </div>
        )}

        {items.length > 0 && (
          <Stack gap={6}>
            {items.map((it, i) => (
              <div key={i}>
                <Group justify="space-between" wrap="nowrap">
                  <Text size="sm" truncate>
                    {it.file.name} <Text span c="dimmed" size="xs">{fmtSize(it.file.size)}</Text>
                  </Text>
                  {it.state === "queued" && <Text size="xs" c="dimmed">{t("upload.queued")}</Text>}
                  {it.state === "uploading" && <Text size="xs">{t("upload.uploading")}</Text>}
                </Group>
                {it.state === "uploading" && <Progress value={it.progress * 100} size="xs" mt={2} />}
                {it.message && (
                  <Text size="xs" c={it.state === "failed" ? "red" : "teal"} style={{ wordBreak: "break-all" }}>
                    {it.message}
                  </Text>
                )}
              </div>
            ))}
          </Stack>
        )}

        {!auto && !blocked && <Switch size="sm" label={t("upload.overwrite")} checked={overwrite} onChange={(e) => setOverwrite(e.currentTarget.checked)} />}

        <Group justify="flex-end">
          <Button variant="default" onClick={close} disabled={busy}>
            {t("upload.close")}
          </Button>
          <Button onClick={run} loading={busy} disabled={pending === 0 || anonymous || blocked}>
            {t("upload.start", { count: pending })}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
