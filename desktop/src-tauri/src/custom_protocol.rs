use crate::ui_messages::{send_ui_message, OpenWorkspaceMsg, UiMessage};
use crate::AppState;
use log::{info, warn};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager, State};
use tauri_plugin_deep_link::DeepLinkExt;
use thiserror::Error;
use url::Url;

pub struct CustomProtocol;

pub struct Request {
    host: String,
    query: String,
}

pub struct UrlParser {}

impl UrlParser {
    const ALLOWED_METHODS: [&'static str; 1] = ["open"];

    fn get_host(url: &Url) -> String {
        url.host_str().unwrap_or("no host").to_string()
    }

    fn parse_raw_url(url_scheme: &str) -> Result<Url, ParseError> {
        Url::parse(url_scheme).map_err(|_| ParseError::InvalidQuery(url_scheme.to_string()))
    }

    fn is_allowed_method(host_str: &str) -> bool {
        Self::ALLOWED_METHODS.contains(&host_str)
    }

    fn parse_query(url: &Url) -> String {
        url.query().unwrap_or("").to_string()
    }

    pub fn parse(url_scheme: &str) -> Result<Request, ParseError> {
        let url = Self::parse_raw_url(url_scheme)?;
        let host_str = Self::get_host(&url);

        if !Self::is_allowed_method(&host_str) {
            return Err(ParseError::UnsupportedHost(host_str));
        }
        return Ok(Request {
            host: host_str,
            query: Self::parse_query(&url),
        });
    }
}

pub struct OpenHandler {}

impl OpenHandler {
    pub async fn handle(msg: Result<OpenWorkspaceMsg, ParseError>, app_state: State<'_, AppState>) {
        match msg {
            Ok(msg) => Self::handle_ok(msg, app_state).await,
            Err(err) => Self::handle_error(err, app_state).await,
        }
    }

    async fn handle_ok(msg: OpenWorkspaceMsg, app_state: State<'_, AppState>) {
        // try to send to UI if ready, otherwise buffer and let ui_ready handle
        send_ui_message(
            app_state,
            UiMessage::OpenWorkspace(msg),
            "Failed to broadcast custom protocol message",
        )
        .await;
    }

    async fn handle_error(err: ParseError, app_state: State<'_, AppState>) {
        #[cfg(not(target_os = "windows"))]
        send_ui_message(
            app_state,
            UiMessage::CommandFailed(err),
            "Failed to broadcast invalid custom protocol message",
        )
        .await;
    }
}

impl CustomProtocol {
    /// Handles the `devpod://` URLs the app is opened with. Must run in the app's setup hook, after the deep-link
    /// plugin was initialized.
    ///
    /// On macOS the URLs come from the system (`RunEvent::Opened`). On Windows and Linux opening a URL starts a new
    /// instance with the URL as its only argument: the single-instance plugin forwards it to the running instance,
    /// and the deep-link plugin reads it from the arguments when there is no running instance.
    pub fn setup(app: AppHandle) {
        let deep_link = app.deep_link();

        let app_handle = app.clone();
        deep_link.on_open_url(move |event| {
            for url in event.urls() {
                Self::handle_url(&app_handle, &url);
            }
        });

        // The URL the app was started with was received before the handler above was registered
        match deep_link.get_current() {
            Ok(Some(urls)) => {
                for url in urls {
                    Self::handle_url(&app, &url);
                }
            }
            Ok(None) => {}
            Err(err) => warn!("Failed to get the URL the app was opened with: {}", err),
        }

        // The installers register the URL scheme, but an AppImage or an app that wasn't installed doesn't
        #[cfg(any(target_os = "linux", target_os = "windows"))]
        if let Err(error) = deep_link.register_all() {
            Self::handle_register_error(&app, error);
        }
    }

    fn handle_url(app: &AppHandle, url: &Url) {
        tauri::async_runtime::block_on(async {
            info!("App opened with URL: {:?}", url.to_string());

            let request = UrlParser::parse(url.as_str());
            let app_state = app.state::<AppState>();
            if let Err(err) = request {
                warn!("Failed to broadcast custom protocol message: {:?}", err);
                return;
            }
            let request = request.unwrap();

            match request.host.as_str() {
                "open" => {
                    let msg = CustomProtocol::parse(&request);
                    OpenHandler::handle(msg, app_state).await
                }
                _ => {}
            }
        })
    }

    #[cfg(any(target_os = "linux", target_os = "windows"))]
    fn handle_register_error(app: &AppHandle, error: tauri_plugin_deep_link::Error) {
        #[cfg(target_os = "windows")]
        {
            let _ = app;
            warn!("Custom protocol setup failed: {}", error);
        }

        #[cfg(target_os = "linux")]
        {
            use crate::ui_messages::{ShowToastMsg, ToastStatus};

            if std::env::var("FLATPAK_ID").is_ok() {
                return;
            }

            let msg = "Either update-desktop-database or xdg-mime are missing. Please make sure they are available on your system";
            warn!("Custom protocol setup failed; {}: {}", msg, error);

            tauri::async_runtime::block_on(async {
                let app_state = app.state::<AppState>();
                let show_toast_msg = ShowToastMsg::new(
                    "Custom protocol handling needs to be configured".to_string(),
                    msg.to_string(),
                    ToastStatus::Warning,
                );
                if let Err(err) = app_state
                    .ui_messages
                    .send(UiMessage::ShowToast(show_toast_msg))
                    .await
                {
                    log::error!(
                        "Failed to broadcast show toast message: {:?}, {}",
                        err.0,
                        err
                    );
                };
            })
        }
    }

    fn parse<'a, Msg>(request: &'a Request) -> Result<Msg, ParseError>
    where
        Msg: Deserialize<'a>,
    {
        serde_qs::from_str::<Msg>(&request.query)
            .map_err(|_| ParseError::InvalidQuery(request.query.clone()))
    }
}

#[derive(Error, Debug, Clone, Serialize)]
pub enum ParseError {
    #[error("Unsupported host: {0}")]
    UnsupportedHost(String),
    #[error("Unsupported query arguments: {0}")]
    InvalidQuery(String),
}

#[cfg(test)]
mod tests {
    mod url_parser {
        use super::super::*;

        #[test]
        fn should_parse() {
            let url_str = "devpod://open?workspace=workspace";
            let request = UrlParser::parse(&url_str).unwrap();

            assert_eq!(request.host, "open".to_string());
            assert_eq!(request.query, "workspace=workspace".to_string());
        }

        #[test]
        fn should_parse_with_empty_query() {
            let url_str = "devpod://open";
            let request = UrlParser::parse(&url_str).unwrap();

            assert_eq!(request.host, "open".to_string());
            assert_eq!(request.query, "".to_string());
        }

        #[test]
        #[should_panic]
        fn should_fail_on_invalid_method() {
            let url_str = "devpod://something";
            let _ = UrlParser::parse(&url_str).unwrap();
        }

        #[test]
        #[should_panic]
        fn should_fail_on_invalid_scheme() {
            let url_str = "invalid-scheme";
            let _ = UrlParser::parse(&url_str).unwrap();
        }
    }

    mod custom_handler_open {
        use crate::custom_protocol::OpenWorkspaceMsg;

        use super::super::*;

        #[test]
        fn should_parse_full() {
            let url_str =
                "devpod://open?workspace=workspace&provider=provider&source=https://github.com/test123&ide=vscode";
            let request = UrlParser::parse(&url_str).unwrap();
            let got: OpenWorkspaceMsg = CustomProtocol::parse(&request).unwrap();

            assert_eq!(got.workspace_id, Some("workspace".to_string()));
            assert_eq!(got.provider_id, Some("provider".into()));
            assert_eq!(got.source, Some("https://github.com/test123".to_string()));
            assert_eq!(got.ide, Some("vscode".into()));
        }

        #[test]
        fn should_parse_workspace() {
            let url_str = "devpod://open?workspace=some-workspace";
            let request = UrlParser::parse(&url_str).unwrap();
            let got: OpenWorkspaceMsg = CustomProtocol::parse(&request).unwrap();

            assert_eq!(got.workspace_id, Some("some-workspace".to_string()));
            assert_eq!(got.provider_id, None);
            assert_eq!(got.source, None);
            assert_eq!(got.ide, None)
        }

        #[test]
        fn should_parse() {
            let url_str = "devpod://open?source=some-source";
            let request = UrlParser::parse(&url_str).unwrap();
            let got: OpenWorkspaceMsg = CustomProtocol::parse(&request).unwrap();

            assert_eq!(got.workspace_id, None);
            assert_eq!(got.provider_id, None);
            assert_eq!(got.source, Some("some-source".to_string()));
            assert_eq!(got.ide, None)
        }
    }
}
