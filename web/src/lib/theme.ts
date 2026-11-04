import { createTheme, rem } from "@mantine/core";

// One theme for the whole UI: every page inherits radius, density, type and
// component defaults from here instead of repeating them at call sites.
export const theme = createTheme({
  primaryColor: "indigo",
  primaryShade: { light: 6, dark: 5 },
  defaultRadius: "md",
  fontFamily:
    'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  fontFamilyMonospace: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
  headings: {
    fontWeight: "650",
    sizes: {
      h3: { fontSize: rem(22), lineHeight: "1.3" },
      h4: { fontSize: rem(18), lineHeight: "1.3" },
    },
  },
  components: {
    Card: { defaultProps: { withBorder: true, radius: "md", shadow: "xs" } },
    Paper: { defaultProps: { withBorder: true, radius: "md" } },
    Button: { defaultProps: { radius: "md" }, styles: { root: { fontWeight: 550 } } },
    ActionIcon: { defaultProps: { variant: "subtle", color: "gray" } },
    Badge: { defaultProps: { variant: "light", radius: "sm" }, styles: { root: { fontWeight: 600 } } },
    TextInput: { defaultProps: { radius: "md" } },
    PasswordInput: { defaultProps: { radius: "md" } },
    Select: { defaultProps: { radius: "md", comboboxProps: { shadow: "md" } } },
    MultiSelect: { defaultProps: { radius: "md", comboboxProps: { shadow: "md" } } },
    Textarea: { defaultProps: { radius: "md" } },
    Modal: {
      defaultProps: { radius: "md", overlayProps: { backgroundOpacity: 0.45, blur: 3 }, shadow: "xl" },
      styles: { title: { fontWeight: 650, fontSize: rem(17) } },
    },
    Alert: { defaultProps: { radius: "md", variant: "light" } },
    Code: { defaultProps: { radius: "sm" } },
    Tooltip: { defaultProps: { radius: "sm", withArrow: true } },
    NavLink: { defaultProps: { variant: "light" }, styles: { root: { borderRadius: rem(8), fontWeight: 500 } } },
  },
});
