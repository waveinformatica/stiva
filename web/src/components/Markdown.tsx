import { Fragment, type ReactNode } from "react";
import { Anchor, Code, List, Stack, Text, Title } from "@mantine/core";

// A small, safe Markdown subset for application descriptions: paragraphs,
// # / ## / ### headings, "- " lists, **bold**, *italic*, `code` and
// [text](https://link). Everything becomes React elements, never HTML, so a
// description cannot inject markup; only http(s) links are made clickable.

function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /(\*\*[^*]+\*\*|\*[^*]+\*|`[^`]+`|\[[^\]]+\]\(https?:\/\/[^\s)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const tok = m[0];
    const key = i++;
    if (tok.startsWith("**")) out.push(<strong key={key}>{tok.slice(2, -2)}</strong>);
    else if (tok.startsWith("`")) out.push(<Code key={key}>{tok.slice(1, -1)}</Code>);
    else if (tok.startsWith("[")) {
      const [, label, href] = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(tok)!;
      out.push(
        <Anchor key={key} href={href} target="_blank" rel="noopener noreferrer">
          {label}
        </Anchor>,
      );
    } else out.push(<em key={key}>{tok.slice(1, -1)}</em>);
    last = m.index + tok.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

export default function Markdown({ source }: { source: string }) {
  const lines = source.replace(/\r\n/g, "\n").split("\n");
  const blocks: ReactNode[] = [];
  let para: string[] = [];
  let items: string[] = [];

  const flushPara = () => {
    if (para.length) {
      blocks.push(
        <Text key={blocks.length} size="sm" style={{ lineHeight: 1.65 }}>
          {inline(para.join(" "))}
        </Text>,
      );
      para = [];
    }
  };
  const flushList = () => {
    if (items.length) {
      blocks.push(
        <List key={blocks.length} size="sm" spacing={4}>
          {items.map((it, i) => (
            <List.Item key={i}>{inline(it)}</List.Item>
          ))}
        </List>,
      );
      items = [];
    }
  };

  for (const raw of lines) {
    const line = raw.trimEnd();
    const h = /^(#{1,3})\s+(.*)$/.exec(line);
    const li = /^\s*[-*]\s+(.*)$/.exec(line);
    if (h) {
      flushPara();
      flushList();
      blocks.push(
        <Title key={blocks.length} order={(h[1].length + 2) as 3 | 4 | 5} mt="xs">
          {inline(h[2])}
        </Title>,
      );
    } else if (li) {
      flushPara();
      items.push(li[1]);
    } else if (line.trim() === "") {
      flushPara();
      flushList();
    } else {
      flushList();
      para.push(line.trim());
    }
  }
  flushPara();
  flushList();
  return (
    <Stack gap="sm">
      {blocks.map((b, i) => (
        <Fragment key={i}>{b}</Fragment>
      ))}
    </Stack>
  );
}
