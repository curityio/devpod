import { Theme, ThemeOverride, Tooltip, defineStyleConfig, extendTheme } from "@chakra-ui/react"
import { mode } from "@chakra-ui/theme-tools"
import { Button } from "./button"
import { Card } from "./card"
import { Checkbox } from "./checkbox"
import { Form } from "./form"
import { Input } from "./input"
import { Menu } from "./menu"
import { Modal } from "./modal"
import { Popover } from "./popover"
import { Radio } from "./radio"
import { Select } from "./select"
import { Switch } from "./switch"
import { Tabs } from "./tabs"
import { Tag } from "./tag"
import { Text } from "./text"
import { Textarea } from "./textarea"

const Code = defineStyleConfig({
  variants: {
    decorative(props) {
      return {
        backgroundColor: "primary.400",
        color: mode("white", "gray.900")(props),
      }
    },
  },
})

const Link = defineStyleConfig({
  defaultProps: {
    variant: "primary",
  },
  variants: {
    muted(props) {
      return { color: mode("gray.600", "gray.400")(props) }
    },
    primary(props) {
      const primary = props.theme.colors.primary

      return { color: mode(primary["600"], primary["300"])(props) }
    },
  },
})

const FormError = defineStyleConfig({
  baseStyle: {
    text: {
      userSelect: "text",
      cursor: "text",
    },
  },
})

const TooltipTheme = defineStyleConfig({
  baseStyle(props) {
    return {
      bg: mode("gray.800", "gray.200")(props),
      color: mode("gray.100", "gray.800")(props),
    }
  },
})

// It's ugly but it works: https://github.com/chakra-ui/chakra-ui/issues/1424#issuecomment-743342944
// Unfortunately there is no other way of overring the default placement.
Tooltip.defaultProps = { ...Tooltip.defaultProps, placement: "top" }

export const theme = extendTheme({
  styles: {
    global({ colorMode }) {
      return {
        html: {
          fontSize: "14px",
          overflow: "hidden",
          background: "transparent",
          position: "fixed",
        },
        body: {
          background: "transparent",
          userSelect: "none",
          cursor: "default",
        },
        td: {
          userSelect: "text",
        },
        code: {
          userSelect: "text",
          cursor: "text",
        },
        "input::placeholder": {
          color: colorMode === "light" ? "gray.500" : "gray.400",
        },
      }
    },
  },
  fonts: {
    heading: `"Google Sans", "Helvetica Neue", Arial, sans-serif`,
    body: `"Google Sans", "Helvetica Neue", Arial, sans-serif`,
  },
  shadows: {
    outline: "0 0 0 2px var(--chakra-colors-focus)",
  },
  semanticTokens: {
    colors: {
      // Lilac is too light for a focus ring or small text on the light background
      focus: { default: "primary.600", _dark: "primary.300" },
      "text.tertiary": { default: "gray.600", _dark: "gray.400" },
    },
  },
  // Curity 2026 palette: purple #7B4FD4 (500) for fills, lilac #C9A8FF (300) as the highlight,
  // and greys tinted towards Curity's navy, ending in midnight #130E2B
  colors: {
    primary: {
      50: "#F5F0FF",
      100: "#EBE0FF",
      200: "#D9C5FF",
      300: "#C9A8FF",
      400: "#A27FEA",
      500: "#7B4FD4",
      600: "#6A40C0",
      700: "#5732A3",
      800: "#40257D",
      900: "#2D1B69",
    },
    gray: {
      50: "#F6F5FA",
      100: "#ECEAF4",
      200: "#DAD7E8",
      300: "#BFBBD3",
      400: "#A39EBB",
      500: "#837D9E",
      600: "#645E82",
      700: "#3B3462",
      800: "#251E48",
      900: "#130E2B",
    },
    text: {
      secondary: "#645E82",
    },
    divider: {
      main: "#BFBBD3",
      light: "#DAD7E8",
      dark: "#645E82",
    },
    background: {
      darkest: "#0D0B1A",
    },
  },
  config: {
    initialColorMode: "system",
    useSystemColorMode: true,
  },
  components: {
    Button,
    Card,
    Code,
    Menu,
    Switch,
    Tabs,
    Checkbox,
    Radio,
    Link,
    Form,
    FormError,
    Popover,
    Modal,
    Tag,
    Input,
    Select,
    Text,
    Textarea,
    Tooltip: TooltipTheme,
  },
} satisfies ThemeOverride) as Theme
