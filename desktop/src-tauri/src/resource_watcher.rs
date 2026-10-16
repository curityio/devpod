use crate::{
    commands::{list_workspaces::ListWorkspacesCommand, DevpodCommandError},
    system_tray::ToSystemTraySubmenu,
};
use crate::{AppHandle, AppState};
use log::info;
use serde::Deserialize;
use std::{collections::HashSet, hash::Hash, time};
use tauri::{
    menu::{MenuItem, Submenu, SubmenuBuilder},
    Manager, Wry,
};

pub trait Identifiable {
    type ID: Eq + Hash + Clone;
    fn id(&self) -> Self::ID;
}
#[derive(Default)]
pub struct WorkspacesState {
    workspaces: Vec<Workspace>,
    submenu: Option<Submenu<Wry>>,
}

#[derive(Deserialize, Clone)]
pub struct Workspace {
    id: String,
    #[serde(skip)]
    menu_item: Option<MenuItem<Wry>>,
}
impl Identifiable for Workspace {
    type ID = String;
    fn id(&self) -> String {
        return self.id.clone();
    }
}
impl PartialEq for Workspace {
    fn eq(&self, other: &Self) -> bool {
        self.id() == other.id()
    }
}
impl Workspace {
    fn new_menu_item(&self, app_handle: &AppHandle) -> tauri::Result<MenuItem<Wry>> {
        return MenuItem::with_id(
            app_handle,
            WorkspacesState::item_id(&self.id()),
            self.id(),
            true,
            None::<&str>,
        );
    }
}

impl WorkspacesState {
    pub const IDENTIFIER_PREFIX: &'static str = "workspaces-";
    pub const CREATE_WORKSPACE_ID: &'static str = "workspaces-create_workspace";

    fn item_id(id: &String) -> String {
        format!("{}{}", Self::IDENTIFIER_PREFIX, id)
    }

    pub fn set_submenu(&mut self, submenu: Submenu<Wry>) {
        self.submenu = Some(submenu);
    }

    pub async fn load_workspaces(
        app_handle: &AppHandle,
    ) -> Result<Vec<Workspace>, DevpodCommandError> {
        let list_workspaces_cmd = ListWorkspacesCommand::new();

        return list_workspaces_cmd.exec(app_handle).await;
    }
}

impl ToSystemTraySubmenu for WorkspacesState {
    fn to_submenu(&self, app_handle: &AppHandle) -> anyhow::Result<tauri::menu::Submenu<Wry>> {
        let mut submenu = SubmenuBuilder::with_id(app_handle, "workspace", "Workspaces");

        let create_workspace = MenuItem::with_id(
            app_handle,
            Self::CREATE_WORKSPACE_ID,
            "Create Workspace",
            true,
            None::<&str>,
        )?;
        submenu = submenu.item(&create_workspace);
        submenu = submenu.separator();

        return Ok(submenu.build()?);
    }
}

pub fn setup(app_handle: &AppHandle) {
    let state = app_handle.state::<AppState>();
    let mut resource_handles = state.resources_handles.lock().unwrap();
    // main resources watchers
    let resources_app_handle = app_handle.clone();
    let resources_handle = tauri::async_runtime::spawn(async move {
        let sleep_duration = time::Duration::from_millis(5_000);
        loop {
            handle_workspaces(&resources_app_handle).await;
            let _ = tokio::time::sleep(sleep_duration).await;
        }
    });
    resource_handles.push(resources_handle);
}

pub async fn shutdown(app_handle: &AppHandle) {
    info!("Shutting down resource watchers");
    let state = app_handle.state::<AppState>();
    // shut down background tasks
    let mut handles = state.resources_handles.lock().unwrap();
    for handle in handles.iter() {
        handle.abort();
    }
    handles.clear();
}

async fn handle_workspaces(app_handle: &AppHandle) {
    let workspaces = WorkspacesState::load_workspaces(app_handle).await;
    if workspaces.is_err() {
        return;
    }

    let mut workspaces = workspaces.unwrap();
    let state = app_handle.state::<AppState>();
    let state = &mut state.workspaces.write().await;
    if workspaces == state.workspaces {
        return;
    }

    if let Some(submenu) = &state.submenu {
        let (removed, added) = diff_mut(&state.workspaces, &mut workspaces);
        for w in removed {
            if let Some(menu_item) = &w.menu_item {
                _ = submenu.remove(menu_item);
            }
        }
        for w in added {
            if let Ok(menu_item) = w.new_menu_item(app_handle) {
                let _ = submenu.append(&menu_item);
                w.menu_item = Some(menu_item);
            }
        }
    }
    state.workspaces = workspaces;
}

fn diff_mut<'a, T: Identifiable>(old: &'a [T], new: &'a mut [T]) -> (Vec<&'a T>, Vec<&'a mut T>) {
    let old_ids: HashSet<_> = old.iter().map(|item| item.id()).collect();
    let new_ids: HashSet<_> = new.iter().map(|item| item.id()).collect();

    let removed: Vec<_> = old
        .iter()
        .filter(|ws| !new_ids.contains(&ws.id()))
        .collect();

    let added: Vec<_> = new
        .iter_mut()
        .filter(|ws| !old_ids.contains(&ws.id()))
        .collect();

    return (removed, added);
}
