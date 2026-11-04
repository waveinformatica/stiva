import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Badge, Button, Card, Chip, Group, SimpleGrid, Stack, Text, TextInput, Title } from "@mantine/core";
import { api } from "../lib/api";
import { EmptyState, ErrorAlert, PageLoader } from "../components/ui";
import { IconSearch } from "../components/icons";
import AppIcon from "../components/AppIcon";

type AppCard = {
  slug: string;
  name: string;
  tagline: string;
  category: string;
  icon: string;
  featured: boolean;
  formats: string[];
  artifacts: number;
};

function AppTile({ app, large }: { app: AppCard; large?: boolean }) {
  return (
    <Card
      component={Link}
      to={`/catalog/${app.slug}`}
      padding="lg"
      style={{ textDecoration: "none", color: "inherit", height: "100%", transition: "transform 120ms, box-shadow 120ms" }}
      className="stiva-tile"
    >
      <Group wrap="nowrap" align="flex-start" gap="md">
        <AppIcon name={app.name} icon={app.icon} size={large ? 72 : 56} />
        <Stack gap={4} style={{ minWidth: 0, flex: 1 }}>
          <Text fw={650} fz={large ? "lg" : "md"} truncate>
            {app.name}
          </Text>
          <Text size="sm" c="dimmed" lineClamp={large ? 3 : 2}>
            {app.tagline}
          </Text>
        </Stack>
      </Group>
      <Group gap={6} mt="md">
        {app.category && (
          <Badge variant="light" color="indigo">
            {app.category}
          </Badge>
        )}
        {app.formats.map((f) => (
          <Badge key={f} variant="outline" color="gray">
            {f}
          </Badge>
        ))}
      </Group>
    </Card>
  );
}

// The home page: an app-store style catalog. What is listed depends on the
// caller, so a guest sees the applications reachable without signing in.
export default function Catalog({ anonymous, onSignIn }: { anonymous: boolean; onSignIn: () => void }) {
  const { t } = useTranslation();
  const [apps, setApps] = useState<AppCard[]>([]);
  const [categories, setCategories] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [cat, setCat] = useState<string>("");

  useEffect(() => {
    api
      .catalog()
      .then((r: any) => {
        setApps(r.apps || []);
        setCategories(r.categories || []);
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  }, []);

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return apps.filter(
      (a) =>
        (!cat || a.category === cat) &&
        (!needle || [a.name, a.tagline, a.category].some((s) => s.toLowerCase().includes(needle))),
    );
  }, [apps, q, cat]);

  if (loading) return <PageLoader />;

  const browsing = !q.trim() && !cat;
  const featured = browsing ? filtered.filter((a) => a.featured) : [];
  const rest = browsing ? filtered.filter((a) => !a.featured) : filtered;

  return (
    <div>
      <Stack gap={4} mb="lg">
        <Title order={2}>{t("catalog.title")}</Title>
        <Text c="dimmed">{t("catalog.subtitle")}</Text>
      </Stack>

      {err && <ErrorAlert onClose={() => setErr(null)}>{err}</ErrorAlert>}

      <TextInput
        size="md"
        leftSection={<IconSearch size={16} />}
        placeholder={t("catalog.searchPlaceholder")}
        value={q}
        onChange={(e) => setQ(e.currentTarget.value)}
        mb="sm"
      />
      {categories.length > 0 && (
        <Chip.Group multiple={false} value={cat} onChange={(v) => setCat((v as string) || "")}>
          <Group gap="xs" mb="lg">
            <Chip value="" checked={cat === ""} onClick={() => setCat("")} variant="light" size="sm">
              {t("catalog.all")}
            </Chip>
            {categories.map((c) => (
              <Chip key={c} value={c} variant="light" size="sm">
                {c}
              </Chip>
            ))}
          </Group>
        </Chip.Group>
      )}

      {apps.length === 0 ? (
        <Card padding="lg">
          <EmptyState message={t("catalog.empty")} />
        </Card>
      ) : filtered.length === 0 ? (
        <EmptyState message={t("catalog.emptyFiltered")} />
      ) : (
        <Stack gap="xl">
          {featured.length > 0 && (
            <section>
              <Title order={4} mb="sm">
                {t("catalog.featured")}
              </Title>
              <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md">
                {featured.map((a) => (
                  <AppTile key={a.slug} app={a} large />
                ))}
              </SimpleGrid>
            </section>
          )}
          {rest.length > 0 && (
            <section>
              {featured.length > 0 && (
                <Title order={4} mb="sm">
                  {t("catalog.allApps")}
                </Title>
              )}
              <SimpleGrid cols={{ base: 1, sm: 2, lg: 3, xl: 4 }} spacing="md">
                {rest.map((a) => (
                  <AppTile key={a.slug} app={a} />
                ))}
              </SimpleGrid>
            </section>
          )}
        </Stack>
      )}

      {anonymous && (
        <Group justify="center" mt="xl" gap="xs">
          <Text size="sm" c="dimmed">
            {t("catalog.signInHint")}
          </Text>
          <Button size="compact-sm" variant="light" onClick={onSignIn}>
            {t("nav.signIn")}
          </Button>
        </Group>
      )}
    </div>
  );
}
