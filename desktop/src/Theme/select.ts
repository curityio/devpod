import { selectAnatomy } from "@chakra-ui/anatomy"
import { createMultiStyleConfigHelpers } from "@chakra-ui/react"

const { definePartsStyle, defineMultiStyleConfig } = createMultiStyleConfigHelpers(
  selectAnatomy.keys
)
export const Select = defineMultiStyleConfig({
  baseStyle: definePartsStyle({
    addon: {
      borderColor: "gray.200",
      _dark: {
        borderColor: "gray.700",
      },
    },
    field: {
      borderColor: "gray.200",
      _dark: {
        borderColor: "gray.700",
      },
    },
  }),
  variants: {
    outline: definePartsStyle(() => {
      return {
        addon: {
          border: "",
          borderWidth: "thin",
          borderStyle: "solid",
          borderColor: "gray.200",
          _dark: {
            borderColor: "gray.700",
          },
        },
        field: {
          border: "",
          borderWidth: "thin",
          borderStyle: "solid",
          borderColor: "gray.200",
          _dark: {
            borderColor: "gray.700",
          },
          _focusVisible: {
            borderColor: "focus",
            boxShadow: "0 0 0 1px var(--chakra-colors-focus)",
          },
        },
      }
    }),
  },
})
