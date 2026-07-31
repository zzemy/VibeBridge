use std::process::Child;
use std::sync::Mutex;
use std::time::Duration;
use tauri::{Manager, WindowEvent};
use tauri_plugin_shell::ShellExt;

struct AppState {
    agent_process: Mutex<Option<Child>>,
    agent_port: Mutex<u16>,
}

impl Default for AppState {
    fn default() -> Self {
        Self {
            agent_process: Mutex::new(None),
            agent_port: Mutex::new(8787),
        }
    }
}

/// Start the Go agent as a sidecar process.
fn start_agent(app: &tauri::AppHandle) -> Result<Child, String> {
    let port = 8787u16;

    let sidecar = app
        .shell()
        .sidecar("vibebridge-agent")
        .map_err(|e| format!("Failed to find sidecar binary: {}", e))?;

    log::info!("Starting VibeBridge agent on port {}", port);

    let child = sidecar
        .args(["--port", &port.to_string()])
        .spawn()
        .map_err(|e| format!("Failed to spawn agent: {}", e))?;

    Ok(child)
}

/// Check if the agent is responding on its HTTP status endpoint
async fn check_agent_health(port: u16) -> bool {
    let url = format!("http://127.0.0.1:{}/status", port);
    reqwest::Client::new()
        .get(&url)
        .timeout(Duration::from_secs(2))
        .send()
        .await
        .map(|r| r.status().is_success())
        .unwrap_or(false)
}

#[tauri::command]
async fn get_agent_status(state: tauri::State<'_, AppState>) -> Result<serde_json::Value, String> {
    let port = *state.agent_port.lock().unwrap();
    let running = check_agent_health(port).await;

    let url = format!("http://127.0.0.1:{}/status", port);
    let info = if running {
        reqwest::Client::new()
            .get(&url)
            .timeout(Duration::from_secs(2))
            .send()
            .await
            .ok()
            .and_then(|r| r.json::<serde_json::Value>().ok())
            .unwrap_or(serde_json::json!({}))
    } else {
        serde_json::json!({})
    };

    Ok(serde_json::json!({
        "running": running,
        "port": port,
        "info": info,
    }))
}

#[tauri::command]
async fn restart_agent(app: tauri::AppHandle, state: tauri::State<'_, AppState>) -> Result<(), String> {
    if let Some(mut child) = state.agent_process.lock().unwrap().take() {
        let _ = child.kill();
        let _ = child.wait();
    }
    tokio::time::sleep(Duration::from_millis(500)).await;
    match start_agent(&app) {
        Ok(child) => {
            *state.agent_process.lock().unwrap() = Some(child);
            Ok(())
        }
        Err(e) => Err(e),
    }
}

#[tauri::command]
fn get_ws_url(state: tauri::State<'_, AppState>) -> String {
    let port = *state.agent_port.lock().unwrap();
    format!("ws://127.0.0.1:{}/ws", port)
}

#[tauri::command]
fn get_http_url(state: tauri::State<'_, AppState>) -> String {
    let port = *state.agent_port.lock().unwrap();
    format!("http://127.0.0.1:{}", port)
}

#[tauri::command]
async fn fetch_pairing_code(state: tauri::State<'_, AppState>) -> Result<serde_json::Value, String> {
    let port = *state.agent_port.lock().unwrap();
    let url = format!("http://127.0.0.1:{}/pairing/code", port);
    reqwest::Client::new()
        .get(&url)
        .timeout(Duration::from_secs(5))
        .send()
        .await
        .map_err(|e| format!("Failed to fetch pairing code: {}", e))?
        .json::<serde_json::Value>()
        .await
        .map_err(|e| format!("Failed to parse pairing response: {}", e))
}

pub fn run() {
    env_logger::init();

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            Some(vec!["--autostart"]),
        ))
        .plugin(tauri_plugin_notification::init())
        .manage(AppState::default())
        .invoke_handler(tauri::generate_handler![
            get_agent_status,
            restart_agent,
            get_ws_url,
            get_http_url,
            fetch_pairing_code,
        ])
        .setup(|app| {
            log::info!("VibeBridge desktop app starting...");

            let app_handle = app.handle().clone();
            let state: tauri::State<AppState> = app.state();

            match start_agent(&app_handle) {
                Ok(child) => {
                    log::info!("Agent started successfully");
                    *state.agent_process.lock().unwrap() = Some(child);
                }
                Err(e) => {
                    log::error!("Failed to start agent: {}. Running in standalone mode.", e);
                }
            }

            // System tray
            let _tray = tauri::tray::TrayIconBuilder::new()
                .tooltip("VibeBridge")
                .icon(app.default_window_icon().unwrap().clone())
                .menu(&tauri::menu::Menu::with_items(
                    app,
                    &[
                        &tauri::menu::MenuItem::with_id(app, "show", "Show VibeBridge", true, None::<&str>)?,
                        &tauri::menu::MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?,
                    ],
                )?)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => {
                        if let Some(window) = app.get_webview_window("main") {
                            let _ = window.show();
                            let _ = window.set_focus();
                        }
                    }
                    "quit" => {
                        app.exit(0);
                    }
                    _ => {}
                })
                .build(app)?;

            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                // Hide to tray instead of closing
                let _ = window.hide();
                api.prevent_close();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
