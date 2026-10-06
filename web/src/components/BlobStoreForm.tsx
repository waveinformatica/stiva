import { useState } from "react";
import { Stack, Group, Select, TextInput, Switch, Button, Alert, Text } from "./ui";
import { SecretField } from "./SecretField";
import { api } from "../lib/api";

export type BlobStore = {
  name: string;
  kind: string;
  description?: string;
  file?: { root: string };
  s3?: {
    bucket: string;
    region?: string;
    endpoint?: string;
    prefix?: string;
    force_path_style?: boolean;
    access_key_id?: string;
    secret_key?: string;
  };
  gcs?: { bucket: string; prefix?: string; credentials_file?: string };
  azure?: { container: string; prefix?: string; connection_string?: string };
};

export function emptyStore(kind = "s3"): BlobStore {
  return withKind({ name: "", kind, description: "" }, kind);
}

/** withKind keeps the definition a proper union: only the block matching the
 *  selected kind is present, which is what the server validates on save. */
function withKind(s: BlobStore, kind: string): BlobStore {
  const base: BlobStore = { name: s.name, kind, description: s.description };
  switch (kind) {
    case "file":
      base.file = s.file || { root: "/data/blobs" };
      break;
    case "s3":
      base.s3 = s.s3 || { bucket: "", region: "us-east-1", force_path_style: true, secret_key: "" };
      break;
    case "gcs":
      base.gcs = s.gcs || { bucket: "" };
      break;
    case "azure":
      base.azure = s.azure || { container: "", connection_string: "" };
      break;
  }
  return base;
}

/**
 * BlobStoreForm is the single definition form for a storage backend. It is used
 * both on the Blob stores page and inline while creating a registry, so the two
 * paths cannot drift into asking for different things.
 */
export function BlobStoreForm({
  value,
  onChange,
  onSaved,
  nameLocked,
  submitLabel = "Save store",
}: {
  value: BlobStore;
  onChange: (s: BlobStore) => void;
  onSaved?: (s: BlobStore) => void;
  nameLocked?: boolean;
  submitLabel?: string;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const s = value;

  const set = (patch: Partial<BlobStore>) => onChange({ ...s, ...patch });
  const setS3 = (patch: Partial<NonNullable<BlobStore["s3"]>>) =>
    onChange({ ...s, s3: { ...(s.s3 || { bucket: "" }), ...patch } });

  const save = async () => {
    setErr(null);
    if (!s.name.trim()) {
      setErr("The store needs a name.");
      return;
    }
    setBusy(true);
    try {
      const saved = await api.upsertBlobStore(s, nameLocked ? s.name : undefined);
      onSaved?.(saved);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Stack gap="sm">
      {err && (
        <Alert color="red" title="Could not save">
          {err}
        </Alert>
      )}

      <Group gap="sm" align="flex-start">
        <TextInput
          label="Name"
          placeholder="minio-main"
          value={s.name}
          disabled={nameLocked}
          onChange={(e) => set({ name: e.currentTarget.value })}
          style={{ flex: 1 }}
        />
        <Select
          label="Type"
          data={["file", "s3", "gcs", "azure"]}
          value={s.kind}
          onChange={(v) => onChange(withKind(s, v || "s3"))}
          allowDeselect={false}
          w={140}
        />
      </Group>

      <TextInput
        label="Description"
        placeholder="What this backend is, in a few words"
        value={s.description || ""}
        onChange={(e) => set({ description: e.currentTarget.value })}
      />

      <Text size="xs" c="dimmed">
        Several registries can share one store. Each is given its own namespace inside it
        automatically, so they never overwrite or delete each other's data — there is no path to
        set here.
      </Text>

      {s.kind === "file" && (
        <>
          <TextInput
            label="Root path"
            value={s.file?.root || ""}
            onChange={(e) => onChange({ ...s, file: { root: e.currentTarget.value } })}
          />
          <Text size="xs" c="dimmed">
            A filesystem store lives on one node. Every registry using it is pinned there, and
            follows that node down.
          </Text>
        </>
      )}

      {s.kind === "s3" && (
        <>
          <Group gap="sm">
            <TextInput
              label="Bucket"
              value={s.s3?.bucket || ""}
              onChange={(e) => setS3({ bucket: e.currentTarget.value })}
              style={{ flex: 1 }}
            />
            <TextInput
              label="Region"
              value={s.s3?.region || ""}
              onChange={(e) => setS3({ region: e.currentTarget.value })}
              w={160}
            />
          </Group>
          <Group gap="sm">
            <TextInput
              label="Endpoint"
              placeholder="http://minio.storage.svc.cluster.local — leave empty for AWS"
              value={s.s3?.endpoint || ""}
              onChange={(e) => setS3({ endpoint: e.currentTarget.value })}
              style={{ flex: 1 }}
            />
          </Group>
          <Switch
            label="Path-style addressing"
            checked={!!s.s3?.force_path_style}
            onChange={(e) => setS3({ force_path_style: e.currentTarget.checked })}
          />
          <SecretField
            label="Secret access key"
            description="Leave the credential unset to use the ambient AWS credential chain instead."
            value={s.s3?.secret_key || ""}
            onChange={(ref) => setS3({ secret_key: ref })}
            suggestedName={s.name ? `${s.name}-secret-key` : ""}
            publicFields={[
              {
                key: "access_key_id",
                label: "Access key ID",
                value: s.s3?.access_key_id || "",
                onChange: (v) => setS3({ access_key_id: v }),
              },
            ]}
          />
        </>
      )}

      {s.kind === "gcs" && (
        <Group gap="sm">
          <TextInput
            label="Bucket"
            value={s.gcs?.bucket || ""}
            onChange={(e) => onChange({ ...s, gcs: { ...(s.gcs || { bucket: "" }), bucket: e.currentTarget.value } })}
            style={{ flex: 1 }}
          />
          <TextInput
            label="Credentials file"
            placeholder="empty = application default credentials"
            value={s.gcs?.credentials_file || ""}
            onChange={(e) =>
              onChange({ ...s, gcs: { ...(s.gcs || { bucket: "" }), credentials_file: e.currentTarget.value } })
            }
            style={{ flex: 1 }}
          />
        </Group>
      )}

      {s.kind === "azure" && (
        <>
          <TextInput
            label="Container"
            value={s.azure?.container || ""}
            onChange={(e) =>
              onChange({ ...s, azure: { ...(s.azure || { container: "" }), container: e.currentTarget.value } })
            }
          />
          <SecretField
            label="Connection string"
            description="Carries the account key, so it is always stored as a credential."
            value={s.azure?.connection_string || ""}
            onChange={(ref) =>
              onChange({ ...s, azure: { ...(s.azure || { container: "" }), connection_string: ref } })
            }
            suggestedName={s.name ? `${s.name}-connection` : ""}
          />
        </>
      )}

      <Group justify="flex-end" mt="xs">
        <Button loading={busy} onClick={save}>
          {submitLabel}
        </Button>
      </Group>
    </Stack>
  );
}
