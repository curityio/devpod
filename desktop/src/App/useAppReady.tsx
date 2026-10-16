import {
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  useDisclosure,
  useToast,
} from "@chakra-ui/react"
import { getCurrentWebviewWindow } from "@tauri-apps/api/webviewWindow"
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import { client } from "../client"
import { ErrorMessageBox } from "../components"
import { WORKSPACE_SOURCE_BRANCH_DELIMITER, WORKSPACE_SOURCE_COMMIT_DELIMITER } from "../constants"
import { startWorkspaceAction, useWorkspaceStore } from "../contexts"
import { exists } from "../lib"
import { Routes } from "../routes"
import { useChangelogModal } from "./useChangelogModal"

export function useAppReady() {
  const { store } = useWorkspaceStore()
  const isReadyLockRef = useRef<boolean>(false)
  const viewID = useId()
  const navigate = useNavigate()
  const toast = useToast()
  const { modal: errorModal, setFailedMessage } = useErrorModal()
  const { modal: changelogModal } = useChangelogModal(isReadyLockRef.current)

  useEffect(() => {
    window.addEventListener("contextmenu", (e) => {
      e.preventDefault()

      return false
    })
  }, [])

  const handleMessage: Parameters<typeof client.subscribe>[1] = useCallback(
    async (event) => {
      if (event.type === "ShowDashboard") {
        if (await getCurrentWebviewWindow().isMinimized()) {
          await getCurrentWebviewWindow().unminimize()
        }

        if (!(await getCurrentWebviewWindow().isVisible())) {
          await getCurrentWebviewWindow().show()
        }

        await getCurrentWebviewWindow().setFocus()

        return
      }

      if (event.type === "ShowToast") {
        await getCurrentWebviewWindow().setFocus()

        toast({
          title: event.title,
          description: event.message,
          status: event.status,
          duration: 5_000,
          isClosable: true,
        })

        return
      }

      if (event.type === "CommandFailed") {
        await getCurrentWebviewWindow().setFocus()
        const message = Object.entries(event)
          .filter(([key]) => key !== "type")
          .map(([key, value]) => `${key}: ${value}`)
          .join("\n")
        setFailedMessage(message)

        return
      }

      // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition
      if (event.type === "OpenWorkspace") {
        const workspacesResult = await client.workspaces.listAll()
        if (workspacesResult.err) {
          return
        }

        // Try to find workspace by source
        let maybeWorkspace = workspacesResult.val.find((w) => {
          if (!w.source) {
            return false
          }

          // Check `repo@sha256:commitHash`
          if (
            `${w.source.gitRepository ?? ""}${WORKSPACE_SOURCE_COMMIT_DELIMITER}${
              w.source.gitCommit ?? ""
            }` === event.source
          ) {
            return true
          }

          // Check `repo@branch`
          if (
            `${w.source.gitRepository ?? ""}${WORKSPACE_SOURCE_BRANCH_DELIMITER}${
              w.source.gitBranch ?? ""
            }` === event.source
          ) {
            return true
          }

          // Check Git repo
          if (w.source.gitRepository === event.source) {
            return true
          }

          // Check local folder
          if (w.source.localFolder === event.source) {
            return true
          }

          // Check Docker Image
          if (w.source.image === event.source) {
            return true
          }

          return false
        })

        // If we don't have a workspace by now, `source` isn't defined but `workspace_id` is, try to find workspace by ID
        // This happens for example if the message is triggered by a system tray item
        // WARN: `event.source` can be an empty string here, hence the falsy check
        if (maybeWorkspace === undefined && !event.source && exists(event.workspace_id)) {
          maybeWorkspace = workspacesResult.val.find((w) => w.id === event.workspace_id)
        }

        const ides = await client.ides.listAll()
        let defaultIDE = undefined
        if (ides.ok) {
          defaultIDE = ides.val.find((ide) => ide.default)?.name
        }

        const providerName = maybeWorkspace?.provider?.name
        if (maybeWorkspace !== undefined && providerName) {
          const actionID = startWorkspaceAction({
            workspaceID: maybeWorkspace.id,
            streamID: viewID,
            config: {
              id: maybeWorkspace.id,
              providerConfig: { providerID: providerName },
              ideConfig: { name: defaultIDE ?? maybeWorkspace.ide?.name ?? null },
            },
            store,
          })

          navigate(Routes.toAction(actionID))

          return
        }

        navigate(
          Routes.toWorkspaceCreate({
            workspaceID: event.workspace_id,
            providerID: event.provider_id,
            rawSource: event.source,
            ide: event.ide,
          })
        )
      }
    },
    [navigate, setFailedMessage, store, toast, viewID]
  )

  // notifies underlying layer that ui is ready for communication
  useEffect(() => {
    const unsubscribePromise = client.subscribe("event", handleMessage)
    if (!isReadyLockRef.current) {
      isReadyLockRef.current = true

      unsubscribePromise.then(async () => {
        try {
          await client.ready()
        } catch (err) {
          return console.error(err)
        }
      })
    }

    return () => {
      unsubscribePromise.then((unsubscribe) => {
        unsubscribe()
      })
    }
  }, [handleMessage, navigate])

  return { errorModal, changelogModal }
}

function useErrorModal() {
  const [failedMessage, setFailedMessage] = useState<string | null>(null)
  const { isOpen, onClose, onOpen } = useDisclosure()
  const modal = useMemo(() => {
    return (
      <Modal
        onClose={onClose}
        isOpen={isOpen}
        onCloseComplete={() => setFailedMessage(null)}
        isCentered>
        <ModalOverlay />
        <ModalContent>
          <ModalCloseButton />
          {/* todo: customize the header */}
          <ModalHeader>Failed to open workspace from URL</ModalHeader>
          <ModalBody>
            <ErrorMessageBox error={Error(failedMessage!)} />
          </ModalBody>
          <ModalFooter />
        </ModalContent>
      </Modal>
    )
  }, [isOpen, onClose, failedMessage])

  useEffect(() => {
    if (failedMessage !== null) {
      onOpen()
    } else {
      onClose()
    }
  }, [onClose, onOpen, failedMessage])

  return { modal, handleOpen: onOpen, setFailedMessage }
}
