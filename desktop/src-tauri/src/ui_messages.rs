use crate::AppState;
use crate::{custom_protocol::ParseError, window::WindowHelper, AppHandle};
use log::{error, warn};
use serde::{Deserialize, Serialize};
use std::collections::VecDeque;
use tauri::{Emitter, Manager, State};
use tokio::sync::mpsc::Receiver;

pub async fn send_ui_message(
    app_state: State<'_, AppState>,
    msg: UiMessage,
    log_msg_on_failure: &str,
) {
    if let Err(err) = app_state.ui_messages.send(msg).await {
        error!("{}: {:?}, {}", log_msg_on_failure, err.0, err);
    };
}

#[derive(Debug, Clone)]
pub struct UiMessageHelper {
    app_handle: AppHandle,
    app_name: String,
    window_helper: WindowHelper,
    message_buffer: VecDeque<UiMessage>,
    is_ready: bool,
}

impl UiMessageHelper {
    pub fn new(app_handle: AppHandle, app_name: String, window_helper: WindowHelper) -> Self {
        Self {
            app_handle,
            app_name,
            window_helper,
            message_buffer: VecDeque::new(),
            is_ready: false,
        }
    }

    pub async fn listen(&mut self, mut receiver: Receiver<UiMessage>) {
        while let Some(ui_msg) = receiver.recv().await {
            match ui_msg {
                UiMessage::Ready => {
                    self.is_ready = true;

                    self.app_handle.get_webview_window("main").map(|w| w.show());
                    while let Some(msg) = self.message_buffer.pop_front() {
                        let emit_result = self.app_handle.emit("event", msg);
                        if let Err(err) = emit_result {
                            warn!("Error sending message: {}", err);
                        }
                    }
                }
                UiMessage::ExitRequested => {
                    self.is_ready = false;
                }
                // send all other messages to the UI
                _ => self.handle_msg(ui_msg),
            }
        }
    }

    fn handle_msg(&mut self, msg: UiMessage) {
        if self.is_ready {
            self.app_handle.get_webview_window("main").map(|w| w.show());
            let _ = self.app_handle.emit("event", msg);
        } else {
            // recreate window
            self.message_buffer.push_back(msg);

            // create a new main window if we can't find it
            let main_window = self.app_handle.get_webview_window("main");
            if main_window.is_none() {
                let _ = self.window_helper.new_main(self.app_name.clone());
            }
        }
    }
}

#[derive(Debug, Serialize, Clone)]
#[serde(tag = "type")]
#[allow(dead_code)]
pub enum UiMessage {
    Ready,
    ExitRequested,
    ShowDashboard,
    ShowToast(ShowToastMsg),
    OpenWorkspace(OpenWorkspaceMsg),
    CommandFailed(ParseError),
}

#[derive(Debug, Serialize, Clone)]
pub struct ShowToastMsg {
    title: String,
    message: String,
    status: ToastStatus,
}

impl ShowToastMsg {
    pub fn new(title: String, message: String, status: ToastStatus) -> Self {
        Self {
            title,
            message,
            status,
        }
    }
}

// WARN: Needs to match the UI's toast status
#[derive(Debug, Serialize, Clone)]
#[serde(rename_all = "lowercase")]
#[allow(dead_code)]
pub enum ToastStatus {
    Success,
    Error,
    Warning,
    Info,
    Loading,
}

#[derive(Debug, PartialEq, Serialize, Deserialize, Clone)]
#[serde(deny_unknown_fields)]
pub struct OpenWorkspaceMsg {
    #[serde(rename(deserialize = "workspace"))]
    pub workspace_id: Option<String>,
    #[serde(rename(deserialize = "provider"))]
    pub provider_id: Option<String>,
    pub ide: Option<String>,
    pub source: Option<String>,
}

impl OpenWorkspaceMsg {
    pub fn empty() -> OpenWorkspaceMsg {
        OpenWorkspaceMsg {
            workspace_id: None,
            provider_id: None,
            ide: None,
            source: None,
        }
    }
    pub fn with_id(id: String) -> OpenWorkspaceMsg {
        OpenWorkspaceMsg {
            workspace_id: Some(id),
            provider_id: None,
            ide: None,
            source: None,
        }
    }
}
