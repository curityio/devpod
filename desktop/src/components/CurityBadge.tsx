import { Link, Text, useColorModeValue } from "@chakra-ui/react"
import { client } from "../client"
import { Curity } from "../icons"

export function CurityBadge() {
  // Curity's logo is white on dark backgrounds, never purple or grey
  const logoColor = useColorModeValue("gray.900", "white")

  return (
    <Link
      display="flex"
      alignItems="center"
      justifyContent="start"
      onClick={() => client.open("https://curity.io/")}>
      <Text fontSize="sm" variant="muted" marginRight="2">
        Maintained by
      </Text>
      <Curity width="16" height="4" color={logoColor} />
    </Link>
  )
}
