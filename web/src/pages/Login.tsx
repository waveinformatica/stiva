import { useEffect, useState } from "react";
import {
  Card,
  TextInput,
  PasswordInput,
  Button,
  Alert,
  Title,
  Group,
  Text,
  Stack,
  Divider,
} from "../components/ui";
import { api, ApiError, setToken } from "../lib/api";
import { IconCloud } from "../components/icons";

type SSOProvider = { id: string; label: string; kind: string };

export default function Login({ onLogin }: { onLogin: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [providers, setProviders] = useState<SSOProvider[]>([]);
  // When the account is flagged password_change_required we force a change
  // before the session can be used.
  const [needChange, setNeedChange] = useState(false);
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");

  // Single-sign-on: list the configured providers, and pick up the session
  // token (or error) the callback hands back through the URL fragment, which
  // browsers never send to any server.
  useEffect(() => {
    api
      .ssoProviders()
      .then((r) => setProviders(r.providers || []))
      .catch(() => setProviders([]));
    const hash = window.location.hash;
    if (hash.includes("sso_token=")) {
      const token = new URLSearchParams(hash.slice(1)).get("sso_token");
      window.location.hash = "";
      if (token) {
        setToken(token);
        onLogin();
        return;
      }
    }
    if (hash.includes("sso_error=")) {
      const msg = new URLSearchParams(hash.slice(1)).get("sso_error");
      window.location.hash = "";
      setErr(msg || "Single sign-on failed.");
    }
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      await api.login(username, password);
      const me = await api.me();
      if (me.user.password_change_required) {
        setNeedChange(true);
      } else {
        onLogin();
      }
    } catch (e) {
      setErr(e instanceof ApiError ? "Invalid username or password." : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const doChange = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    if (newPassword.length < 1) {
      setErr("Choose a new password.");
      setBusy(false);
      return;
    }
    if (newPassword !== confirm) {
      setErr("The two passwords do not match.");
      setBusy(false);
      return;
    }
    try {
      await api.changePassword(password, newPassword);
      // Re-authenticate so the new token no longer carries the change-required flag.
      await api.login(username, newPassword);
      onLogin();
    } catch (e) {
      setErr(e instanceof ApiError ? (e as ApiError).message : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (needChange) {
    return (
      <Stack align="center" justify="center" h="100vh">
        <Card withBorder w={{ base: "calc(100vw - 2rem)", xs: 360 }} padding="xl" radius="md">
          <Group justify="center" mb="md">
            <IconCloud size={28} />
            <Title order={3}>Change password</Title>
          </Group>
          <Alert color="yellow" mb="md" title="Password change required">
            You are using a temporary password. Set a personal password to continue.
          </Alert>
          {err && (
            <Alert color="red" mb="md" title="Error">
              {err}
            </Alert>
          )}
          <form onSubmit={doChange}>
            <Stack>
              <PasswordInput
                label="Current password"
                value={password}
                onChange={(e) => setPassword(e.currentTarget.value)}
                required
              />
              <PasswordInput
                label="New password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.currentTarget.value)}
                required
              />
              <PasswordInput
                label="Confirm new password"
                value={confirm}
                onChange={(e) => setConfirm(e.currentTarget.value)}
                required
              />
              <Button type="submit" fullWidth loading={busy}>
                Set password &amp; continue
              </Button>
            </Stack>
          </form>
        </Card>
      </Stack>
    );
  }

  return (
    <Stack align="center" justify="center" h="100vh">
      <Card withBorder w={{ base: "calc(100vw - 2rem)", xs: 360 }} padding="xl" radius="md">
        <Group justify="center" mb="md">
          <img src="/stiva-logo.png" alt="Stiva" style={{ width: 180, height: "auto" }} />
        </Group>
        <Text size="sm" c="dimmed" mb="md" ta="center">
          Sign in to browse and administer artifacts.
        </Text>
        {err && (
          <Alert color="red" mb="md" title="Login failed">
            {err}
          </Alert>
        )}
        <form onSubmit={submit}>
          <Stack>
            <TextInput
              label="Username"
              value={username}
              onChange={(e) => setUsername(e.currentTarget.value)}
              required
            />
            <PasswordInput
              label="Password"
              value={password}
              onChange={(e) => setPassword(e.currentTarget.value)}
              required
            />
            <Button type="submit" fullWidth loading={busy}>
              Sign in
            </Button>
          </Stack>
        </form>
        {providers.length > 0 && (
          <>
            <Divider label="or continue with" labelPosition="center" my="md" />
            <Stack gap="xs">
              {providers.map((p) => (
                <Button
                  key={p.id}
                  variant="default"
                  fullWidth
                  component="a"
                  href={`/auth/sso/${encodeURIComponent(p.id)}/login`}
                >
                  {p.label}
                </Button>
              ))}
            </Stack>
          </>
        )}
      </Card>
    </Stack>
  );
}
