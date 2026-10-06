import { useEffect, useState } from "react";
import {
  PageHeader,
  Card,
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
      setErr("The two new passwords do not match.");
      setBusy(false);
      return;
    }
    try {
      await api.changePassword(cur, next);
      setOk("Password updated.");
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
  if (err && !me) return <Alert color="red" title="Error">{err}</Alert>;

  return (
    <div>
      <PageHeader title="Settings" />
      <Group grow mb="md">
        <StatCard label="Repositories" value={stats?.repositories ?? 0} />
        <StatCard label="Tags" value={stats?.tags ?? 0} />
      </Group>
      <Card withBorder padding="md" mb="md">
        <Text fw={700} mb="xs">
          Current session
        </Text>
        <Stack gap={4}>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              User
            </Text>
            <Text size="sm">{me.user.name}</Text>
          </Group>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              Role
            </Text>
            <Badge color={me.user.admin ? "grape" : "gray"}>
              {me.user.admin ? "administrator" : "user"}
            </Badge>
          </Group>
          <Group justify="space-between">
            <Text size="sm" c="dimmed">
              Groups
            </Text>
            <Text size="sm">{(me.user.groups || []).join(", ") || "—"}</Text>
          </Group>
        </Stack>
      </Card>

      <Card withBorder padding="md" mb="md">
        <Text fw={700} mb="xs">
          Change password
        </Text>
        {me.user.password_change_required && (
          <Alert color="yellow" mb="sm" title="Action required">
            You are still using a temporary password. Change it to continue using the
            registry.
          </Alert>
        )}
        {err && (
          <Alert color="red" mb="sm" title="Error">
            {err}
          </Alert>
        )}
        {ok && (
          <Alert color="teal" mb="sm" title="Done">
            {ok}
          </Alert>
        )}
        <form onSubmit={changePassword}>
          <Stack gap="xs">
            <PasswordInput
              label="Current password"
              value={cur}
              onChange={(e) => setCur(e.currentTarget.value)}
              required
            />
            <PasswordInput
              label="New password"
              value={next}
              onChange={(e) => setNext(e.currentTarget.value)}
              required
            />
            <PasswordInput
              label="Confirm new password"
              value={confirm}
              onChange={(e) => setConfirm(e.currentTarget.value)}
              required
            />
            <Group justify="flex-end">
              <Button type="submit" loading={busy}>
                Update password
              </Button>
            </Group>
          </Stack>
        </form>
      </Card>

      <Card withBorder padding="md" mb="md">
        <Group justify="space-between" mb="xs">
          <Text fw={700}>Anonymous access</Text>
          <Switch
            checked={allowAnon}
            onChange={(e) => toggleAnon(e.currentTarget.checked)}
            disabled={settingsBusy}
            label={allowAnon ? "Allowed" : "Denied"}
          />
        </Group>
        <Text size="sm" c="dimmed">
          When denied (default), every request must be authenticated. When allowed,
          unauthenticated requests are mapped to the <Code>anonymous</Code> subject;
          grant it a role scoped to the registry under{" "}
          <Text span fw={600}>Administration → Roles and Grants</Text> to decide what
          anonymous clients may do.
        </Text>
      </Card>

      <Text size="xs" c="dimmed" mt="md">
        Server configuration (PostgreSQL connection, blob store backend, authentication
        realms) is provided via command-line flags or a JSON config file at startup —
        not through the UI, by design. See the project README for details.
      </Text>
    </div>
  );
}
