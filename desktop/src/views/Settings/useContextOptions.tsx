import { Switch } from "@chakra-ui/react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useMemo } from "react"
import { client } from "../../client"
import { QueryKeys } from "../../queryKeys"
import { TContextOptionName } from "../../types"

export function useContextOptions() {
  const queryClient = useQueryClient()
  const { data: options } = useQuery({
    queryKey: QueryKeys.CONTEXT_OPTIONS,
    queryFn: async () => (await client.context.listOptions()).unwrap(),
  })
  const { mutate: updateOption } = useMutation({
    mutationFn: async ({ option, value }: { option: TContextOptionName; value: string }) => {
      ;(await client.context.setOption(option, value)).unwrap()
    },
    onSettled: () => {
      queryClient.invalidateQueries(QueryKeys.CONTEXT_OPTIONS)
    },
  })

  return useMemo(
    () => ({
      options,
      updateOption,
    }),
    [options, updateOption]
  )
}

export function useDockerCredentialsForwardingOption() {
  const { options, updateOption } = useContextOptions()

  const input = useMemo(
    () => (
      <Switch
        isChecked={options?.SSH_INJECT_DOCKER_CREDENTIALS.value === "true"}
        onChange={(e) =>
          updateOption({
            option: "SSH_INJECT_DOCKER_CREDENTIALS",
            value: e.target.checked.toString(),
          })
        }
      />
    ),
    [options?.SSH_INJECT_DOCKER_CREDENTIALS.value, updateOption]
  )

  const helpText = useMemo(
    () => <>Enable to forward your local docker credentials to workspaces</>,
    []
  )

  return { input, helpText }
}

export function useGitCredentialsForwardingOption() {
  const { options, updateOption } = useContextOptions()

  const input = useMemo(
    () => (
      <Switch
        isChecked={options?.SSH_INJECT_GIT_CREDENTIALS.value === "true"}
        onChange={(e) =>
          updateOption({
            option: "SSH_INJECT_GIT_CREDENTIALS",
            value: e.target.checked.toString(),
          })
        }
      />
    ),
    [options?.SSH_INJECT_GIT_CREDENTIALS.value, updateOption]
  )

  const helpText = useMemo(
    () => <>Enable to forward your local HTTPS based git credentials to workspaces</>,
    []
  )

  return { input, helpText }
}
