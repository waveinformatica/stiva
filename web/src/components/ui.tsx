import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  Card,
  Group,
  Text,
  Title,
  Stack,
  Table,
  Badge,
  Loader,
  Center,
  Alert,
  Button,
  Modal as MantineModal,
  type ModalProps,
  TextInput,
  PasswordInput,
  Select,
  Switch,
  Textarea,
  Code,
  ActionIcon,
  ThemeIcon,
  ScrollArea,
  Checkbox,
  MultiSelect,
  Tooltip,
  Divider,
  Tree,
  CopyButton,
} from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";

// Re-export the core Mantine primitives so the whole UI imports from one place.
export {
  Card,
  Group,
  Text,
  Title,
  Stack,
  Table,
  Badge,
  Loader,
  Alert,
  Button,
  TextInput,
  PasswordInput,
  Select,
  Switch,
  Textarea,
  Code,
  ActionIcon,
  ThemeIcon,
  ScrollArea,
  Checkbox,
  MultiSelect,
  Tooltip,
  Divider,
  Tree,
  CopyButton,
};

// Modal goes fullscreen on narrow screens so form dialogs stay usable on
// phones without touching every call site.
export function Modal(props: ModalProps) {
  const narrow = useMediaQuery("(max-width: 48em)");
  return <MantineModal fullScreen={narrow} {...props} />;
}

export function PageHeader({ title, actions }: { title: string; actions?: ReactNode }) {
  return (
    <Group justify="space-between" mb="md">
      <Title order={3}>{title}</Title>
      {actions}
    </Group>
  );
}

export function EmptyState({ message }: { message: string }) {
  return (
    <Center maw={480} mx="auto" my="xl">
      <Text c="dimmed">{message}</Text>
    </Center>
  );
}

export function StatCard({ label, value }: { label: string; value: ReactNode }) {
  return (
    <Card withBorder padding="md" radius="md">
      <Text size="xs" c="dimmed" tt="uppercase" fw={700}>
        {label}
      </Text>
      <Text fw={700} fz="xl">
        {value}
      </Text>
    </Card>
  );
}

export interface Column<T> {
  header: string;
  render: (row: T) => ReactNode;
  width?: number | string;
}

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  empty,
}: {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  empty?: string;
}) {
  const { t } = useTranslation();
  if (rows.length === 0) {
    return <EmptyState message={empty || t("common.noData")} />;
  }
  return (
    <ScrollArea>
      <Table striped highlightOnHover withTableBorder verticalSpacing="sm">
        <Table.Thead>
          <Table.Tr>
            {columns.map((c, i) => (
              <Table.Th key={i} style={{ width: c.width }}>
                {c.header}
              </Table.Th>
            ))}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {rows.map((row) => (
            <Table.Tr key={rowKey(row)}>
              {columns.map((c, i) => (
                <Table.Td key={i}>{c.render(row)}</Table.Td>
              ))}
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </ScrollArea>
  );
}

export function ConfirmModal({
  opened,
  title,
  message,
  onConfirm,
  onCancel,
  danger,
}: {
  opened: boolean;
  title: string;
  message: string;
  onConfirm: () => void;
  onCancel: () => void;
  danger?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <Modal opened={opened} onClose={onCancel} title={title} centered>
      <Stack>
        <Text size="sm">{message}</Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button color={danger ? "red" : "blue"} onClick={onConfirm}>
            {t("common.confirm")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
