import {
  Button,
  HStack,
  Menu,
  MenuButton,
  MenuDivider,
  MenuItemOption,
  MenuList,
  MenuOptionGroup,
  Text,
} from "@chakra-ui/react"
import { WorkspaceStatus } from "@/icons"
import { WORKSPACE_STATUSES } from "@/constants"
import { WorkspaceStatusBadge } from "@/views/Workspaces/WorkspaceStatusBadge"
import { useCallback } from "react"
import { TWorkspace } from "@/types"

export type TWorkspaceStatusFilterState = string[] | "all"

export function WorkspaceStatusFilter({
  statusFilter,
  setStatusFilter,
}: {
  statusFilter: TWorkspaceStatusFilterState
  setStatusFilter: (statusFilter: TWorkspaceStatusFilterState) => void
}) {
  const availableStatuses = WORKSPACE_STATUSES

  const onSelectAll = useCallback(() => {
    if (statusFilter === "all") {
      setStatusFilter([])
    } else {
      setStatusFilter("all")
    }
  }, [statusFilter, setStatusFilter])

  const onChange = useCallback(
    (value: string | string[]) => {
      setStatusFilter(typeof value === "string" ? [value] : value)
    },
    [setStatusFilter]
  )

  return (
    <Menu closeOnSelect={false} offset={[0, 2]}>
      <MenuButton
        as={Button}
        variant="outline"
        leftIcon={<WorkspaceStatus boxSize={4} color="gray.600" />}>
        Status ({getCurrentFilterCount(statusFilter, availableStatuses.length)}/
        {availableStatuses.length})
      </MenuButton>
      <MenuList>
        <MenuItemOption
          isChecked={
            statusFilter.includes("all") || statusFilter.length === availableStatuses.length
          }
          onClick={onSelectAll}
          key="all"
          value="all">
          Select All
        </MenuItemOption>
        <MenuOptionGroup
          value={statusFilter === "all" ? (availableStatuses as unknown as string[]) : statusFilter}
          onChange={onChange}
          type="checkbox">
          <MenuDivider />
          {availableStatuses.map((status) => (
            <MenuItemOption key={status} value={status}>
              <HStack>
                <WorkspaceStatusBadge
                  status={status as TWorkspace["status"]}
                  isLoading={false}
                  hasError={false}
                  showText={false}
                />{" "}
                <Text> {status}</Text>
              </HStack>
            </MenuItemOption>
          ))}
        </MenuOptionGroup>
      </MenuList>
    </Menu>
  )
}

function getCurrentFilterCount(filter: TWorkspaceStatusFilterState, total: number) {
  if (filter === "all") {
    return total
  }

  return filter.length
}
