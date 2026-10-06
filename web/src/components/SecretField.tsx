import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Stack, Text, Group, Button, Select, PasswordInput, TextInput, Alert } from "./ui";
import { api } from "../lib/api";

export type SecretEntry = {
  key: string;
  description: string;
  public: Record<string, unknown>;
};

/**
 * SecretField picks the credential a configuration field points at.
 *
 * The stored value is always a `vault://<name>` reference, never the credential
 * itself. In "new" mode the field creates the vault entry first and then hands
 * back the reference, so a plaintext value never reaches the definition being
 * edited — the server rejects one anyway, and this keeps the UI honest about it.
 *
 * `publicFields` are the non-secret half of a credential, stored beside the
 * encrypted half so one entry describes one credential: an S3 access key id
 * next to its secret key, an LDAP bind DN next to its password.
 */
export function SecretField({
  label,
  description,
  value,
  onChange,
  publicFields,
  suggestedName,
}: {
  label: string;
  description?: string;
  value: string;
  onChange: (ref: string) => void;
  publicFields?: { key: string; label: string; value: string; onChange: (v: string) => void }[];
  suggestedName?: string;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"existing" | "new">("existing");
  const [secrets, setSecrets] = useState<SecretEntry[]>([]);
  const [name, setName] = useState(suggestedName || "");
  const [plain, setPlain] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = () =>
    api
      .secrets()
      .then((r: any) => setSecrets(r.secrets || []))
      .catch((e) => setErr(e.message));

  useEffect(() => {
    load();
  }, []);

  useEffect(() => {
    if (suggestedName && !name) setName(suggestedName);
  }, [suggestedName]);

  const create = async () => {
    setErr(null);
    if (!name.trim() || !plain) {
      setErr(t("secrets.nameValueRequired"));
      return;
    }
    setBusy(true);
    try {
      const pub: Record<string, unknown> = {};
      (publicFields || []).forEach((f) => {
        if (f.value) pub[f.key] = f.value;
      });
      const r = await api.createSecret({
        key: name.trim(),
        value: plain,
        description: description || "",
        public: pub,
      });
      onChange(r.ref);
      setPlain("");
      setMode("existing");
      await load();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  const selected = value.startsWith("vault://") ? value.slice("vault://".length) : "";

  return (
    <Stack gap="xs">
      <div>
        <Text size="sm" fw={500}>
          {label}
        </Text>
        {description && (
          <Text size="xs" c="dimmed">
            {description}
          </Text>
        )}
      </div>

      {publicFields?.map((f) => (
        <TextInput
          key={f.key}
          label={f.label}
          value={f.value}
          onChange={(e) => f.onChange(e.currentTarget.value)}
        />
      ))}

      <Group gap="xs">
        <Button
          size="xs"
          variant={mode === "existing" ? "filled" : "default"}
          onClick={() => setMode("existing")}
        >
          {t("secrets.useExisting")}
        </Button>
        <Button size="xs" variant={mode === "new" ? "filled" : "default"} onClick={() => setMode("new")}>
          {t("secrets.createNew")}
        </Button>
      </Group>

      {err && (
        <Alert color="red" title={t("secrets.credentialTitle")}>
          {err}
        </Alert>
      )}

      {mode === "existing" ? (
        <Select
          placeholder={secrets.length ? t("secrets.selectPlaceholder") : t("secrets.emptyPlaceholder")}
          data={secrets.map((s) => ({
            value: s.key,
            label: s.description ? `${s.key} — ${s.description}` : s.key,
          }))}
          value={selected || null}
          onChange={(v) => onChange(v ? `vault://${v}` : "")}
          searchable
          clearable
          nothingFoundMessage={t("secrets.nothingFound")}
        />
      ) : (
        <Stack gap="xs">
          <TextInput
            label={t("secrets.nameLabel")}
            placeholder={t("secrets.namePlaceholder")}
            description={t("secrets.nameDesc")}
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
          />
          <PasswordInput
            label={t("secrets.valueLabel")}
            description={t("secrets.valueDesc")}
            value={plain}
            onChange={(e) => setPlain(e.currentTarget.value)}
          />
          <Group justify="flex-end">
            <Button size="xs" loading={busy} onClick={create}>
              {t("secrets.saveCredential")}
            </Button>
          </Group>
        </Stack>
      )}

      {selected && (
        <Text size="xs" c="dimmed">
          {t("secrets.using")} <strong>{selected}</strong>
        </Text>
      )}
    </Stack>
  );
}
