import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation();
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
      setErr(msg || t("login.ssoFailed"));
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
      setErr(e instanceof ApiError ? t("login.invalidCredentials") : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const doChange = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    if (newPassword.length < 1) {
      setErr(t("login.chooseNewPassword"));
      setBusy(false);
      return;
    }
    if (newPassword !== confirm) {
      setErr(t("login.passwordsDoNotMatch"));
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
            <Title order={3}>{t("login.changePassword")}</Title>
          </Group>
          <Alert color="yellow" mb="md" title={t("login.changeRequiredTitle")}>
            {t("login.changeRequiredBody")}
          </Alert>
          {err && (
            <Alert color="red" mb="md" title={t("common.error")}>
              {err}
            </Alert>
          )}
          <form onSubmit={doChange}>
            <Stack>
              <PasswordInput
                label={t("login.currentPassword")}
                value={password}
                onChange={(e) => setPassword(e.currentTarget.value)}
                required
              />
              <PasswordInput
                label={t("login.newPassword")}
                value={newPassword}
                onChange={(e) => setNewPassword(e.currentTarget.value)}
                required
              />
              <PasswordInput
                label={t("login.confirmNewPassword")}
                value={confirm}
                onChange={(e) => setConfirm(e.currentTarget.value)}
                required
              />
              <Button type="submit" fullWidth loading={busy}>
                {t("login.setPasswordContinue")}
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
          {t("login.subtitle")}
        </Text>
        {err && (
          <Alert color="red" mb="md" title={t("login.loginFailed")}>
            {err}
          </Alert>
        )}
        <form onSubmit={submit}>
          <Stack>
            <TextInput
              label={t("login.username")}
              value={username}
              onChange={(e) => setUsername(e.currentTarget.value)}
              required
            />
              <PasswordInput
                label={t("login.password")}
                value={password}
              onChange={(e) => setPassword(e.currentTarget.value)}
              required
            />
            <Button type="submit" fullWidth loading={busy}>
              {t("login.signIn")}
            </Button>
          </Stack>
        </form>
        {providers.length > 0 && (
          <>
            <Divider label={t("login.orContinueWith")} labelPosition="center" my="md" />
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
