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
  NumberInput,
} from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { IconInbox, IconPlus, IconTrash } from "./icons";

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
  NumberInput,
};

// Modal goes fullscreen on narrow screens so form dialogs stay usable on
// phones without touching every call site.
export function Modal(props: ModalProps) {
  const narrow = useMediaQuery("(max-width: 48em)");
  return <MantineModal fullScreen={narrow} {...props} />;
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <Group justify="space-between" align="flex-start" mb="lg" gap="md">
      <div>
        <Title order={3}>{title}</Title>
        {description && (
          <Text size="sm" c="dimmed" mt={2}>
            {description}
          </Text>
        )}
      </div>
      {actions && <Group gap="xs">{actions}</Group>}
    </Group>
  );
}

// Titled card used for grouped content (settings blocks, detail panels).
export function Section({
  title,
  description,
  actions,
  children,
}: {
  title?: string;
  description?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card padding="lg" mb="md">
      {(title || actions) && (
        <Group justify="space-between" align="flex-start" mb="md">
          <div>
            {title && (
              <Text fw={650} fz="md">
                {title}
              </Text>
            )}
            {description && (
              <Text size="sm" c="dimmed">
                {description}
              </Text>
            )}
          </div>
          {actions}
        </Group>
      )}
      {children}
    </Card>
  );
}

export function EmptyState({ message }: { message: string }) {
  return (
    <Stack align="center" gap="xs" py="xl" mx="auto" maw={480}>
      <ThemeIcon variant="light" color="gray" size={48} radius="xl">
        <IconInbox size={24} />
      </ThemeIcon>
      <Text c="dimmed" ta="center" size="sm">
        {message}
      </Text>
    </Stack>
  );
}

export function StatCard({ label, value }: { label: string; value: ReactNode }) {
  return (
    <Card padding="lg">
      <Text size="xs" c="dimmed" tt="uppercase" fw={700} lts={0.5}>
        {label}
      </Text>
      <Text fw={700} fz={28} lh={1.2} mt={4}>
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
    return (
      <Card padding="sm">
        <EmptyState message={empty || t("common.noData")} />
      </Card>
    );
  }
  return (
    <Card padding={0} style={{ overflow: "hidden" }}>
      <ScrollArea>
        <Table highlightOnHover verticalSpacing="sm" horizontalSpacing="md" className="stiva-table">
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
    </Card>
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

// ---- Shared building blocks: every page uses these instead of ad hoc markup ----

export function PageLoader() {
  return (
    <Stack align="center" py="xl">
      <Loader size="sm" />
    </Stack>
  );
}

// Standard error banner (dismissible when onClose is given).
export function ErrorAlert({
  children,
  title,
  onClose,
  mb = "md",
}: {
  children: ReactNode;
  title?: string;
  onClose?: () => void;
  mb?: string;
}) {
  const { t } = useTranslation();
  return (
    <Alert color="red" title={title ?? t("common.error")} mb={mb} withCloseButton={!!onClose} onClose={onClose}>
      {children}
    </Alert>
  );
}

// Primary "create" action used in page headers.
export function AddButton({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return (
    <Button leftSection={<IconPlus size={16} />} onClick={onClick}>
      {children}
    </Button>
  );
}

// Destructive row action.
export function DeleteButton({
  onClick,
  label,
  disabled,
}: {
  onClick: () => void;
  label?: string;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const text = label ?? t("common.delete");
  return (
    <Tooltip label={text}>
      <ActionIcon color="red" variant="subtle" disabled={disabled} onClick={onClick} aria-label={text}>
        <IconTrash size={16} />
      </ActionIcon>
    </Tooltip>
  );
}

// Standard modal footer: cancel on the left of the primary action.
export function FormFooter({
  onCancel,
  onSubmit,
  loading,
  disabled,
  leftSection,
  children,
}: {
  onCancel: () => void;
  leftSection?: ReactNode;
  onSubmit: () => void;
  loading?: boolean;
  disabled?: boolean;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <Group justify="flex-end" mt="xs">
      <Button variant="default" onClick={onCancel}>
        {t("common.cancel")}
      </Button>
      <Button leftSection={leftSection} loading={loading} disabled={disabled} onClick={onSubmit}>
        {children}
      </Button>
    </Group>
  );
}
