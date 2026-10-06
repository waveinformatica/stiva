import { useEffect, useState } from "react";
import { Stack, Group, Select, TextInput, Text, Code } from "./ui";
import { api } from "../lib/api";

/**
 * ScopeBuilder composes the three positions of a scope — format, registry,
 * repository pattern — instead of asking for the string.
 *
 * The format comes first because it is what gives the pattern its meaning:
 * "com/waveinformatica/**" is a Maven coordinate and "kosmos/**" is an image
 * namespace, and a grant that did not say which would be guessing.
 */
export function ScopeBuilder({ value, onChange }: { value: string; onChange: (s: string) => void }) {
  const [registries, setRegistries] = useState<{ name: string; format: string }[]>([]);

  useEffect(() => {
    api
      .adminRegistries()
      .then((r: any) => setRegistries(r.registries || []))
      .catch(() => setRegistries([]));
  }, []);

  const parts = (() => {
    if (!value || value === "*") return { format: "*", registry: "*", pattern: "*" };
    const bits = value.split(":");
    return {
      format: bits[0] || "*",
      registry: bits[1] || "*",
      pattern: bits[2] || "*",
    };
  })();

  const emit = (format: string, registry: string, pattern: string) => {
    if (format === "*") {
      onChange("*");
      return;
    }
    if (registry === "*" && pattern === "*") {
      onChange(format);
      return;
    }
    if (pattern === "*") {
      onChange(`${format}:${registry}`);
      return;
    }
    onChange(`${format}:${registry}:${pattern}`);
  };

  const formats = Array.from(new Set(registries.map((r) => r.format))).sort();
  const inFormat = registries.filter((r) => parts.format === "*" || r.format === parts.format);

  return (
    <Stack gap="xs">
      <Group gap="sm" align="flex-end">
        <Select
          label="Format"
          description="* = everywhere"
          data={[{ value: "*", label: "* (every format)" }, ...formats.map((f) => ({ value: f, label: f }))]}
          value={parts.format}
          onChange={(v) => emit(v || "*", "*", "*")}
          allowDeselect={false}
          w={170}
        />
        <Select
          label="Registry"
          data={[
            { value: "*", label: "* (every registry of this format)" },
            ...inFormat.map((r) => ({ value: r.name, label: r.name })),
          ]}
          value={parts.registry}
          onChange={(v) => emit(parts.format, v || "*", parts.pattern)}
          disabled={parts.format === "*"}
          allowDeselect={false}
          style={{ flex: 1 }}
        />
      </Group>
      <TextInput
        label="Repository pattern"
        description="* covers one path segment, ** any number. kosmos/** means the repositories under kosmos/, not one named kosmos."
        placeholder="* (the whole registry)"
        value={parts.pattern === "*" ? "" : parts.pattern}
        onChange={(e) => emit(parts.format, parts.registry, e.currentTarget.value || "*")}
        disabled={parts.format === "*"}
      />
      <Text size="xs" c="dimmed">
        Scope: <Code>{value || "*"}</Code>
      </Text>
    </Stack>
  );
}
