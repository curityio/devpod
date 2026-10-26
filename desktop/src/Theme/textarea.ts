import { defineStyleConfig } from "@chakra-ui/react"

export const Textarea = defineStyleConfig({
  variants: {
    outline: {
      borderColor: "gray.200",
      _dark: {
        borderColor: "gray.800",
      },
      _focusVisible: {
        borderColor: "focus",
        boxShadow: "0 0 0 1px var(--chakra-colors-focus)",
      },
    },
  },
})
