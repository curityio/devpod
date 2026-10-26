import { cardAnatomy } from "@chakra-ui/anatomy"
import { createMultiStyleConfigHelpers } from "@chakra-ui/react"
import { mode } from "@chakra-ui/theme-tools"

const { definePartsStyle, defineMultiStyleConfig } = createMultiStyleConfigHelpers(cardAnatomy.keys)
export const Card = defineMultiStyleConfig({
  baseStyle: definePartsStyle((props) => {
    return {
      container: {
        backgroundColor: "gray.50",
        borderColor: mode("gray.200", "gray.700")(props),
        _dark: {
          background: "linear-gradient(180deg, rgba(45, 27, 105, 0.45), rgba(19, 14, 43, 0.55))",
          borderColor: "rgba(201, 168, 255, 0.22)",
        },
      },
    }
  }),
})
