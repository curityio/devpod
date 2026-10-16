import { Link, Text } from "@chakra-ui/react"
import { client } from "../client"
import { Curity } from "../icons"

export function CurityBadge() {
  return (
    <Link
      display="flex"
      alignItems="center"
      justifyContent="start"
      onClick={() => client.open("https://curity.io/")}>
      <Text fontSize="sm" variant="muted" marginRight="2">
        Maintained by
      </Text>
      <Curity width="16" height="4" />
    </Link>
  )
}
