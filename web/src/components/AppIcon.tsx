import { Avatar } from "@mantine/core";

const PALETTE = ["indigo", "teal", "grape", "cyan", "orange", "pink", "lime", "blue"];

// The app's icon, or a tile with its initial in a colour taken from the name,
// so entries without an icon still look deliberate.
export default function AppIcon({ name, icon, size = 56 }: { name: string; icon?: string; size?: number }) {
  const color = PALETTE[[...name].reduce((n, ch) => n + ch.charCodeAt(0), 0) % PALETTE.length];
  return (
    <Avatar src={icon || undefined} alt={name} size={size} radius={Math.round(size / 4.5)} color={color} variant="light">
      {name.trim().charAt(0).toUpperCase()}
    </Avatar>
  );
}
