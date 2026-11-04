import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  PageHeader,
  Section,
  Group,
  Text,
  Badge,
  Stack,
  StatCard,
  Alert,
  Loader,
  PasswordInput,
  Button,
  Switch,
  Code,
} from "../../components/ui";
import { api, MeResponse } from "../../lib/api";

export default function Settings() {
  const { t } = useTranslation();
  const [me, setMe] = useState<MeResponse | null>(null);
  const [stats, setStats] = useState<any>(null);
  const [err, setErr] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  const [allowAnon, setAllowAnon] = useState(false);
  const [settingsBusy, setSettingsBusy] = useState(false);

  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .me()
      .then(setMe)
      .catch((e) => setErr(e.message));
    api
      .registries()
      .then((r) => api.stats(r.registries[0]?.name || ""))
      .then(setStats)
      .catch((e) => setErr(e.message));
    api
      .getSettings()
      .then((s) => setAllowAnon(s.allow_anonymous))
      .catch((e) => setErr(e.message));
  }, []);

  const toggleAnon = async (value: boolean) => {
    setSettingsBusy(true);
    setErr(null);
    try {
      await api.setSettings(value);
      setAllowAnon(value);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSettingsBusy(false);
    }
  };

  const changePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    setOk(null);
    if (next !== confirm) {
      setErr(t("settings.passwordMismatch"));
      setBusy(false);
      return;
    }
    try {
      await api.changePassword(cur, next);
      setOk(t("settings.passwordUpdated"));
      setCur("");
      setNext("");
      setConfirm("");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (!me) return <Loader />;
  if (err && !me) return <Alert color="red" title={t("common.error")}>{err}</Alert>;

  return (
    <div>
      <PageHeader title={t("settings.title")} />
      <Group grow mb="md">
        <StatCard label={t("settings.repositoriesStat")} value={stats?.repositories ?? 0} />
        <StatCard label={t("settings.tagsStat")} value={stats?.tags ?? 0} />
      </Group>
      <Section title={t("settings.sessionTitle")}>
        <Stack gap={4}>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              {t("settings.userLabel")}
            </Text>
            <Text size="sm">{me.user.name}</Text>
          </Group>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              {t("settings.roleLabel")}
            </Text>
            <Badge color={me.user.admin ? "grape" : "gray"}>
              {me.user.admin ? t("settings.administrator") : t("settings.regularUser")}
            </Badge>
          </Group>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              {t("settings.groupsLabel")}
            </Text>
            <Text size="sm">{(me.user.groups || []).join(", ") || "—"}</Text>
          </Group>
        </Stack>
      </Section>

      <Section title={t("settings.changePasswordTitle")}>
        {me.user.password_change_required && (
          <Alert color="yellow" mb="sm" title={t("settings.actionRequired")}>
            {t("settings.tempPasswordBody")}
          </Alert>
        )}
        {err && (
          <Alert color="red" mb="sm" title={t("common.error")}>
            {err}
          </Alert>
        )}
        {ok && (
          <Alert color="teal" mb="sm" title={t("common.done")}>
            {ok}
          </Alert>
        )}
        <form onSubmit={changePassword}>
          <Stack gap="xs">
            <PasswordInput
              label={t("login.currentPassword")}
              value={cur}
              onChange={(e) => setCur(e.currentTarget.value)}
              required
            />
            <PasswordInput
              label={t("login.newPassword")}
              value={next}
              onChange={(e) => setNext(e.currentTarget.value)}
              required
            />
            <PasswordInput
              label={t("login.confirmNewPassword")}
              value={confirm}
              onChange={(e) => setConfirm(e.currentTarget.value)}
              required
            />
            <Group justify="flex-end">
              <Button type="submit" loading={busy}>
                {t("settings.updatePassword")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Section>

      <Section
        title={t("settings.anonTitle")}
        actions={
          <Switch
            checked={allowAnon}
            onChange={(e) => toggleAnon(e.currentTarget.checked)}
            disabled={settingsBusy}
            label={allowAnon ? t("settings.allowed") : t("settings.denied")}
          />
        }
      >
        <Text size="sm" c="dimmed">
          {t("settings.anonBodyPre")} <Code>anonymous</Code> {t("settings.anonBodyPost")}
        </Text>
      </Section>

      <Text size="xs" c="dimmed" mt="md">
        {t("settings.serverNote")}
      </Text>
    </div>
  );
}
