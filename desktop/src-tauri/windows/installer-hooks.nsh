; Hooks for Tauri's NSIS installer template, included before the template defines its own symbols.
!include LogicLib.nsh

; DevPod's bundle identifier used to be sh.loft.devpod, which made the installer's manufacturer "loft",
; so installers before it changed to io.curity.devpod (with publisher "Curity", see tauri.conf.json)
; kept the install folder in Software\loft\DevPod. The installer reads Software\Curity\DevPod to run
; the installed version's uninstaller, which it does by default when upgrading, so copy the old key
; before the pages are shown. The template's MANUFACTURER and PRODUCTNAME aren't defined yet here.
!define MUI_CUSTOMFUNCTION_GUIINIT DevPodMigrateInstallDirKey

Function DevPodMigrateInstallDirKey
  Push $0
  ReadRegStr $0 SHCTX "Software\Curity\DevPod" ""
  ${If} $0 == ""
    ReadRegStr $0 SHCTX "Software\loft\DevPod" ""
    ${If} $0 != ""
      WriteRegStr SHCTX "Software\Curity\DevPod" "" $0
    ${EndIf}
  ${EndIf}
  Pop $0
FunctionEnd

!macro NSIS_HOOK_POSTINSTALL
  DeleteRegKey SHCTX "Software\loft\DevPod"
  DeleteRegKey /ifempty SHCTX "Software\loft"
!macroend
